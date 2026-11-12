// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/otfabric/go-dnp3/app"
	"github.com/otfabric/go-dnp3/server"
)

// The server's control socket and the counter-increment command (contract
// 1.4). A running outstation and the command talk one line of JSON each way
// over a Unix socket; that exchange is private to this adapter.

const defaultControlSocket = "/run/dnp3-interop/control.sock"

type controlRequest struct {
	Operation string `json:"operation"`
	Index     uint16 `json:"index"`
	By        uint32 `json:"by"`
}

type controlResponse struct {
	OK      bool   `json:"ok"`
	Value   uint32 `json:"value,omitempty"`
	Message string `json:"message,omitempty"`
}

// increment adds by to a counter of the database, modulo 2^32. The value goes
// through the library's own update, which raises the counter's event with
// the outstation's time.
func increment(db *server.Database, index uint16, by uint32) (uint32, error) {
	v, ok := db.Get(server.Counter, uint32(index))
	c, isCounter := v.(app.Counter)
	if !ok || !isCounter {
		return 0, fmt.Errorf("no counter at index %d", index)
	}
	c.Value += by
	c.Time = 0 // stamped with the outstation's clock
	if err := db.Update(server.Counter, uint32(index), c, server.EventOnChange); err != nil {
		return 0, err
	}
	return c.Value, nil
}

// listenControl opens the control socket. The caller closes the listener.
func listenControl(path string, db *server.Database) (net.Listener, error) {
	_ = os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go serveControl(conn, db)
		}
	}()
	return l, nil
}

func serveControl(conn net.Conn, db *server.Database) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	var req controlRequest
	var res controlResponse
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err == nil {
		err = json.Unmarshal(line, &req)
	}
	if err == nil && req.Operation != "counter-increment" {
		err = errors.New("unknown control operation")
	}
	if err == nil {
		res.Value, err = increment(db, req.Index, req.By)
	}
	if err != nil {
		res.Message = err.Error()
	} else {
		res.OK = true
	}
	out, _ := json.Marshal(res)
	_, _ = conn.Write(append(out, '\n'))
}

// runCounterIncrement is the counter-increment command.
func runCounterIncrement(argv []string) int {
	start := time.Now()
	a, err := parseArgs(argv, []string{"index", "by", "control-socket", "timeout-ms"}, nil)
	var (
		index   uint16
		by      uint32
		timeout int64
	)
	if err == nil {
		index, err = a.requiredUint16Flag("index", 0, 65535)
	}
	if err == nil {
		by, err = a.uint32Flag("by", 1, 1, 4294967295)
	}
	if err == nil {
		timeout, err = a.integer("timeout-ms", 5000, 1, 3600000)
	}
	if err != nil {
		logf("counter-increment: %v", err)
		return exitUsage
	}
	doc := func(connected bool, value any, code, message string) object {
		var failure any
		if code != "" {
			failure = object{{"code", code}, {"message", message}}
		}
		return object{{"schemaVersion", schemaVersion}, {"adapter", adapter}, {"operation", "counter-increment"},
			{"ok", code == ""}, {"error", failure}, {"connected", connected}, {"iin", nil}, {"objects", []any{}},
			{"index", index}, {"value", value}, {"elapsedMs", time.Since(start).Milliseconds()}}
	}
	wait := time.Duration(timeout) * time.Millisecond
	conn, err := net.DialTimeout("unix", a.str("control-socket", defaultControlSocket), wait)
	if err != nil {
		emit(doc(false, nil, "connect-failed", err.Error()))
		return exitConnectFailed
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(wait))
	out, _ := json.Marshal(controlRequest{Operation: "counter-increment", Index: index, By: by})
	_, err = conn.Write(append(out, '\n'))
	var res controlResponse
	var line []byte
	if err == nil {
		line, err = bufio.NewReader(conn).ReadBytes('\n')
	}
	if err == nil {
		err = json.Unmarshal(line, &res)
	}
	if err == nil && !res.OK {
		err = errors.New(res.Message)
	}
	if err != nil {
		emit(doc(true, nil, "failed", err.Error()))
		return exitFailed
	}
	emit(doc(true, res.Value, "", ""))
	return exitOK
}
