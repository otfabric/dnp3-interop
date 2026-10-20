// SPDX-License-Identifier: GPL-3.0-or-later

// The master: one bounded operation per process, one result document.

package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dscsystems/go-dnp3"
	"github.com/dscsystems/go-dnp3/master"
)

var operations = []string{"integrity-poll", "event-poll", "read", "operate", "time-sync", "restart",
	"enable-unsolicited", "disable-unsolicited", "assign-class", "freeze", "monitor", "operate-multi", "deadband-write"}

// timed lists the variations with a time stamp; objects of other variations
// carry no "time".
var timed = map[[2]uint8]bool{
	{2, 2}: true, {2, 3}: true, {4, 2}: true, {4, 3}: true, {11, 2}: true, {21, 5}: true, {21, 6}: true,
	{22, 5}: true, {22, 6}: true, {23, 5}: true, {23, 6}: true, {32, 3}: true, {32, 4}: true, {32, 7}: true,
	{32, 8}: true, {42, 3}: true, {42, 4}: true, {42, 7}: true, {42, 8}: true,
	{13, 2}: true, {33, 3}: true, {33, 4}: true, {33, 7}: true, {33, 8}: true,
	{43, 3}: true, {43, 4}: true, {43, 7}: true, {43, 8}: true,
}

// handler collects what the library delivers.
type handler struct {
	master.NopHandler
	mu          sync.Mutex
	objects     []object
	iin         *uint16 // of the last solicited response
	unsolicited bool
}

func (h *handler) BeginFragment(info master.ResponseInfo) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.unsolicited = info.Unsolicited
	if !info.Unsolicited {
		v := uint16(info.IIN)
		h.iin = &v
	}
}

func push[T any](h *handler, info master.HeaderInfo, values []dnp3.Indexed[T], value func(T) any,
	flags func(T) dnp3.Flags, stamp func(T) dnp3.Timestamp) {
	g, v := info.GV.Group, info.GV.Variation
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, x := range values {
		o := object{{"group", g}, {"variation", v}, {"index", x.Index}, {"value", value(x.Value)},
			{"flags", flagNames(g, uint8(flags(x.Value)))}}
		if t := stamp(x.Value); timed[[2]uint8{g, v}] && t.Quality != dnp3.TimestampInvalid {
			o = append(o, field{"time", formatTime(t.Time)})
		}
		h.objects = append(h.objects, append(o, field{"unsolicited", h.unsolicited}))
	}
}

func (h *handler) HandleBinary(info master.HeaderInfo, values []dnp3.Indexed[dnp3.Binary]) {
	push(h, info, values, func(x dnp3.Binary) any { return x.Value },
		func(x dnp3.Binary) dnp3.Flags { return x.Flags }, func(x dnp3.Binary) dnp3.Timestamp { return x.Time })
}

func (h *handler) HandleDoubleBit(info master.HeaderInfo, values []dnp3.Indexed[dnp3.DoubleBitBinary]) {
	push(h, info, values, func(x dnp3.DoubleBitBinary) any { return int(x.Value) },
		func(x dnp3.DoubleBitBinary) dnp3.Flags { return x.Flags },
		func(x dnp3.DoubleBitBinary) dnp3.Timestamp { return x.Time })
}

func (h *handler) HandleCounter(info master.HeaderInfo, values []dnp3.Indexed[dnp3.Counter]) {
	push(h, info, values, func(x dnp3.Counter) any { return x.Value },
		func(x dnp3.Counter) dnp3.Flags { return x.Flags }, func(x dnp3.Counter) dnp3.Timestamp { return x.Time })
}

func (h *handler) HandleFrozenCounter(info master.HeaderInfo, values []dnp3.Indexed[dnp3.FrozenCounter]) {
	push(h, info, values, func(x dnp3.FrozenCounter) any { return x.Value },
		func(x dnp3.FrozenCounter) dnp3.Flags { return x.Flags },
		func(x dnp3.FrozenCounter) dnp3.Timestamp { return x.Time })
}

