// SPDX-License-Identifier: MIT

// The server's control socket and the counter-increment command (contract
// 1.4). The command sends one line of JSON to the running server over a
// Unix socket and reads one line back; that exchange is private to this
// adapter.

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	dnp3 "github.com/cB-Sigma/dnp3"
)

const defaultControlSocket = "/run/dnp3-interop/control.sock"

type controlMessage struct {
	Index uint16 `json:"index"`
	By    uint32 `json:"by"`
}

type controlAnswer struct {
	Value *uint32 `json:"value,omitempty"`
	Error string  `json:"error,omitempty"`
}

// increment adds to a counter through the library's update, which detects
// the change and raises the counter's event.
func increment(db *dnp3.PointDB, index uint16, by uint32) (uint32, error) {
	p, ok := db.Point("counter", uint32(index))
	if !ok {
		return 0, fmt.Errorf("no counter at index %d", index)
	}
	v := uint32(p.Value) + by // modulo 2^32
	if _, err := db.Update(context.Background(), "counter", uint32(index), float64(v), nil, p.Flags, time.Now()); err != nil {
		return 0, err
	}
	return v, nil
}

func listenControl(path string, db *dnp3.PointDB) (net.Listener, error) {
	_ = os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go answerControl(c, db)
		}
	}()
	return l, nil
}

func answerControl(c net.Conn, db *dnp3.PointDB) {
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	var m controlMessage
	var answer controlAnswer
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err == nil {
		err = json.Unmarshal(line, &m)
	}
	if err == nil {
		var v uint32
		if v, err = increment(db, m.Index, m.By); err == nil {
			answer.Value = &v
		}
	}
	if err != nil {
		answer.Error = err.Error()
	}
	out, _ := json.Marshal(answer)
	_, _ = c.Write(append(out, '\n'))
}

func runCounterIncrement(argv []string) int {
	started := time.Now()
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
	result := func(connected bool, value any, code, message string) int {
		var e any
		if code != "" {
			e = object{{"code", code}, {"message", message}}
		}
		emit(object{{"schemaVersion", schemaVersion}, {"adapter", adapter}, {"operation", "counter-increment"},
			{"ok", code == ""}, {"error", e}, {"connected", connected}, {"iin", nil}, {"objects", []any{}},
			{"index", index}, {"value", value}, {"elapsedMs", time.Since(started).Milliseconds()}})
		switch code {
		case "":
			return exitOK
		case "connect-failed":
			return exitConnectFailed
		}
		return exitFailed
	}
	wait := time.Duration(timeout) * time.Millisecond
	c, err := net.DialTimeout("unix", a.str("control-socket", defaultControlSocket), wait)
	if err != nil {
		return result(false, nil, "connect-failed", err.Error())
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(wait))
	out, _ := json.Marshal(controlMessage{Index: index, By: by})
	var answer controlAnswer
	_, err = c.Write(append(out, '\n'))
	if err == nil {
		var line []byte
		if line, err = bufio.NewReader(c).ReadBytes('\n'); err == nil {
			err = json.Unmarshal(line, &answer)
		}
	}
	if err == nil && answer.Value == nil {
		err = errors.New(answer.Error)
	}
	if err != nil {
		return result(true, nil, "failed", err.Error())
	}
	return result(true, *answer.Value, "", "")
}
