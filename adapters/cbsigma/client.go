// SPDX-License-Identifier: MIT

// The master: one bounded operation per process, one result document.
// Every request goes through the library's Master.Request; the adapter
// builds the objects with the library's encoders and reads the responses
// with its decoders.

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	dnp3 "github.com/cB-Sigma/dnp3"
	"github.com/cB-Sigma/iotsec"
)

var operations = []string{"integrity-poll", "event-poll", "read", "operate", "operate-multi", "time-sync", "restart",
	"enable-unsolicited", "disable-unsolicited", "assign-class", "freeze", "monitor", "manage", "deadband-write"}

// master is the association of the operation and what it received.
type master struct {
	m       *dnp3.Master
	timeout time.Duration

	mu      sync.Mutex
	objects []object
	iin     *uint16 // of the last solicited response
}

func (c *master) record(frags []dnp3.Fragment, unsolicited bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, f := range frags {
		if !unsolicited {
			v := uint16(f.IIN)
			c.iin = &v
		}
		c.objects = append(c.objects, objectDocs(f.Objects, unsolicited)...)
	}
}

func (c *master) request(ctx context.Context, fc byte, objs []byte) ([]dnp3.Fragment, error) {
	frags, err := c.m.Request(ctx, fc, objs)
	c.record(frags, false)
	return frags, err
}

func (c *master) lost() bool {
	select {
	case <-c.m.Done():
		return true
	default:
		return false
	}
}

type failure struct{ code, message string }

// result holds the fields of the operations that have them.
type result struct {
	commandStatus   *uint8
	commandStatuses []uint8
	restartDelay    *int64
	partialEcho     bool
}

// request is a parsed operation.
type request func(ctx context.Context, c *master, r *result) error

// startStop is the header of a range or, without one, of all objects.
func startStop(group, variation uint8, a args) ([]byte, error) {
	if a.has("start") != a.has("stop") {
		return nil, usagef("--start and --stop go together")
	}
	if !a.has("start") {
		return dnp3.AppendHeader(nil, group, variation, dnp3.QAll, 0, 0), nil
	}
	start, err := a.uint16Flag("start", 0, 0, 65535)
	if err != nil {
		return nil, err
	}
	stop, err := a.uint16Flag("stop", 0, 0, 65535)
	if err != nil {
		return nil, err
	}
	if start > stop {
		return nil, usagef("--start is larger than --stop")
	}
	q := uint8(dnp3.Q8StartStop)
	if stop > 255 {
		q = dnp3.Q16StartStop
	}
	return dnp3.AppendHeader(nil, group, variation, q, uint32(start), uint32(stop)), nil
}

func send(fc byte, objs []byte) request {
	return func(ctx context.Context, c *master, _ *result) error {
		_, err := c.request(ctx, fc, objs)
		return err
	}
}