func (h *handler) HandleAnalog(info master.HeaderInfo, values []dnp3.Indexed[dnp3.Analog]) {
	push(h, info, values, func(x dnp3.Analog) any { return number(x.Value) },
		func(x dnp3.Analog) dnp3.Flags { return x.Flags }, func(x dnp3.Analog) dnp3.Timestamp { return x.Time })
}

// HandleFrozenAnalog receives g31 and g33 (master.FrozenAnalogHandler).
func (h *handler) HandleFrozenAnalog(info master.HeaderInfo, values []dnp3.Indexed[dnp3.Analog]) {
	push(h, info, values, func(x dnp3.Analog) any { return number(x.Value) },
		func(x dnp3.Analog) dnp3.Flags { return x.Flags }, func(x dnp3.Analog) dnp3.Timestamp { return x.Time })
}

// HandleCommandEvent receives g13 and g43 (master.CommandEventHandler): the
// commanded state or value and the status, with no flags.
func (h *handler) HandleCommandEvent(info master.HeaderInfo, values []dnp3.Indexed[dnp3.CommandEvent]) {
	g, v := info.GV.Group, info.GV.Variation
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, x := range values {
		var value any = x.Value.State
		if x.Value.Analog {
			value = number(x.Value.Value)
		}
		o := object{{"group", g}, {"variation", v}, {"index", x.Index}, {"value", value},
			{"status", commandStatusName(uint8(x.Value.Status))}}
		if t := x.Value.Time; timed[[2]uint8{g, v}] && t.Quality != dnp3.TimestampInvalid {
			o = append(o, field{"time", formatTime(t.Time)})
		}
		h.objects = append(h.objects, append(o, field{"unsolicited", h.unsolicited}))
	}
}

func (h *handler) HandleBinaryOutputStatus(info master.HeaderInfo, values []dnp3.Indexed[dnp3.BinaryOutputStatus]) {
	push(h, info, values, func(x dnp3.BinaryOutputStatus) any { return x.Value },
		func(x dnp3.BinaryOutputStatus) dnp3.Flags { return x.Flags },
		func(x dnp3.BinaryOutputStatus) dnp3.Timestamp { return x.Time })
}

func (h *handler) HandleAnalogOutputStatus(info master.HeaderInfo, values []dnp3.Indexed[dnp3.AnalogOutputStatus]) {
	push(h, info, values, func(x dnp3.AnalogOutputStatus) any { return number(x.Value) },
		func(x dnp3.AnalogOutputStatus) dnp3.Flags { return x.Flags },
		func(x dnp3.AnalogOutputStatus) dnp3.Timestamp { return x.Time })
}

func (h *handler) HandleOctetString(info master.HeaderInfo, values []dnp3.Indexed[dnp3.OctetString]) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, x := range values {
		h.objects = append(h.objects, object{{"group", info.GV.Group}, {"variation", len(x.Value)},
			{"index", x.Index}, {"value", fmt.Sprintf("%x", []byte(x.Value))}, {"unsolicited", h.unsolicited}})
	}
}

type failure struct{ code, message string }

func classMask(classes []int) dnp3.Class {
	var m dnp3.Class
	for _, c := range classes {
		m |= []dnp3.Class{dnp3.Class0, dnp3.Class1, dnp3.Class2, dnp3.Class3}[c]
	}
	return m
}

// request is a parsed operation: what it does once connected.
type request func(ctx context.Context, s *master.Session, r *result) error

type result struct {
	commandStatus *uint8
	// operate-multi: one status per control, in the order sent.
	commandStatuses []uint8
}

