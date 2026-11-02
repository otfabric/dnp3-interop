// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The repository's fixtures are not in the build stage, so these tests carry
// their own. scripts/smoke.sh, interop.sh and features.sh test against the
// real ones.
const testFixture = `{
  "schemaVersion": "1.0", "name": "test",
  "outstation": {"address": 1024, "masterAddress": 1, "needTime": true, "unsolicited": true,
                 "coldRestartDelayMs": 5000, "warmRestartDelayMs": 1000, "maxControlsPerRequest": 2},
  "binaryInputs": [{"index": 0, "value": true, "class": 1}, {"index": 5, "value": false, "flags": ["ONLINE", "LOCAL_FORCED"]}],
  "binaryOutputs": [{"index": 0, "value": false, "target": 0, "class": 1, "commandEventClass": 1},
                    {"index": 1, "value": false, "selectRequired": true}],
  "counters": [{"index": 0, "value": 7, "class": 1}],
  "frozenCounters": [{"index": 0, "value": 0, "class": 1, "eventVariation": 5}],
  "analogInputs": [{"index": 0, "value": 0, "class": 2}, {"index": 1, "value": 3, "staticVariation": 3}],
  "analogOutputs": [{"index": 0, "value": 0, "target": 0, "min": -100, "max": 100}],
  "frozenAnalogInputs": [{"index": 0, "value": 0}],
  "octetStrings": [{"index": 0, "value": "0102"}],
  "timeAndIntervals": [{"index": 0, "time": "2026-01-02T03:04:05.006Z", "interval": 15, "units": 3}],
  "files": [{"name": "/readme.txt", "content": "68656c6c6f"}],
  "directories": ["/logs"],
  "deviceAttributes": [
    {"set": 1, "variation": 1, "type": "visibleString", "value": "test"},
    {"set": 1, "variation": 2, "type": "unsignedInt", "value": 4000000000},
    {"set": 1, "variation": 10, "type": "signedInt", "value": -42, "writable": true}
  ]
}`

const updateKey = "000102030405060708090a0b0c0d0e0f"

type output struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (o *output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.Write(p)
}

// take returns the documents written so far and forgets them.
func (o *output) take(t *testing.T) []map[string]any {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	var docs []map[string]any
	scan := bufio.NewScanner(&o.buf)
	scan.Buffer(nil, 1<<24)
	for scan.Scan() {
		var d map[string]any
		if err := json.Unmarshal(scan.Bytes(), &d); err != nil {
			t.Fatalf("not one JSON document per line: %q: %v", scan.Text(), err)
		}
		docs = append(docs, d)
	}
	o.buf.Reset()
	return docs
}

func capture(t *testing.T) *output {
	t.Helper()
	o := &output{}
	stdoutMu.Lock()
	previous := stdout
	stdout = o
	stdoutMu.Unlock()
	t.Cleanup(func() {
		stdoutMu.Lock()
		stdout = previous
		stdoutMu.Unlock()
	})
	return o
}

func tempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
}

// shortDir is a directory whose path fits a Unix socket address.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "dnp3")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

type testServer struct {
	common  []string // the client flags that reach it
	control string   // its control socket
}

// startServer runs the server until the test ends.
func startServer(t *testing.T, out *output, extra ...string) testServer {
	t.Helper()
	port := freePort(t)
	readyFile := filepath.Join(t.TempDir(), "ready")
	control := filepath.Join(shortDir(t), "c.sock")
	done := make(chan int, 1)
	go func() {
		done <- run(append([]string{"server", "--bind-address", "127.0.0.1", "--port", port,
			"--fixture", tempFile(t, "fixture.json", testFixture), "--ready-file", readyFile,
			"--control-socket", control}, extra...))
	}()
	t.Cleanup(func() {
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		select {
		case code := <-done:
			if code != exitOK {
				t.Errorf("the server exited with %d", code)
			}
		case <-time.After(10 * time.Second):
			t.Error("the server did not stop")
		}
		if _, err := os.Stat(readyFile); err == nil {
			t.Error("the ready file is still there")
		}
	})
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(readyFile); err == nil {
			break
		}
		select {
		case code := <-done:
			t.Fatalf("the server exited with %d", code)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the server is not ready")
		}
	}
	return testServer{common: []string{"--host", "127.0.0.1", "--port", port, "--timeout-ms", "2000"}, control: control}
}