func parseOperation(op string, a args) (request, error) {
	switch op {
	case "integrity-poll":
		return send(dnp3.FCRead, dnp3.ClassRequest(1, 2, 3, 0)), nil
	case "event-poll":
		c, err := a.classes("classes", "1,2,3", 1)
		if err != nil {
			return nil, err
		}
		return send(dnp3.FCRead, dnp3.ClassRequest(c...)), nil
	case "read":
		g, err := a.requiredUint8Flag("group", 0, 255)
		if err != nil {
			return nil, err
		}
		v, err := a.requiredUint8Flag("variation", 0, 255)
		if err != nil {
			return nil, err
		}
		objs, err := startStop(g, v, a)
		if err != nil {
			return nil, err
		}
		return send(dnp3.FCRead, objs), nil
	case "operate":
		return parseOperate(a)
	case "operate-multi":
		return parseOperateMulti(a)
	case "time-sync":
		var lan bool
		switch a.str("procedure", "lan") {
		case "lan":
			lan = true
		case "non-lan":
		default:
			return nil, usagef("--procedure must be lan or non-lan")
		}
		return func(ctx context.Context, c *master, _ *result) error { return c.m.SyncTime(ctx, lan) }, nil
	case "restart":
		fc := byte(dnp3.FCColdRestart)
		switch a.str("type", "cold") {
		case "cold":
		case "warm":
			fc = dnp3.FCWarmRestart
		default:
			return nil, usagef("--type must be cold or warm")
		}
		return func(ctx context.Context, c *master, r *result) error {
			frags, err := c.request(ctx, fc, nil)
			if err != nil {
				return err
			}
			for _, f := range frags {
				blocks, _ := dnp3.ParseObjects(f.Objects, true)
				for _, b := range blocks {
					if b.Header.Group != 52 || len(b.Points) == 0 {
						continue
					}
					ms := int64(toFloat(b.Points[0].Value))
					if b.Header.Var == 1 {
						ms *= 1000
					}
					r.restartDelay = &ms
				}
			}
			if r.restartDelay == nil {
				return errors.New("the outstation announced no restart delay (g52)")
			}
			return nil
		}, nil
	case "enable-unsolicited", "disable-unsolicited":
		c, err := a.classes("classes", "1,2,3", 1)
		if err != nil {
			return nil, err
		}
		fc := byte(dnp3.FCEnableUnsolicited)
		if op == "disable-unsolicited" {
			fc = dnp3.FCDisableUnsolicited
		}
		return send(fc, dnp3.ClassRequest(c...)), nil
	case "assign-class":
		class, err := a.requiredInteger("class", 0, 3)
		if err != nil {
			return nil, err
		}
		g, err := a.requiredUint8Flag("group", 1, 255)
		if err != nil {
			return nil, err
		}
		header, err := startStop(g, 0, a)
		if err != nil {
			return nil, err
		}
		return send(dnp3.FCAssignClass, dnp3.AssignClass([]dnp3.ClassAssignment{{Class: int(class), Headers: [][]byte{header}}})), nil
	case "freeze":
		return parseFreeze(a)
	case "manage":
		return parseManage(a)
	case "deadband-write":
		return parseDeadbands(a)
	}
	return nil, usagef("unknown operation '%s'", op)
}

func parseFreeze(a args) (request, error) {
	group, err := a.integer("group", 20, 20, 30)
	if err != nil || (group != 20 && group != 30) {
		return nil, usagef("--group must be 20 or 30")
	}
	noAck, clear := a.has("no-ack"), a.has("clear")
	objs := dnp3.AppendHeader(nil, uint8(group), 0, dnp3.QAll, 0, 0)
	fc := byte(dnp3.FCImmedFreeze)
	if clear {
		fc = dnp3.FCFreezeClear
	}
	if a.has("at") {
		if clear {
			return nil, usagef("--clear does not go with --at")
		}
		at, err := freezeTime(a.str("at", ""))
		if err != nil {
			return nil, err
		}
		interval, err := a.uint32Flag("interval-ms", 0, 0, 4294967295)
		if err != nil {
			return nil, err
		}
		schedule, err := dnp3.EncodeBlock(50, 2, dnp3.Q8Count, []dnp3.Point{{Value: at, Interval: interval}})
		if err != nil {
			return nil, err
		}
		objs = append(schedule, objs...)
		fc = dnp3.FCFreezeAtTime
	} else if a.has("interval-ms") {
		return nil, usagef("--interval-ms needs --at")
	}
	if noAck {
		fc++ // the _NR function code follows each one (table 4-2)
	}
	return send(fc, objs), nil
}

// freezeTime reads --at: "immediately" (time 0), "+MS" (from now) or
// milliseconds since the epoch.
func freezeTime(text string) (time.Time, error) {
	if text == "immediately" {
		return time.UnixMilli(0), nil
	}
	if ms, ok := strings.CutPrefix(text, "+"); ok {
		if n, err := strconv.ParseInt(ms, 10, 64); err == nil && n >= 0 {
			return time.Now().Add(time.Duration(n) * time.Millisecond), nil
		}
	} else if n, err := strconv.ParseInt(text, 10, 64); err == nil && n >= 0 && n < 1<<48 {
		return time.UnixMilli(n), nil
	}
	return time.Time{}, usagef("--at must be immediately, +MS or milliseconds since the epoch")
}