func parseOperation(op string, a args) (request, error) {
	switch op {
	case "integrity-poll":
		return func(ctx context.Context, s *master.Session, _ *result) error { return s.IntegrityPoll(ctx) }, nil
	case "event-poll":
		c, err := a.classes("classes", "1,2,3", 1)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, s *master.Session, _ *result) error {
			return s.ScanClasses(ctx, classMask(c))
		}, nil
	case "read":
		g, err := a.requiredInteger("group", 0, 255)
		if err != nil {
			return nil, err
		}
		v, err := a.requiredInteger("variation", 0, 255)
		if err != nil {
			return nil, err
		}
		if a.has("start") != a.has("stop") {
			return nil, usagef("--start and --stop go together")
		}
		if !a.has("start") {
			// A class read is the one all-objects read the library can send.
			if g == 60 && v >= 1 && v <= 4 {
				return func(ctx context.Context, s *master.Session, _ *result) error {
					return s.ScanClasses(ctx, classMask([]int{int(v - 1)}))
				}, nil
			}
			return nil, usagef("read without --start/--stop: the library's master cannot read all objects of a group")
		}
		start, err := a.integer("start", 0, 0, 65535)
		if err != nil {
			return nil, err
		}
		stop, err := a.integer("stop", 0, 0, 65535)
		if err != nil {
			return nil, err
		}
		if start > stop {
			return nil, usagef("--start is larger than --stop")
		}
		return func(ctx context.Context, s *master.Session, _ *result) error {
			return s.ScanRange(ctx, uint8(g), uint8(v), uint16(start), uint16(stop))
		}, nil
	case "operate":
		return parseOperate(a)
	case "operate-multi":
		return parseOperateMulti(a)
	case "deadband-write":
		deadbands, err := parseDeadbands(a)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, s *master.Session, _ *result) error { return s.WriteDeadband(ctx, deadbands) }, nil
	case "time-sync":
		switch a.str("procedure", "lan") {
		case "lan":
			return func(ctx context.Context, s *master.Session, _ *result) error { return s.SyncTimeRecorded(ctx) }, nil
		case "non-lan":
			return func(ctx context.Context, s *master.Session, _ *result) error { return s.SyncTimeWithDelay(ctx) }, nil
		}
		return nil, usagef("--procedure must be lan or non-lan")
	case "restart":
		mode := dnp3.ColdRestart
		switch a.str("type", "cold") {
		case "cold":
		case "warm":
			mode = dnp3.WarmRestart
		default:
			return nil, usagef("--type must be cold or warm")
		}
		return func(ctx context.Context, s *master.Session, _ *result) error { return s.Restart(ctx, mode) }, nil
	case "enable-unsolicited", "disable-unsolicited":
		c, err := a.classes("classes", "1,2,3", 1)
		if err != nil {
			return nil, err
		}
		if op == "enable-unsolicited" {
			return func(ctx context.Context, s *master.Session, _ *result) error {
				return s.EnableUnsolicited(ctx, classMask(c))
			}, nil
		}
		return func(ctx context.Context, s *master.Session, _ *result) error {
			return s.DisableUnsolicited(ctx, classMask(c))
		}, nil
	case "assign-class":
		return nil, usagef("assign-class: the library's master cannot send ASSIGN CLASS")
	case "freeze":
		if a.has("no-ack") {
			return nil, usagef("--no-ack: the library's master cannot send a freeze without response")
		}
		if group, err := a.integer("group", 20, 20, 30); err != nil || group != 20 {
			return nil, usagef("--group: the library's master freezes counters (group 20) only")
		}
		clear := a.has("clear")
		if a.has("at") {
			// FREEZE AT TIME (contract 1.3).
			if clear {
				return nil, usagef("--clear does not go with --at")
			}
			at, err := freezeTime(a.str("at", ""))
			if err != nil {
				return nil, err
			}
			interval, err := a.integer("interval-ms", 0, 0, 4294967295)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, s *master.Session, _ *result) error {
				return s.FreezeAtTime(ctx, at, time.Duration(interval)*time.Millisecond)
			}, nil
		}
		if a.has("interval-ms") {
			return nil, usagef("--interval-ms needs --at")
		}
		return func(ctx context.Context, s *master.Session, _ *result) error { return s.FreezeCounters(ctx, clear) }, nil
	}
	return nil, usagef("unknown operation '%s'", op)
}