// runOp runs an operation; it returns the exit code, the result and the
// server events written meanwhile.
func runOp(t *testing.T, out *output, common []string, op string, args ...string) (int, map[string]any, []map[string]any) {
	t.Helper()
	code := run(append(append([]string{"client", op}, common...), args...))
	time.Sleep(100 * time.Millisecond) // server events of the operation
	var result map[string]any
	var events []map[string]any
	for _, d := range out.take(t) {
		if _, ok := d["event"]; ok {
			events = append(events, d)
		} else if result == nil {
			result = d
		} else {
			t.Fatalf("a second result document: %v", d)
		}
	}
	if result == nil {
		t.Fatalf("client %s: no result document", op)
	}
	return code, result, events
}

// ok runs an operation that must succeed.
func ok(t *testing.T, out *output, common []string, op string, args ...string) (map[string]any, []map[string]any) {
	t.Helper()
	code, r, events := runOp(t, out, common, op, args...)
	if code != exitOK || r["ok"] != true {
		t.Fatalf("client %s %v: exit %d, result %v", op, args, code, r)
	}
	return r, events
}

func errorCode(r map[string]any) string {
	if e, ok := r["error"].(map[string]any); ok {
		s, _ := e["code"].(string)
		return s
	}
	return ""
}

func list(v any) []map[string]any {
	var out []map[string]any
	for _, x := range v.([]any) {
		out = append(out, x.(map[string]any))
	}
	return out
}

// findObject finds the object of a group, variation and index.
func findObject(t *testing.T, r map[string]any, g, v, i int) map[string]any {
	t.Helper()
	for _, o := range list(r["objects"]) {
		if o["group"] == float64(g) && o["variation"] == float64(v) && o["index"] == float64(i) {
			return o
		}
	}
	t.Fatalf("no g%dv%d index %d in %v", g, v, i, r["objects"])
	return nil
}

// eventLine is the events of a kind as "field=value" pairs.
func eventLine(events []map[string]any, kind string, fields ...string) string {
	var out []string
	for _, e := range events {
		if e["event"] != kind {
			continue
		}
		var parts []string
		for _, f := range fields {
			parts = append(parts, fmt.Sprintf("%v", e[f]))
		}
		out = append(out, strings.Join(parts, ":"))
	}
	return strings.Join(out, " ")
}

func TestCapabilities(t *testing.T) {
	out := capture(t)
	if code := run([]string{"print-capabilities"}); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	docs := out.take(t)
	if len(docs) != 1 {
		t.Fatalf("%d documents", len(docs))
	}
	c := docs[0]
	up := c["upstream"].(map[string]any)
	if c["adapter"] != "otfabric" || up["independentEngine"] != false || up["license"] != "MIT" || up["name"] != "go-dnp3" {
		t.Errorf("capabilities %v", c)
	}
	sa := c["secureAuthentication"].(map[string]any)
	for _, m := range sa["macAlgorithms"].([]any) {
		if m == "aes-gmac" {
			t.Error("aes-gmac is declared, the library has none")
		}
	}
	features := c["features"].(map[string]any)
	for _, f := range []string{"tls", "udp", "serial", "secureAuthentication", "fileServer", "manageClient", "deadbandReadServer"} {
		if features[f] != true {
			t.Errorf("feature %s is not declared", f)
		}
	}
	ops := c["clientOperations"].([]any)
	if len(ops) != len(clientOperations) {
		t.Errorf("clientOperations %v", ops)
	}
}