func parseManage(a args) (request, error) {
	f, err := a.required("function")
	if err != nil {
		return nil, err
	}
	if a.has("spec") && f != "activate-config" {
		return nil, usagef("--spec goes with --function activate-config")
	}
	all := dnp3.AppendHeader(nil, 90, 1, dnp3.QAll, 0, 0)
	switch f {
	case "initialize-data":
		return send(dnp3.FCInitializeData, nil), nil
	case "initialize-application":
		return send(dnp3.FCInitializeAppl, all), nil
	case "start-application":
		return send(dnp3.FCStartAppl, all), nil
	case "stop-application":
		return send(dnp3.FCStopAppl, all), nil
	case "save-configuration":
		return send(dnp3.FCSaveConfig, nil), nil
	case "activate-config":
		spec := a.str("spec", "config.xml")
		if len(spec) < 1 || len(spec) > 255 {
			return nil, usagef("--spec must be 1..255 octets")
		}
		objs, err := dnp3.EncodeBlock(70, 8, dnp3.QFreeFormat, []dnp3.Point{{Raw: []byte(spec)}})
		if err != nil {
			return nil, err
		}
		return send(dnp3.FCActivateConfig, objs), nil
	}
	return nil, usagef("--function must be initialize-data, initialize-application, start-application, stop-application, save-configuration or activate-config")
}

// parseDeadbands reads --deadbands INDEX:VALUE,... and writes them as
// g34v3 with a one-octet index prefix, two octets when an index needs them.
func parseDeadbands(a args) (request, error) {
	text, err := a.required("deadbands")
	if err != nil {
		return nil, err
	}
	var pts []dnp3.Point
	q := uint8(dnp3.Q8Index8)
	for _, item := range strings.Split(text, ",") {
		index, value, ok := strings.Cut(item, ":")
		i, ierr := strconv.ParseUint(index, 10, 16)
		v, verr := strconv.ParseFloat(value, 64)
		if !ok || ierr != nil || verr != nil || v < 0 || v > math.MaxFloat32 {
			return nil, usagef("--deadbands items are INDEX:VALUE, VALUE a non-negative number")
		}
		if i > 255 {
			q = dnp3.Q16Index16
		}
		pts = append(pts, dnp3.Point{Index: uint32(i), Value: v})
	}
	objs, err := dnp3.EncodeBlock(34, 3, q, pts)
	if err != nil {
		return nil, err
	}
	return send(dnp3.FCWrite, objs), nil
}

// ---- controls ----

var crobCodes = map[string]uint8{"LATCH_ON": 0x03, "LATCH_OFF": 0x04, "PULSE_ON": 0x01, "PULSE_OFF": 0x02,
	"CLOSE": 0x41, "TRIP": 0x81}

// control is one control object: its header and object, encoded.
type control struct {
	group, variation uint8
	point            dnp3.Point
}

func analogControl(kind string, index uint16, value float64) (control, error) {
	integral := func(lo, hi float64) error {
		if value != math.Floor(value) || value < lo || value > hi {
			return usagef("--value is out of the range of %s", kind)
		}
		return nil
	}
	var v uint8
	switch kind {
	case "g41v1":
		v = 1
		if err := integral(math.MinInt32, math.MaxInt32); err != nil {
			return control{}, err
		}
	case "g41v2":
		v = 2
		if err := integral(math.MinInt16, math.MaxInt16); err != nil {
			return control{}, err
		}
	case "g41v3":
		v = 3
		if math.Abs(value) > math.MaxFloat32 {
			return control{}, usagef("--value is out of the range of g41v3")
		}
	case "g41v4":
		v = 4
	default:
		return control{}, usagef("--type must be g12v1, g41v1, g41v2, g41v3 or g41v4")
	}
	return control{group: 41, variation: v, point: dnp3.Point{Index: uint32(index), Value: value}}, nil
}

func (c control) encode() ([]byte, error) {
	return dnp3.EncodeBlock(c.group, c.variation, dnp3.Q16Index16, []dnp3.Point{c.point})
}

func encodeControls(cs []control) ([]byte, error) {
	var out []byte
	for _, c := range cs {
		b, err := c.encode()
		if err != nil {
			return nil, err
		}
		out = append(out, b...)
	}
	return out, nil
}

// echoed are the statuses of the control objects of a response.
func echoed(frags []dnp3.Fragment) []uint8 {
	var out []uint8
	for _, f := range frags {
		blocks, _ := dnp3.ParseObjects(f.Objects, true)
		for _, b := range blocks {
			if b.Header.Group != 12 && b.Header.Group != 41 {
				continue
			}
			for _, p := range b.Points {
				out = append(out, p.Status)
			}
		}
	}
	return out
}