func parseOperate(a args) (request, error) {
	kind, err := a.required("type")
	if err != nil {
		return nil, err
	}
	index, err := a.requiredInteger("index", 0, 65535)
	if err != nil {
		return nil, err
	}
	idx := uint16(index)
	mode := a.str("mode", "direct")
	if mode != "direct" && mode != "sbo" && mode != "direct-no-ack" {
		return nil, usagef("--mode must be direct, sbo or direct-no-ack")
	}
	var cmd master.Command
	if kind == "g12v1" {
		if a.has("value") {
			return nil, usagef("--value is for g41")
		}
		codeName, err := a.required("code")
		if err != nil {
			return nil, err
		}
		codes := map[string]dnp3.ControlCode{"LATCH_ON": 0x03, "LATCH_OFF": 0x04, "PULSE_ON": 0x01,
			"PULSE_OFF": 0x02, "CLOSE": 0x41, "TRIP": 0x81}
		code, ok := codes[codeName]
		if !ok {
			return nil, usagef("--code must be LATCH_ON, LATCH_OFF, PULSE_ON, PULSE_OFF, CLOSE or TRIP")
		}
		count, err := a.integer("count", 1, 0, 255)
		if err != nil {
			return nil, err
		}
		on, err := a.integer("on-ms", 100, 0, math.MaxUint32)
		if err != nil {
			return nil, err
		}
		off, err := a.integer("off-ms", 100, 0, math.MaxUint32)
		if err != nil {
			return nil, err
		}
		cmd = master.CROB(idx, dnp3.ControlRelayOutputBlock{Code: code, Count: uint8(count), OnTime: uint32(on), OffTime: uint32(off)})
	} else {
		for _, f := range []string{"code", "count", "on-ms", "off-ms"} {
			if a.has(f) {
				return nil, usagef("--code, --count, --on-ms and --off-ms are for g12v1")
			}
		}
		value, err := a.number("value")
		if err != nil {
			return nil, err
		}
		integral := func(lo, hi float64) error {
			if value != math.Floor(value) || value < lo || value > hi {
				return usagef("--value is out of the range of %s", kind)
			}
			return nil
		}
		switch kind {
		case "g41v1":
			if err := integral(math.MinInt32, math.MaxInt32); err != nil {
				return nil, err
			}
			cmd = master.AnalogOutputInt32(idx, int32(value))
		case "g41v2":
			if err := integral(math.MinInt16, math.MaxInt16); err != nil {
				return nil, err
			}
			cmd = master.AnalogOutputInt16(idx, int16(value))
		case "g41v3":
			if math.Abs(value) > math.MaxFloat32 {
				return nil, usagef("--value is out of the range of g41v3")
			}
			cmd = master.AnalogOutputFloat32(idx, float32(value))
		case "g41v4":
			cmd = master.AnalogOutputFloat64(idx, value)
		default:
			return nil, usagef("--type must be g12v1, g41v1, g41v2, g41v3 or g41v4")
		}
	}
	return func(ctx context.Context, s *master.Session, r *result) error {
		var res master.CommandResult
		var err error
		switch mode {
		case "direct-no-ack":
			return s.DirectOperateNoReply(ctx, cmd)
		case "sbo":
			res, err = s.SelectAndOperate(ctx, cmd)
		default:
			res, err = s.DirectOperate(ctx, cmd)
		}
		if len(res.Statuses) > 0 {
			st := uint8(res.Statuses[0])
			r.commandStatus = &st
		}
		return err
	}, nil
}