func TestUsageErrors(t *testing.T) {
	capture(t)
	for _, argv := range [][]string{
		{"nonsense"},
		{"client"},
		{"client", "nonsense", "--host", "h"},
		{"client", "integrity-poll"},
		{"client", "integrity-poll", "--host", "h", "--unknown", "1"},
		{"client", "operate", "--host", "h", "--type", "g12v1", "--index", "0"},
		{"client", "operate", "--host", "h", "--type", "g41v2", "--index", "0", "--value", "70000"},
		{"client", "operate", "--host", "h", "--type", "g12v1", "--index", "0", "--code", "LATCH_ON", "--broadcast"},
		{"client", "freeze", "--host", "h", "--group", "21"},
		{"client", "freeze", "--host", "h", "--interval-ms", "5"},
		{"client", "read", "--host", "h", "--group", "1", "--variation", "2", "--start", "3"},
		{"client", "file-info", "--host", "h", "--name", "/a", "--file-user", "u", "--file-password", "p"},
		{"client", "file-get", "--host", "h", "--name", "relative"},
		{"client", "integrity-poll", "--host", "h", "--sa-update-key", updateKey, "--sa-mac", "aes-gmac"},
		{"client", "integrity-poll", "--host", "h", "--sa-user", "2"},
		{"client", "integrity-poll", "--transport", "udp", "--host", "h", "--tls"},
		{"client", "integrity-poll", "--transport", "serial", "--host", "h", "--serial-device", "/dev/null"},
		{"client", "manage", "--host", "h", "--function", "activate-config"},
		{"server", "--frozen-event-policy", "always"},
		{"server", "--tls-cert", "c.pem"},
		{"server", "--outstations", "2", "--connect", "h:1"},
		{"counter-increment"},
		{"print-fixture", "../x"},
	} {
		if code := run(argv); code != exitUsage {
			t.Errorf("%v: exit %d, want %d", argv, code, exitUsage)
		}
	}
}

func TestInvalidFixtures(t *testing.T) {
	capture(t)
	for name, fixture := range map[string]string{
		"not JSON":      `{`,
		"no outstation": `{"schemaVersion": "1.0", "name": "x"}`,
		"bad variation": `{"schemaVersion": "1.0", "name": "x", "outstation": {"address": 1024, "masterAddress": 1},
			"analogInputs": [{"index": 0, "value": 1, "staticVariation": 9}]}`,
	} {
		code := run([]string{"server", "--port", freePort(t), "--fixture", tempFile(t, "f.json", fixture),
			"--ready-file", filepath.Join(t.TempDir(), "ready")})
		if code != exitFixtureInvalid {
			t.Errorf("%s: exit %d, want %d", name, code, exitFixtureInvalid)
		}
	}
}

func TestConnectFailed(t *testing.T) {
	out := capture(t)
	code, r, _ := runOp(t, out, []string{"--host", "127.0.0.1", "--port", freePort(t), "--connect-timeout-ms", "500"}, "integrity-poll")
	if code != exitConnectFailed || errorCode(r) != "connect-failed" || r["connected"] != false {
		t.Errorf("exit %d, result %v", code, r)
	}
}

func TestCounterIncrementWithoutServer(t *testing.T) {
	out := capture(t)
	code := run([]string{"counter-increment", "--index", "0", "--control-socket", filepath.Join(shortDir(t), "none")})
	docs := out.take(t)
	if code != exitConnectFailed || len(docs) != 1 || errorCode(docs[0]) != "connect-failed" {
		t.Errorf("exit %d, documents %v", code, docs)
	}
}