func parseOperate(a args) (request, error) {
	kind, err := a.required("type")
	if err != nil {
		return nil, err
	}
	index, err := a.requiredUint16Flag("index", 0, 65535)
	if err != nil {
		return nil, err
	}
	mode := a.str("mode", "direct")
	if !slices.Contains([]string{"direct", "sbo", "direct-no-ack", "operate"}, mode) {
		return nil, usagef("--mode must be direct, sbo, direct-no-ack or operate")
	}
	delay, err := a.integer("operate-delay-ms", 0, 0, 3600000)
	if err != nil {
		return nil, err
	}
	if a.has("operate-delay-ms") && mode != "sbo" {
		return nil, usagef("--operate-delay-ms needs --mode sbo")
	}
	var ctl control
	if kind == "g12v1" {
		if a.has("value") {
			return nil, usagef("--value is for g41")
		}
		name, err := a.required("code")
		if err != nil {
			return nil, err
		}
		code, ok := crobCodes[name]
		if !ok {
			return nil, usagef("--code must be LATCH_ON, LATCH_OFF, PULSE_ON, PULSE_OFF, CLOSE or TRIP")
		}
		count, err := a.uint8Flag("count", 1, 0, 255)
		if err != nil {
			return nil, err
		}
		on, err := a.uint32Flag("on-ms", 100, 0, math.MaxUint32)
		if err != nil {
			return nil, err
		}
		off, err := a.uint32Flag("off-ms", 100, 0, math.MaxUint32)
		if err != nil {
			return nil, err
		}
		ctl = control{group: 12, variation: 1, point: dnp3.Point{Index: uint32(index),
			CROB: dnp3.CROB{Code: code, Count: count, OnTime: on, OffTime: off}}}
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
		if ctl, err = analogControl(kind, index, value); err != nil {
			return nil, err
		}
	}
	objs, err := ctl.encode()
	if err != nil {
		return nil, usagef("%v", err)
	}
	return func(ctx context.Context, c *master, r *result) error {
		statuses, err := operateObjects(ctx, c, mode, objs, time.Duration(delay)*time.Millisecond)
		if len(statuses) > 0 {
			r.commandStatus = &statuses[0]
		}
		return err
	}, nil
}

// operateObjects sends the controls in a mode and returns the statuses of
// the last echo.
func operateObjects(ctx context.Context, c *master, mode string, objs []byte, delay time.Duration) ([]uint8, error) {
	switch mode {
	case "direct-no-ack":
		_, err := c.request(ctx, dnp3.FCDirectOperateNR, objs)
		return nil, err
	case "operate":
		frags, err := c.request(ctx, dnp3.FCOperate, objs)
		return echoed(frags), err
	case "sbo":
		frags, err := c.request(ctx, dnp3.FCSelect, objs)
		statuses := echoed(frags)
		if err != nil || len(statuses) == 0 || slices.ContainsFunc(statuses, func(s uint8) bool { return s != 0 }) {
			return statuses, err
		}
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		frags, err = c.request(ctx, dnp3.FCOperate, objs)
		return echoed(frags), err
	}
	frags, err := c.request(ctx, dnp3.FCDirectOperate, objs)
	return echoed(frags), err
}