func runClient(argv []string) int {
	if len(argv) == 0 {
		logf("client: missing operation")
		return exitUsage
	}
	op := argv[0]
	if !slices.Contains(operations, op) && !slices.Contains(fileOperations, op) && !slices.Contains(attributeOperations, op) {
		logf("client: unknown operation '%s'", op)
		return exitUsage
	}
	flags := []string{"host", "port", "local-addr", "remote-addr", "connect-timeout-ms", "timeout-ms", "collect-ms", "keep-alive-ms"}
	flags = append(append(flags, clientTransportFlags...), saFlags...)
	switches := append([]string(nil), clientTransportSwitches...)
	switch op {
	case "file-get", "file-ls", "file-info", "file-delete":
		flags = append(flags, "name", "file-user", "file-password")
	case "file-put":
		flags = append(flags, "name", "content", "file-user", "file-password")
	case "file-authenticate":
		flags = append(flags, "file-user", "file-password")
	case "attribute-read":
		flags = append(flags, "set", "variation")
	case "attribute-write":
		flags = append(flags, "set", "variation", "text", "uint", "int")
	case "event-poll", "enable-unsolicited", "disable-unsolicited":
		flags = append(flags, "classes")
	case "read":
		flags = append(flags, "group", "variation", "start", "stop")
	case "operate":
		flags = append(flags, "type", "index", "code", "count", "on-ms", "off-ms", "value", "mode")
	case "operate-multi":
		flags = append(flags, "controls", "mode")
	case "deadband-write":
		flags = append(flags, "deadbands")
	case "time-sync":
		flags = append(flags, "procedure")
	case "restart":
		flags = append(flags, "type")
	case "assign-class":
		flags = append(flags, "class", "group", "start", "stop")
	case "freeze":
		switches = append(switches, "clear", "no-ack")
		flags = append(flags, "group", "at", "interval-ms")
	case "monitor":
		flags = append(flags, "duration-ms")
	}

	// Everything is parsed before anything is sent.
	var (
		host                                                            string
		port, local, remote, connectTimeout, timeout, collect, duration int64
		keepAlive                                                       int64
		req                                                             request
		freq                                                            fileRequest
		areq                                                            attributeRequest
		tr                                                              transport
		sec                                                             security
		credentials                                                     *master.FileCredentials
	)
	a, err := parseArgs(argv[1:], flags, switches)
	if err == nil {
		tr, err = parseTransport(a, false)
	}
	if err == nil {
		sec, err = parseSecurity(a)
	}
	switch {
	case err != nil:
	case tr.listen:
		host = a.str("bind-address", "0.0.0.0")
	case tr.kind != "serial":
		host, err = a.required("host")
	}
	for _, f := range []struct {
		dst           *int64
		name          string
		def, min, max int64
	}{
		{&port, "port", 20000, 1, 65535}, {&local, "local-addr", 1, 0, 65519}, {&remote, "remote-addr", 1024, 0, 65519},
		{&connectTimeout, "connect-timeout-ms", 5000, 1, 3600000}, {&timeout, "timeout-ms", 5000, 1, 3600000},
		{&collect, "collect-ms", 0, 0, 3600000}, {&duration, "duration-ms", 1000, 0, 3600000},
		{&keepAlive, "keep-alive-ms", 0, 0, 3600000},
	} {
		if err == nil {
			*f.dst, err = a.integer(f.name, f.def, f.min, f.max)
		}
	}
	if err == nil {
		credentials, err = parseFileCredentials(op, a)
	}
	fileOp := slices.Contains(fileOperations, op)
	attributeOp := slices.Contains(attributeOperations, op)
	switch {
	case err != nil, op == "monitor":
	case fileOp:
		freq, err = parseFileOperation(op, a)
	case attributeOp:
		areq, err = parseAttributeOperation(op, a)
	default:
		req, err = parseOperation(op, a)
	}
	if err != nil {
		logf("client %s: %v", op, err)
		return exitUsage
	}

	started := time.Now()
	h := &handler{}
	session := master.New(master.Config{
		LocalAddr:       uint16(local),
		RemoteAddr:      uint16(remote),
		ResponseTimeout: time.Duration(timeout) * time.Millisecond,
		Log:             libraryLog(),

		SecureAuthentication: sec.master(),
		FileCredentials:      credentials,
		UseLinkConfirms:      a.has("link-confirms"),
		KeepAlive:            time.Duration(keepAlive) * time.Millisecond,
	}, h)
	inner, addr, err := clientChannel(tr, host, port)
	if err != nil {
		// Certificate files that cannot be used.
		emit(object{{"schemaVersion", schemaVersion}, {"adapter", adapter}, {"operation", op}, {"ok", false},
			{"error", object{{"code", "failed"}, {"message", err.Error()}}}, {"connected", false}, {"iin", nil},
			{"objects", []object{}}, {"elapsedMs", time.Since(started).Milliseconds()}})
		return exitFailed
	}
	ch := statisticsChannel{Channel: inner, h: h}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		_ = session.Run(ctx, ch)
		close(runDone)
	}()

	connected := false
	for deadline := time.Now().Add(time.Duration(connectTimeout) * time.Millisecond); time.Now().Before(deadline); {
		if session.Connected() {
			connected = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	var fail *failure
	var res result
	var fres fileResult
	var attributes []object
	switch {
	case !connected:
		fail = &failure{"connect-failed", "no connection to " + addr}
	case op == "monitor":
		end := time.Now().Add(time.Duration(duration) * time.Millisecond)
		for time.Now().Before(end) && session.Connected() {
			time.Sleep(10 * time.Millisecond)
		}
		if !session.Connected() {
			fail = &failure{"connection-lost", "the connection closed during the monitoring"}
		}
	case fileOp:
		// A transfer is many requests, each bounded by the response timeout.
		opCtx, opCancel := context.WithTimeout(ctx, 20*time.Duration(timeout)*time.Millisecond+2*time.Second)
		err := freq(opCtx, session, &fres)
		opCancel()
		fail = classify(err, session.Connected())
		if status, ok := fileStatusOf(err); ok {
			name := fileStatusName(status)
			fres.status = &name
			fail = &failure{"file-error", err.Error()}
		} else if err == nil {
			name := "SUCCESS"
			fres.status = &name
		}
	case attributeOp:
		opCtx, opCancel := context.WithTimeout(ctx, 3*time.Duration(timeout)*time.Millisecond+2*time.Second)
		err := areq(opCtx, session, &attributes)
		opCancel()
		fail = classify(err, session.Connected())
	default:
		// The library bounds every request by the response timeout; this only
		// guards against a task that never reports.
		opCtx, opCancel := context.WithTimeout(ctx, 3*time.Duration(timeout)*time.Millisecond+2*time.Second)
		err := req(opCtx, session, &res)
		opCancel()
		fail = classify(err, session.Connected())
	}

	// A response with a request error is a refusal, whatever the library made
	// of it; the IIN of a command response is only available as the last one.
	h.mu.Lock()
	iin := h.iin
	h.mu.Unlock()
	reads := op == "integrity-poll" || op == "event-poll" || op == "read"
	if connected && op != "monitor" && (fail == nil || fail.code == "command-failed") {
		last := uint16(session.LastIIN())
		check := last
		if reads && iin != nil {
			check = *iin
		}
		if check&0x0700 != 0 {
			fail = &failure{"rejected", "the outstation refused the request (IIN2)"}
			res.commandStatus = nil
			res.commandStatuses = nil
		}
	}
	if fail != nil && fail.code == "command-failed" && res.commandStatus == nil {
		fail.code = "failed"
	}
	if fail == nil && (op == "operate" || op == "operate-multi") && res.commandStatus != nil && *res.commandStatus != 0 {
		fail = &failure{"command-failed", "the outstation answered " + commandStatusName(*res.commandStatus)}
	}

	if fail == nil && collect > 0 {
		time.Sleep(time.Duration(collect) * time.Millisecond)
	}
	// The confirm of the last response may still be on its way out; closing
	// at once can lose it, and the outstation then keeps events the master
	// has already reported.
	if connected {
		time.Sleep(200 * time.Millisecond)
	}
	cancel()
	select {
	case <-runDone:
	case <-time.After(2 * time.Second):
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	doc := object{{"schemaVersion", schemaVersion}, {"adapter", adapter}, {"operation", op}, {"ok", fail == nil}}
	if fail != nil {
		doc = append(doc, field{"error", object{{"code", fail.code}, {"message", fail.message}}})
	} else {
		doc = append(doc, field{"error", nil})
	}
	doc = append(doc, field{"connected", connected})
	if reads && h.iin != nil {
		doc = append(doc, field{"iin", iinList(*h.iin)})
	} else {
		doc = append(doc, field{"iin", nil})
	}
	objects := h.objects
	if objects == nil {
		objects = []object{}
	}
	doc = append(doc, field{"objects", objects})
	if op == "operate" {
		if res.commandStatus != nil {
			doc = append(doc, field{"commandStatus", commandStatusName(*res.commandStatus)})
		} else {
			doc = append(doc, field{"commandStatus", nil})
		}
	}
	if op == "operate-multi" {
		if res.commandStatus != nil {
			doc = append(doc, field{"commandStatus", commandStatusName(*res.commandStatus)})
		} else {
			doc = append(doc, field{"commandStatus", nil})
		}
		if res.commandStatuses != nil {
			names := []string{}
			for _, st := range res.commandStatuses {
				names = append(names, commandStatusName(st))
			}
			doc = append(doc, field{"commandStatuses", names})
		} else {
			doc = append(doc, field{"commandStatuses", nil})
		}
	}
	if op == "restart" {
		// The library does not hand the announced delay to the caller.
		doc = append(doc, field{"restartDelayMs", nil})
	}
	if op == "attribute-read" {
		if attributes == nil {
			attributes = []object{}
		}
		doc = append(doc, field{"attributes", attributes})
	}
	if fileOp {
		switch {
		case fres.files != nil:
			doc = append(doc, field{"files", fres.files})
		case fres.file != nil:
			doc = append(doc, field{"file", fres.file})
		}
		if fres.status != nil {
			doc = append(doc, field{"fileStatus", *fres.status})
		} else {
			doc = append(doc, field{"fileStatus", nil})
		}
	}
	doc = append(doc, field{"elapsedMs", time.Since(started).Milliseconds()})
	emit(doc)

	switch {
	case fail == nil:
		return exitOK
	case fail.code == "connect-failed":
		return exitConnectFailed
	}
	return exitFailed
}

// classify maps an error of the library to the contract's error codes.
func classify(err error, connected bool) *failure {
	switch {
	case err == nil:
		return nil
	case authenticationFailed(err):
		return &failure{"auth-failed", err.Error()}
	case errors.Is(err, dnp3.ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		return &failure{"timeout", err.Error()}
	case errors.Is(err, dnp3.ErrRejected), errors.Is(err, dnp3.ErrNotSupported):
		return &failure{"rejected", err.Error()}
	case errors.Is(err, dnp3.ErrNoConnection), errors.Is(err, dnp3.ErrClosed), !connected:
		return &failure{"connection-lost", err.Error()}
	}
	// CommandResult.Err: a status other than SUCCESS; the caller decides
	// between rejected and command-failed from the IIN.
	return &failure{"command-failed", err.Error()}
}

// freezeTime reads --at of freeze: "immediately" (time 0), "+MS" (from now)
// or milliseconds since the epoch.
func freezeTime(text string) (time.Time, error) {
	if text == "immediately" {
		return time.UnixMilli(0), nil
	}
	if ms, ok := strings.CutPrefix(text, "+"); ok {
		n, err := strconv.ParseInt(ms, 10, 64)
		if err == nil && n >= 0 {
			return time.Now().Add(time.Duration(n) * time.Millisecond), nil
		}
	} else if n, err := strconv.ParseInt(text, 10, 64); err == nil && n >= 0 && n < 1<<48 {
		return time.UnixMilli(n), nil
	}
	return time.Time{}, usagef("--at must be immediately, +MS or milliseconds since the epoch")
}

// parseDeadbands reads --deadbands of deadband-write: INDEX:VALUE items,
// comma-separated (contract 1.3).
func parseDeadbands(a args) (map[uint16]float32, error) {
	text, err := a.required("deadbands")
	if err != nil {
		return nil, err
	}
	out := map[uint16]float32{}
	for _, item := range strings.Split(text, ",") {
		index, value, ok := strings.Cut(item, ":")
		i, ierr := strconv.ParseUint(index, 10, 16)
		v, verr := strconv.ParseFloat(value, 32)
		if !ok || ierr != nil || verr != nil || v < 0 {
			return nil, usagef("--deadbands items are INDEX:VALUE, VALUE a non-negative number")
		}
		out[uint16(i)] = float32(v)
	}
	return out, nil
}