func TestAgainstOwnServer(t *testing.T) {
	out := capture(t)
	s := startServer(t, out)
	ready := out.take(t)
	if len(ready) != 1 || ready[0]["event"] != "ready" || ready[0]["transport"] != "tcp" || ready[0]["linkAddress"] != 1024.0 {
		t.Fatalf("ready event %v", ready)
	}
	c := s.common

	t.Run("integrity poll", func(t *testing.T) {
		r, events := ok(t, out, c, "integrity-poll")
		if iin := fmt.Sprint(r["iin"]); !strings.Contains(iin, "NEED_TIME") || !strings.Contains(iin, "DEVICE_RESTART") {
			t.Errorf("iin %v", r["iin"])
		}
		if f := fmt.Sprint(findObject(t, r, 1, 2, 5)["flags"]); f != "[ONLINE LOCAL_FORCED]" {
			t.Errorf("flags %s", f)
		}
		// g30v3 has no flags: reported ONLINE.
		if f := fmt.Sprint(findObject(t, r, 30, 3, 1)["flags"]); f != "[ONLINE]" {
			t.Errorf("g30v3 flags %s", f)
		}
		if o := findObject(t, r, 110, 2, 0); o["value"] != "0102" {
			t.Errorf("octet string %v", o)
		}
		if eventLine(events, "connection-opened", "event") == "" || eventLine(events, "connection-closed", "event") == "" {
			t.Errorf("connection events %v", events)
		}
	})
	t.Run("read", func(t *testing.T) {
		r, _ := ok(t, out, c, "read", "--group", "50", "--variation", "4", "--start", "0", "--stop", "0")
		o := findObject(t, r, 50, 4, 0)
		if o["value"] != 15.0 || o["units"] != 3.0 || o["time"] != "2026-01-02T03:04:05.006Z" {
			t.Errorf("g50v4 %v", o)
		}
		r, _ = ok(t, out, c, "read", "--group", "1", "--variation", "0")
		if len(list(r["objects"])) != 2 {
			t.Errorf("all objects of group 1: %v", r["objects"])
		}
	})
	t.Run("time sync", func(t *testing.T) {
		for _, p := range []string{"lan", "non-lan"} {
			_, events := ok(t, out, c, "time-sync", "--procedure", p)
			if eventLine(events, "time-sync", "event") == "" {
				t.Errorf("%s: no time-sync event in %v", p, events)
			}
		}
		r, _ := ok(t, out, c, "integrity-poll")
		if strings.Contains(fmt.Sprint(r["iin"]), "NEED_TIME") {
			t.Errorf("NEED_TIME after a time sync: %v", r["iin"])
		}
	})
	t.Run("controls", func(t *testing.T) {
		r, events := ok(t, out, c, "operate", "--type", "g12v1", "--index", "0", "--code", "LATCH_OFF")
		if r["commandStatus"] != "SUCCESS" || eventLine(events, "command", "function", "index", "code", "status") != "direct-operate:0:4:SUCCESS" {
			t.Errorf("result %v, events %v", r, events)
		}
		r, _ = ok(t, out, c, "event-poll", "--classes", "1")
		findObject(t, r, 2, 2, 0)  // the target
		findObject(t, r, 13, 2, 0) // the command event
		_, events = ok(t, out, c, "operate", "--type", "g12v1", "--index", "1", "--code", "TRIP", "--mode", "sbo")
		if got := eventLine(events, "command", "function", "code"); got != "select:129 operate:129" {
			t.Errorf("sbo events %s", got)
		}
		code, r, _ := runOp(t, out, c, "operate", "--type", "g12v1", "--index", "1", "--code", "LATCH_ON")
		if code != exitFailed || errorCode(r) != "rejected" || r["commandStatus"] != nil {
			t.Errorf("select required: exit %d, result %v", code, r)
		}
		code, r, _ = runOp(t, out, c, "operate", "--type", "g41v3", "--index", "0", "--value", "500")
		if code != exitFailed || errorCode(r) != "command-failed" || r["commandStatus"] != "OUT_OF_RANGE" {
			t.Errorf("out of range: exit %d, result %v", code, r)
		}
		r, events = ok(t, out, c, "operate", "--type", "g41v2", "--index", "0", "--value", "-7", "--mode", "direct-no-ack")
		if r["commandStatus"] != nil || eventLine(events, "command", "function", "type", "value") != "direct-operate-no-ack:g41v2:-7" {
			t.Errorf("no ack: result %v, events %v", r, events)
		}
		code, r, _ = runOp(t, out, c, "operate-multi", "--controls", "g12v1:0:LATCH_OFF,g41v1:0:5,g41v1:0:6")
		if code != exitFailed || errorCode(r) != "command-failed" || fmt.Sprint(r["commandStatuses"]) != "[SUCCESS SUCCESS TOO_MANY_OPS]" {
			t.Errorf("operate-multi: exit %d, result %v", code, r)
		}
	})
	t.Run("restart", func(t *testing.T) {
		r, events := ok(t, out, c, "restart", "--type", "warm")
		if r["restartDelayMs"] != 1000.0 || eventLine(events, "restart", "type") != "warm" {
			t.Errorf("result %v, events %v", r, events)
		}
	})
	t.Run("freeze", func(t *testing.T) {
		_, _ = ok(t, out, c, "event-poll")
		if code := run([]string{"counter-increment", "--index", "0", "--by", "3", "--control-socket", s.control}); code != exitOK {
			t.Fatalf("counter-increment: exit %d", code)
		}
		if d := out.take(t); len(d) != 1 || d[0]["value"] != 10.0 {
			t.Fatalf("counter-increment %v", d)
		}
		ok(t, out, c, "freeze", "--clear")
		r, _ := ok(t, out, c, "read", "--group", "21", "--variation", "1", "--start", "0", "--stop", "0")
		if o := findObject(t, r, 21, 1, 0); o["value"] != 10.0 {
			t.Errorf("frozen counter %v", o)
		}
		ok(t, out, c, "freeze", "--group", "30", "--no-ack")
		ok(t, out, c, "freeze", "--at", "immediately", "--group", "20")
		ok(t, out, c, "freeze", "--at", "+100000", "--interval-ms", "1000", "--no-ack")
	})
	t.Run("deadbands", func(t *testing.T) {
		ok(t, out, c, "deadband-write", "--deadbands", "0:2.5")
		r, _ := ok(t, out, c, "read", "--group", "34", "--variation", "0")
		if o := findObject(t, r, 34, 3, 0); o["value"] != 2.5 || o["flags"] != nil {
			t.Errorf("deadband %v", o)
		}
	})
	t.Run("assign class", func(t *testing.T) {
		ok(t, out, c, "assign-class", "--class", "3", "--group", "30", "--start", "1", "--stop", "1")
	})
	t.Run("manage", func(t *testing.T) {
		_, events := ok(t, out, c, "manage", "--function", "start-application")
		if eventLine(events, "manage", "function") != "17" {
			t.Errorf("events %v", events)
		}
	})
	t.Run("attributes", func(t *testing.T) {
		r, _ := ok(t, out, c, "attribute-read", "--set", "1")
		var got []string
		for _, a := range list(r["attributes"]) {
			got = append(got, fmt.Sprintf("%v/%v/%v", a["variation"], a["type"], a["value"]))
		}
		for _, want := range []string{"1/visibleString/test", "2/unsignedInt/4e+09", "10/signedInt/-42"} {
			if !slices.Contains(got, want) {
				t.Errorf("attributes %v, want %s", got, want)
			}
		}
		_, events := ok(t, out, c, "attribute-write", "--set", "1", "--variation", "10", "--int", "7")
		if eventLine(events, "attribute-write", "set", "variation", "type", "value") != "1:10:signedInt:7" {
			t.Errorf("events %v", events)
		}
		code, r, _ := runOp(t, out, c, "attribute-write", "--set", "1", "--variation", "1", "--text", "x")
		if code != exitFailed || errorCode(r) != "rejected" {
			t.Errorf("read-only attribute: exit %d, result %v", code, r)
		}
	})
	t.Run("files", func(t *testing.T) {
		r, _ := ok(t, out, c, "file-get", "--name", "/readme.txt")
		if f := r["file"].(map[string]any); f["content"] != "68656c6c6f" || r["fileStatus"] != "SUCCESS" {
			t.Errorf("file-get %v", r)
		}
		r, _ = ok(t, out, c, "file-ls", "--name", "/")
		var names []string
		for _, e := range list(r["files"]) {
			names = append(names, fmt.Sprintf("%v:%v", e["name"], e["type"]))
		}
		if strings.Join(names, " ") != "logs:directory readme.txt:file" {
			t.Errorf("file-ls %v", names)
		}
		r, _ = ok(t, out, c, "file-info", "--name", "/readme.txt")
		if f := r["file"].(map[string]any); f["permissions"] != "rw-r--r--" || f["size"] != 5.0 {
			t.Errorf("file-info %v", f)
		}
		ok(t, out, c, "file-put", "--name", "/logs/a", "--content", "0102")
		_, events := ok(t, out, c, "file-put", "--name", "/logs/a", "--content", "03", "--append")
		if eventLine(events, "file", "action", "name", "status") != "append:/logs/a:SUCCESS" {
			t.Errorf("events %v", events)
		}
		r, _ = ok(t, out, c, "file-get", "--name", "/logs/a")
		if r["file"].(map[string]any)["content"] != "010203" {
			t.Errorf("appended file %v", r["file"])
		}
		ok(t, out, c, "file-delete", "--name", "/logs/a")
		code, r, _ := runOp(t, out, c, "file-get", "--name", "/logs/a")
		if code != exitFailed || errorCode(r) != "file-error" || r["fileStatus"] != "FILE_NOT_FOUND" {
			t.Errorf("deleted file: exit %d, result %v", code, r)
		}
	})
	t.Run("broadcast", func(t *testing.T) {
		_, events := ok(t, out, c, "operate", "--type", "g12v1", "--index", "0", "--code", "LATCH_ON",
			"--mode", "direct-no-ack", "--broadcast")
		if eventLine(events, "broadcast", "function") != "6" {
			t.Errorf("events %v", events)
		}
		r, _ := ok(t, out, c, "read", "--group", "10", "--variation", "2", "--start", "0", "--stop", "0")
		if !strings.Contains(fmt.Sprint(r["iin"]), "BROADCAST") {
			t.Errorf("iin %v", r["iin"])
		}
	})
	t.Run("keep-alive", func(t *testing.T) {
		_, events := ok(t, out, c, "monitor", "--duration-ms", "700", "--keep-alive-ms", "200")
		if eventLine(events, "link-status", "event") == "" {
			t.Errorf("no link-status event in %v", events)
		}
	})
}