func parseOperateMulti(a args) (request, error) {
	text, err := a.required("controls")
	if err != nil {
		return nil, err
	}
	mode := a.str("mode", "direct")
	if mode != "direct" && mode != "sbo" {
		return nil, usagef("--mode must be direct or sbo")
	}
	var cs []control
	for _, item := range strings.Split(text, ",") {
		parts := strings.Split(item, ":")
		if len(parts) != 3 {
			return nil, usagef("--controls items are g12v1:INDEX:CODE or g41vN:INDEX:VALUE")
		}
		index, err := strconv.ParseUint(parts[1], 10, 16)
		if err != nil {
			return nil, usagef("--controls: bad index '%s'", parts[1])
		}
		if parts[0] == "g12v1" {
			code, ok := crobCodes[parts[2]]
			if !ok {
				return nil, usagef("--controls: bad code '%s'", parts[2])
			}
			cs = append(cs, control{group: 12, variation: 1, point: dnp3.Point{Index: uint32(index),
				CROB: dnp3.CROB{Code: code, Count: 1, OnTime: 100, OffTime: 100}}})
			continue
		}
		value, err := strconv.ParseFloat(parts[2], 64)
		if err != nil {
			return nil, usagef("--controls: bad value '%s'", parts[2])
		}
		ctl, err := analogControl(parts[0], uint16(index), value)
		if err != nil {
			return nil, err
		}
		cs = append(cs, ctl)
	}
	objs, err := encodeControls(cs)
	if err != nil {
		return nil, usagef("%v", err)
	}
	return func(ctx context.Context, c *master, r *result) error {
		statuses, err := operateObjects(ctx, c, mode, objs, 0)
		for _, s := range statuses {
			if s != 0 {
				first := s
				r.commandStatus = &first
				break
			}
		}
		switch {
		case len(statuses) == len(cs):
			r.commandStatuses = statuses
			if r.commandStatus == nil {
				ok := uint8(0)
				r.commandStatus = &ok
			}
		case len(statuses) > 0 && err == nil:
			r.partialEcho = true
		}
		return err
	}, nil
}

// ---- the operation ----

// dialer opens the master's TCP connection: dialling again until the
// connect timeout, or (dual end point) waiting for the outstation.
type dialer struct {
	ctx    context.Context // bounded by --connect-timeout-ms
	ln     net.Listener
	listen *tls.Config // TLS server configuration of a listening master
}

func (d *dialer) DialContext(_ context.Context, network, address string) (net.Conn, error) {
	if d.ln != nil {
		type accepted struct {
			c   net.Conn
			err error
		}
		ch := make(chan accepted, 1)
		go func() {
			c, err := d.ln.Accept()
			ch <- accepted{c, err}
		}()
		select {
		case a := <-ch:
			if a.err != nil || d.listen == nil {
				return a.c, a.err
			}
			tc := tls.Server(a.c, d.listen)
			if err := tc.HandshakeContext(d.ctx); err != nil {
				_ = a.c.Close()
				return nil, err
			}
			return tc, nil
		case <-d.ctx.Done():
			_ = d.ln.Close()
			return nil, fmt.Errorf("no outstation connected to %s", d.ln.Addr())
		}
	}
	var last error
	for {
		var nd net.Dialer
		c, err := nd.DialContext(d.ctx, network, address)
		if err == nil {
			return c, nil
		}
		last = err
		select {
		case <-d.ctx.Done():
			return nil, last
		case <-time.After(100 * time.Millisecond):
		}
	}
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
	flags = append(append(flags, clientTransport...), saFlags...)
	switches := []string{"tls", "listen", "link-confirms"}
	switch op {
	case "file-get", "file-ls", "file-info", "file-delete":
		flags = append(flags, "name", "file-user", "file-password")
	case "file-put":
		flags = append(flags, "name", "content", "file-user", "file-password")
		switches = append(switches, "append")
	case "file-authenticate":
		flags = append(flags, "file-user", "file-password")
	case "attribute-read":
		flags = append(flags, "set", "variation")
	case "attribute-write":
		flags = append(flags, "set", "variation", "text", "uint", "int", "float", "octets", "bits", "time")
	case "event-poll", "enable-unsolicited", "disable-unsolicited":
		flags = append(flags, "classes")
	case "read":
		flags = append(flags, "group", "variation", "start", "stop")
	case "operate":
		flags = append(flags, "type", "index", "code", "count", "on-ms", "off-ms", "value", "mode", "operate-delay-ms")
		switches = append(switches, "broadcast")
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
	case "manage":
		flags = append(flags, "function", "spec")
	}

	// Everything is parsed before anything is sent.
	var (
		host                                                            string
		port, local, remote, connectTimeout, timeout, collect, duration int64
		keepAlive                                                       int64
		localAddr, remoteAddr                                           uint16
		req                                                             request
		freq                                                            fileRequest
		areq                                                            attributeRequest
		tr                                                              transport
		sec                                                             security
	)
	a, err := parseArgs(argv[1:], flags, switches)
	if err == nil {
		tr, err = parseTransport(a, false)
	}
	if err == nil {
		sec, err = parseSecurity(a, false)
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
	if err == nil && a.has("keep-alive-ms") && tr.kind != "tcp" {
		err = usagef("--keep-alive-ms needs --transport tcp: the library sends keep-alives on TCP only")
	}
	if err == nil && a.has("broadcast") {
		if a.str("mode", "direct") != "direct-no-ack" {
			err = usagef("--broadcast needs --mode direct-no-ack")
		}
		remote = dnp3.AddrBroadcastOptional
	}
	if err == nil {
		localAddr, err = usageUint16("local-addr", local)
	}
	if err == nil {
		remoteAddr, err = usageUint16("remote-addr", remote)
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
	c := &master{timeout: time.Duration(timeout) * time.Millisecond}
	var fail *failure
	var res result
	var fres fileResult
	attributes := []object{}
	connected := false

	addr := net.JoinHostPort(host, strconv.Itoa(int(port)))
	cfg := dnp3.MasterConfig{
		MasterAddress:     localAddr,
		OutstationAddress: remoteAddr,
		Timeout:           c.timeout,
		Log:               libraryLog(),
		OnUnsolicited:     func(f dnp3.Fragment) { c.record([]dnp3.Fragment{f}, true) },
		KeepAlive:         time.Duration(keepAlive) * time.Millisecond,
		LinkConfirm:       a.has("link-confirms"),
		LocalPort:         tr.udpLocalPort,
	}
	connectCtx, connectCancel := context.WithTimeout(context.Background(), time.Duration(connectTimeout)*time.Millisecond)
	defer connectCancel()
	d := &dialer{ctx: connectCtx}
	var setupErr error
	switch {
	case tr.kind == "serial":
		cfg.URL = serialURL(tr.line)
		line := tr.line
		dnp3.OpenSerial = func(string, int, string) (io.ReadWriteCloser, error) { return openSerial(line) }
	case tr.kind == "udp":
		cfg.URL = "udp://" + addr
	default:
		cfg.URL = "tcp://" + addr
		cfg.Dialer = d
		if tr.listen {
			if tr.tls {
				d.listen, setupErr = tlsServer(tr)
			}
			if setupErr == nil {
				d.ln, setupErr = net.Listen("tcp", addr)
			}
		} else if tr.tls {
			var tc *tls.Config
			if tc, setupErr = tlsClient(tr, host); setupErr == nil {
				cfg.TLS = iotsec.StdTLS{Config: tc}
			}
		}
	}
	if setupErr == nil {
		cfg.Auth, setupErr = sec.master()
	}
	if setupErr != nil {
		fail = &failure{"failed", setupErr.Error()}
	} else {
		// Serial devices and UDP sockets are opened again until the
		// connect timeout; TCP connections by the dialer.
		var dialErr error
		for {
			c.m, dialErr = dnp3.Dial(connectCtx, cfg)
			if dialErr == nil || tr.kind == "tcp" || connectCtx.Err() != nil {
				break
			}
			select {
			case <-connectCtx.Done():
			case <-time.After(100 * time.Millisecond):
			}
		}
		if dialErr != nil {
			fail = &failure{"connect-failed", fmt.Sprintf("no connection to %s: %v", where(tr, addr), dialErr)}
		} else {
			connected = true
		}
	}
	if d.ln != nil {
		_ = d.ln.Close()
	}

	if connected {
		bound := func(n int64) (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), time.Duration(n)*time.Duration(timeout)*time.Millisecond+2*time.Second)
		}
		if sec.on {
			ctx, cancel := bound(4)
			if err := c.m.ChangeSessionKeys(ctx); err != nil {
				fail = classify(err, c)
				if fail.code != "connection-lost" && fail.code != "timeout" {
					fail.code = "auth-failed"
				}
			}
			cancel()
		}
		switch {
		case fail != nil:
		case op == "monitor":
			select {
			case <-time.After(time.Duration(duration) * time.Millisecond):
			case <-c.m.Done():
				fail = &failure{"connection-lost", "the connection closed during the monitoring"}
			}
		case fileOp:
			// A transfer is many requests, each bounded by the timeout.
			ctx, cancel := bound(40)
			err := c.runFile(ctx, freq, &fres)
			cancel()
			var fe fileError
			if errors.As(err, &fe) {
				fail = &failure{"file-error", err.Error()}
			} else {
				fail = classify(err, c)
			}
		case attributeOp:
			ctx, cancel := bound(3)
			fail = classify(c.runAttribute(ctx, areq, &attributes), c)
			cancel()
		default:
			ctx, cancel := bound(4)
			fail = classify(req(ctx, c, &res), c)
			cancel()
		}
		if fail != nil && fail.code == "rejected" {
			res.commandStatus, res.commandStatuses = nil, nil
		}
		if fail == nil && res.partialEcho {
			fail = &failure{"failed", "the outstation echoed only some of the controls"}
			if res.commandStatus != nil {
				fail.code = "command-failed"
			}
			res.commandStatuses = nil
		}
		if fail == nil && (op == "operate" || op == "operate-multi") && res.commandStatus != nil && *res.commandStatus != 0 {
			fail = &failure{"command-failed", "the outstation answered " + commandStatusName(*res.commandStatus)}
		}
		if fail == nil && collect > 0 {
			time.Sleep(time.Duration(collect) * time.Millisecond)
		}
		// A master clears DEVICE_RESTART (IIN1.7) by writing g80v1 index 7
		// to 0, as the library's own session does at its start; not after a
		// restart, whose outstation is going down.
		if op != "restart" && remote != dnp3.AddrBroadcastOptional && !c.lost() && c.m.IIN()&dnp3.IINDeviceRestart != 0 {
			if objs, err := dnp3.EncodeBlock(80, 1, dnp3.Q8StartStop, []dnp3.Point{{Index: 7, Value: false}}); err == nil {
				ctx, cancel := bound(2)
				_, _ = c.m.Request(ctx, dnp3.FCWrite, objs)
				cancel()
			}
		}
		// The confirm of the last response may still be on its way out.
		time.Sleep(100 * time.Millisecond)
		_ = c.m.Close()
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	doc := object{{"schemaVersion", schemaVersion}, {"adapter", adapter}, {"operation", op}, {"ok", fail == nil}}
	if fail != nil {
		doc = append(doc, field{"error", object{{"code", fail.code}, {"message", fail.message}}})
	} else {
		doc = append(doc, field{"error", nil})
	}
	doc = append(doc, field{"connected", connected})
	reads := op == "integrity-poll" || op == "event-poll" || op == "read"
	if reads && c.iin != nil {
		doc = append(doc, field{"iin", iinList(*c.iin)})
	} else {
		doc = append(doc, field{"iin", nil})
	}
	objects := c.objects
	if objects == nil {
		objects = []object{}
	}
	doc = append(doc, field{"objects", objects})
	if op == "operate" || op == "operate-multi" {
		if res.commandStatus != nil {
			doc = append(doc, field{"commandStatus", commandStatusName(*res.commandStatus)})
		} else {
			doc = append(doc, field{"commandStatus", nil})
		}
	}
	if op == "operate-multi" {
		if res.commandStatuses != nil {
			names := []string{}
			for _, s := range res.commandStatuses {
				names = append(names, commandStatusName(s))
			}
			doc = append(doc, field{"commandStatuses", names})
		} else {
			doc = append(doc, field{"commandStatuses", nil})
		}
	}
	if op == "restart" {
		if res.restartDelay != nil {
			doc = append(doc, field{"restartDelayMs", *res.restartDelay})
		} else {
			doc = append(doc, field{"restartDelayMs", nil})
		}
	}
	if attributeOp && op == "attribute-read" {
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
			doc = append(doc, field{"fileStatus", fileStatusName(*fres.status)})
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

func where(t transport, addr string) string {
	if t.kind == "serial" {
		return t.line.device
	}
	return addr
}

// classify maps an error of the library to the contract's error codes.
func classify(err error, c *master) *failure {
	var rejected *dnp3.RejectedError
	var auth *dnp3.AuthError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &rejected):
		return &failure{"rejected", err.Error()}
	case errors.As(err, &auth):
		return &failure{"auth-failed", err.Error()}
	case c.lost():
		return &failure{"connection-lost", err.Error()}
	case errors.Is(err, dnp3.ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		return &failure{"timeout", err.Error()}
	}
	return &failure{"failed", err.Error()}
}