func TestFileAuthentication(t *testing.T) {
	out := capture(t)
	s := startServer(t, out, "--file-user", "operator", "--file-password", "s3cret")
	out.take(t)
	c := s.common
	code, r, _ := runOp(t, out, c, "file-get", "--name", "/readme.txt")
	if code != exitFailed || r["fileStatus"] != "PERMISSION_DENIED" {
		t.Errorf("without credentials: exit %d, result %v", code, r)
	}
	code, r, events := runOp(t, out, c, "file-authenticate", "--file-user", "operator", "--file-password", "wrong")
	if code != exitFailed || errorCode(r) != "file-error" || r["fileStatus"] != "PERMISSION_DENIED" ||
		eventLine(events, "file", "action", "name", "status") != "authenticate:operator:PERMISSION_DENIED" {
		t.Errorf("wrong password: exit %d, result %v, events %v", code, r, events)
	}
	ok(t, out, c, "file-get", "--name", "/readme.txt", "--file-user", "operator", "--file-password", "s3cret")
}

func TestSecureAuthentication(t *testing.T) {
	out := capture(t)
	s := startServer(t, out, "--sa-update-key", updateKey, "--sa-statistics-threshold", "1")
	out.take(t)
	c := append(slices.Clone(s.common), "--sa-update-key", updateKey)
	r, events := ok(t, out, c, "operate", "--type", "g12v1", "--index", "0", "--code", "LATCH_ON")
	// The master clears the restart bit with a WRITE (2), also authenticated.
	if r["commandStatus"] != "SUCCESS" || !strings.HasPrefix(eventLine(events, "auth", "user", "function"), "1:5") {
		t.Errorf("result %v, events %v", r, events)
	}
	r, _ = ok(t, out, c, "read", "--group", "121", "--variation", "1", "--start", "0", "--stop", "17")
	if len(list(r["objects"])) != 18 {
		t.Errorf("security statistics %v", r["objects"])
	}
	code, r, events := runOp(t, out, s.common, "operate", "--type", "g12v1", "--index", "0", "--code", "LATCH_OFF",
		"--sa-update-key", "0f0e0d0c0b0a09080706050403020100")
	if code != exitFailed || errorCode(r) != "auth-failed" || eventLine(events, "auth-error", "code") != "1" {
		t.Errorf("wrong key: exit %d, result %v, events %v", code, r, events)
	}
}

// pki is a CA and identities signed by it, for TLS.
func pki(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)
	save := func(name, kind string, der []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	save("ca.pem", "CERTIFICATE", caDER)
	for i, name := range []string{"server", "client"} {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tmpl := &x509.Certificate{SerialNumber: big.NewInt(int64(i + 2)), Subject: pkix.Name{CommonName: name},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
			DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
			ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		save(name+".pem", "CERTIFICATE", der)
		pk, _ := x509.MarshalPKCS8PrivateKey(key)
		save(name+".key", "PRIVATE KEY", pk)
	}
	return dir
}

func TestTLS(t *testing.T) {
	out := capture(t)
	dir := pki(t)
	s := startServer(t, out, "--tls-cert", filepath.Join(dir, "server.pem"), "--tls-key", filepath.Join(dir, "server.key"),
		"--tls-ca", filepath.Join(dir, "ca.pem"))
	if ready := out.take(t); len(ready) != 1 || ready[0]["transport"] != "tls" {
		t.Fatalf("ready %v", ready)
	}
	tlsFlags := []string{"--tls", "--tls-ca", filepath.Join(dir, "ca.pem"), "--tls-cert", filepath.Join(dir, "client.pem"),
		"--tls-key", filepath.Join(dir, "client.key")}
	ok(t, out, append(slices.Clone(s.common), tlsFlags...), "integrity-poll")
	code, r, _ := runOp(t, out, s.common, "integrity-poll", "--tls", "--tls-ca", filepath.Join(dir, "client.pem"),
		"--tls-cert", filepath.Join(dir, "client.pem"), "--tls-key", filepath.Join(dir, "client.key"))
	if code != exitConnectFailed || errorCode(r) != "connect-failed" {
		t.Errorf("untrusted outstation: exit %d, result %v", code, r)
	}
	code, r, _ = runOp(t, out, s.common, "integrity-poll", "--tls", "--tls-ca", filepath.Join(dir, "missing.pem"),
		"--tls-cert", filepath.Join(dir, "client.pem"), "--tls-key", filepath.Join(dir, "client.key"))
	if code != exitFailed || errorCode(r) != "failed" || r["connected"] != false {
		t.Errorf("missing CA: exit %d, result %v", code, r)
	}
}

func TestDualEndPoint(t *testing.T) {
	out := capture(t)
	port := freePort(t)
	readyFile := filepath.Join(t.TempDir(), "ready")
	done := make(chan int, 1)
	go func() {
		done <- run([]string{"server", "--connect", "127.0.0.1:" + port, "--fixture", tempFile(t, "f.json", testFixture),
			"--ready-file", readyFile, "--control-socket", filepath.Join(shortDir(t), "c.sock")})
	}()
	defer func() {
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		if code := <-done; code != exitOK {
			t.Errorf("server exit %d", code)
		}
	}()
	r, _ := ok(t, out, []string{"--listen", "--bind-address", "127.0.0.1", "--port", port, "--connect-timeout-ms", "5000"}, "integrity-poll")
	if r["connected"] != true || len(list(r["objects"])) == 0 {
		t.Errorf("result %v", r)
	}
}

func TestUDP(t *testing.T) {
	out := capture(t)
	port := freePort(t)
	local := freePort(t)
	readyFile := filepath.Join(t.TempDir(), "ready")
	done := make(chan int, 1)
	go func() {
		done <- run([]string{"server", "--transport", "udp", "--bind-address", "127.0.0.1", "--port", port,
			"--udp-peer", "127.0.0.1:" + local, "--fixture", tempFile(t, "f.json", testFixture),
			"--ready-file", readyFile, "--control-socket", filepath.Join(shortDir(t), "c.sock")})
	}()
	defer func() {
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		if code := <-done; code != exitOK {
			t.Errorf("server exit %d", code)
		}
	}()
	time.Sleep(300 * time.Millisecond)
	ok(t, out, []string{"--transport", "udp", "--host", "127.0.0.1", "--port", port, "--udp-local-port", local}, "integrity-poll")
}

func TestSeveralOutstations(t *testing.T) {
	out := capture(t)
	s := startServer(t, out, "--outstations", "2")
	out.take(t)
	ok(t, out, append(slices.Clone(s.common), "--remote-addr", "1025"), "integrity-poll")
	code, r, _ := runOp(t, out, append(slices.Clone(s.common), "--remote-addr", "1026", "--timeout-ms", "500"), "integrity-poll")
	if code != exitFailed || errorCode(r) != "timeout" || r["iin"] != nil {
		t.Errorf("a third address: exit %d, result %v", code, r)
	}
}
