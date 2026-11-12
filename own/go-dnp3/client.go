// SPDX-License-Identifier: MIT

// The master: one bounded operation per process, one result document.

package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	dnp3 "github.com/otfabric/go-dnp3"
	"github.com/otfabric/go-dnp3/app"
	"github.com/otfabric/go-dnp3/client"
	"github.com/otfabric/go-dnp3/link"
)

// timed lists the variations with a time stamp; objects of other variations
// carry no "time".
var timed = map[[2]uint8]bool{
	{2, 2}: true, {2, 3}: true, {4, 2}: true, {4, 3}: true, {11, 2}: true, {21, 5}: true, {21, 6}: true,
	{22, 5}: true, {22, 6}: true, {23, 5}: true, {23, 6}: true, {32, 3}: true, {32, 4}: true, {32, 7}: true,
	{32, 8}: true, {42, 3}: true, {42, 4}: true, {42, 7}: true, {42, 8}: true,
	{13, 2}: true, {31, 3}: true, {31, 4}: true, {33, 3}: true, {33, 4}: true, {33, 7}: true, {33, 8}: true,
	{43, 3}: true, {43, 4}: true, {43, 7}: true, {43, 8}: true, {50, 4}: true, {122, 2}: true,
}

// noFlags lists the variations without a flags octet. The library decodes
// them with no flags; the adapter reports them ONLINE, as the static value
// of a point the outstation reports at all, the way the other masters do.
var noFlags = map[[2]uint8]bool{
	{1, 1}: true, {10, 1}: true, {20, 5}: true, {20, 6}: true, {21, 9}: true, {21, 10}: true,
	{30, 3}: true, {30, 4}: true, {31, 5}: true, {31, 6}: true,
}

// handler collects the measurements the library delivers, in order.
type measurementHandler struct {
	mu      sync.Mutex
	objects []object
}

func (h *measurementHandler) HandleMeasurements(info client.ResponseInfo, ms []client.Measurement) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, m := range ms {
		if o := measurement(m, info.Unsolicited); o != nil {
			h.objects = append(h.objects, o)
		}
	}
}

// measurement is the object document of a measurement, or nil for one the
// contract has no document for.
func measurement(m client.Measurement, unsolicited bool) object {
	g, v := m.GV.Group(), m.GV.Variation()
	o := object{{"group", g}, {"variation", v}, {"index", m.Index}}
	var flags app.Flags
	var stamp app.Timestamp
	switch x := m.Value.(type) {
	case app.Binary:
		o, flags, stamp = append(o, field{"value", x.Value}), x.Flags, x.Time
	case app.DoubleBitBinary:
		o, flags, stamp = append(o, field{"value", int(x.Value)}), x.Flags, x.Time
	case app.Counter:
		o, flags, stamp = append(o, field{"value", x.Value}), x.Flags, x.Time
	case app.Analog:
		o, flags, stamp = append(o, field{"value", number(x.Value)}), x.Flags, x.Time
	case app.SecurityStatistic:
		o = append(o, field{"value", x.Value}, field{"flags", flagNames(g, uint8(x.Flags))},
			field{"association", x.Association})
		if timed[[2]uint8{g, v}] {
			o = append(o, field{"time", formatTime(x.Time.Time())})
		}
		return append(o, field{"unsolicited", unsolicited})
	case app.BinaryCommandEvent:
		o = append(o, field{"value", x.Value}, field{"status", commandStatusName(uint8(x.Status))})
		if timed[[2]uint8{g, v}] {
			o = append(o, field{"time", formatTime(x.Time.Time())})
		}
		return append(o, field{"unsolicited", unsolicited})
	case app.AnalogCommandEvent:
		o = append(o, field{"value", number(x.Value)}, field{"status", commandStatusName(uint8(x.Status))})
		if timed[[2]uint8{g, v}] {
			o = append(o, field{"time", formatTime(x.Time.Time())})
		}
		return append(o, field{"unsolicited", unsolicited})
	case app.AnalogDeadband:
		return append(o, field{"value", number(x.Value)}, field{"unsolicited", unsolicited})
	case app.AbsoluteTime:
		return append(o, field{"value", x.Interval}, field{"time", formatTime(x.Time.Time())},
			field{"units", uint8(x.Units)}, field{"unsolicited", unsolicited})
	case app.OctetString:
		return object{{"group", g}, {"variation", len(x.Data)}, {"index", m.Index},
			{"value", fmt.Sprintf("%x", x.Data)}, {"unsolicited", unsolicited}}
	default:
		return nil
	}
	if noFlags[[2]uint8{g, v}] {
		flags = app.FlagOnline
	}
	o = append(o, field{"flags", flagNames(g, uint8(flags))})
	if timed[[2]uint8{g, v}] {
		o = append(o, field{"time", formatTime(stamp.Time())})
	}
	return append(o, field{"unsolicited", unsolicited})
}

// clientMetrics keeps the IIN of the last solicited response: the library
// returns a control's statuses, not the IIN of its response. It also counts
// the unsolicited fragments, repeats included: the library hands a repeat's
// events to the handler only once.
type clientMetrics struct {
	dnp3.NopMetrics
	mu          sync.Mutex
	lastIIN     *app.IIN
	unsolicited int
}

func (m *clientMetrics) OnFragmentReceived(_ net.Addr, f *app.Fragment, _ int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch f.Function {
	case app.FuncResponse:
		iin := f.IIN
		m.lastIIN = &iin
	case app.FuncUnsolicitedResponse:
		m.unsolicited++
	}
}

func (m *clientMetrics) unsolicitedFragments() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.unsolicited
}

func (m *clientMetrics) last() *app.IIN {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastIIN
}

type failure struct{ code, message string }

// request is a parsed operation: what it does once connected.
type request func(ctx context.Context, c *client.Client, r *result) error

type result struct {
	// iin: read operations, the IIN of the response.
	iin           *app.IIN
	commandStatus *uint8
	// operate-multi: one status per control, in the order sent.
	commandStatuses []uint8
	restartDelay    *time.Duration
}

func (r *result) read(res *client.Result, err error) error {
	var ie *dnp3.IINError
	switch {
	// The library hands back an empty result when no response arrived:
	// that has no IIN.
	case errors.Is(err, dnp3.ErrTimeout), errors.Is(err, dnp3.ErrConnectionLost),
		errors.Is(err, dnp3.ErrNotConnected), errors.Is(err, dnp3.ErrClosed):
	case res != nil:
		iin := res.IIN
		r.iin = &iin
	case errors.As(err, &ie):
		iin := ie.IIN
		r.iin = &iin
	}
	return err
}

var controlCodes = map[string]app.CROB{
	"LATCH_ON": {Op: app.OpLatchOn}, "LATCH_OFF": {Op: app.OpLatchOff}, "PULSE_ON": {Op: app.OpPulseOn},
	"PULSE_OFF": {Op: app.OpPulseOff}, "CLOSE": {Op: app.OpPulseOn, TripClose: app.TCCClose},
	"TRIP": {Op: app.OpPulseOn, TripClose: app.TCCTrip},
}

var analogTypes = map[string]app.GroupVariation{"g41v1": app.G41V1, "g41v2": app.G41V2, "g41v3": app.G41V3, "g41v4": app.G41V4}

// analogCommand is a set-point of a g41 variation, with the value in its
// range.
func analogCommand(kind string, index uint16, value float64) (client.Command, bool) {
	integral := func(lo, hi float64) bool { return value == math.Floor(value) && value >= lo && value <= hi }
	ok := false
	switch kind {
	case "g41v1":
		ok = integral(math.MinInt32, math.MaxInt32)
	case "g41v2":
		ok = integral(math.MinInt16, math.MaxInt16)
	case "g41v3":
		ok = math.Abs(value) <= math.MaxFloat32
	case "g41v4":
		ok = true
	}
	return client.Command{Index: uint32(index), Value: app.AnalogOutput{Value: value}, GV: analogTypes[kind]}, ok
}

func classList(a args, name string) ([]int, error) { return a.classes(name, "1,2,3", 1) }

var manageFunctions = map[string]app.FunctionCode{
	"initialize-data": app.FuncInitializeData, "initialize-application": app.FuncInitializeApplication,
	"start-application": app.FuncStartApplication, "stop-application": app.FuncStopApplication,
	"save-configuration": app.FuncSaveConfiguration,
}

func parseOperation(op string, a args) (request, error) {
	switch op {
	case "integrity-poll":
		return func(ctx context.Context, c *client.Client, r *result) error { return r.read(c.IntegrityPoll(ctx)) }, nil
	case "event-poll":
		classes, err := classList(a, "classes")
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *client.Client, r *result) error {
			return r.read(c.EventPoll(ctx, classes...))
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
		h, err := objectHeader(a, uint8(g), uint8(v))
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *client.Client, r *result) error { return r.read(c.Read(ctx, h)) }, nil
	case "operate":
		return parseOperate(a)
	case "operate-multi":
		return parseOperateMulti(a)
	case "deadband-write":
		objs, err := parseDeadbands(a)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *client.Client, _ *result) error {
			_, err := c.Write(ctx, app.Indexed(app.G34V3, objs...))
			return err
		}, nil
	case "time-sync":
		p := client.TimeSyncLAN
		switch a.str("procedure", "lan") {
		case "lan":
		case "non-lan":
			p = client.TimeSyncNonLAN
		default:
			return nil, usagef("--procedure must be lan or non-lan")
		}
		return func(ctx context.Context, c *client.Client, _ *result) error { return c.SyncTime(ctx, p) }, nil
	case "restart":
		var restart func(*client.Client, context.Context) (time.Duration, error)
		switch a.str("type", "cold") {
		case "cold":
			restart = (*client.Client).ColdRestart
		case "warm":
			restart = (*client.Client).WarmRestart
		default:
			return nil, usagef("--type must be cold or warm")
		}
		return func(ctx context.Context, c *client.Client, r *result) error {
			d, err := restart(c, ctx)
			if err == nil {
				r.restartDelay = &d
			}
			return err
		}, nil
	case "enable-unsolicited", "disable-unsolicited":
		classes, err := classList(a, "classes")
		if err != nil {
			return nil, err
		}
		if op == "enable-unsolicited" {
			return func(ctx context.Context, c *client.Client, _ *result) error {
				return c.EnableUnsolicited(ctx, classes...)
			}, nil
		}
		return func(ctx context.Context, c *client.Client, _ *result) error {
			return c.DisableUnsolicited(ctx, classes...)
		}, nil
	case "assign-class":
		class, err := a.requiredInteger("class", 0, 3)
		if err != nil {
			return nil, err
		}
		g, err := a.requiredInteger("group", 0, 255)
		if err != nil {
			return nil, err
		}
		h, err := objectHeader(a, uint8(g), 0)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *client.Client, _ *result) error { return c.AssignClass(ctx, int(class), h) }, nil
	case "freeze":
		return parseFreeze(a)
	case "manage":
		name, err := a.required("function")
		if err != nil {
			return nil, err
		}
		if name == "activate-config" {
			// Contract 1.5: one g70v8 specification, free format. The
			// g91v1 status is not reported; an IIN2 error is a refusal.
			spec := a.str("spec", "config.xml")
			if len(spec) < 1 || len(spec) > 255 {
				return nil, usagef("--spec must be 1..255 octets")
			}
			h := app.FreeFormat(app.G70V8, app.FileSpecification{Name: spec})
			return func(ctx context.Context, c *client.Client, _ *result) error {
				_, err := c.ActivateConfig(ctx, h)
				return err
			}, nil
		}
		fc, ok := manageFunctions[name]
		if !ok {
			return nil, usagef("--function must be initialize-data, initialize-application, start-application, stop-application, save-configuration or activate-config")
		}
		if a.has("spec") {
			return nil, usagef("--spec is for --function activate-config")
		}
		var hs []app.Header
		switch fc {
		case app.FuncInitializeApplication, app.FuncStartApplication, app.FuncStopApplication:
			hs = []app.Header{app.AllObjects(app.GV(90, 1))}
		}
		return func(ctx context.Context, c *client.Client, _ *result) error {
			_, err := c.Request(ctx, fc, hs...)
			return err
		}, nil
	}
	return nil, usagef("unknown operation '%s'", op)
}

// objectHeader is a range header with --start and --stop, all objects
// without.
func objectHeader(a args, g, v uint8) (app.Header, error) {
	if a.has("start") != a.has("stop") {
		return app.Header{}, usagef("--start and --stop go together")
	}
	if !a.has("start") {
		return app.AllObjects(app.GV(g, v)), nil
	}
	start, err := a.integer("start", 0, 0, 65535)
	if err != nil {
		return app.Header{}, err
	}
	stop, err := a.integer("stop", 0, 0, 65535)
	if err != nil {
		return app.Header{}, err
	}
	if start > stop {
		return app.Header{}, usagef("--start is larger than --stop")
	}
	return app.IndexRange(app.GV(g, v), uint32(start), uint32(stop)), nil
}

func parseFreeze(a args) (request, error) {
	group, err := a.integer("group", 20, 20, 30)
	if err != nil || (group != 20 && group != 30) {
		return nil, usagef("--group must be 20 or 30")
	}
	h := app.AllObjects(app.GV(uint8(group), 0))
	clear, noAck := a.has("clear"), a.has("no-ack")
	if a.has("at") {
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
		if noAck {
			// The library has FreezeAtTime with a response only; the
			// _NR function code goes through Request.
			when := app.Counted(app.G50V2, app.AbsoluteTime{Time: app.TimestampOf(at), Interval: uint32(interval)})
			return func(ctx context.Context, c *client.Client, _ *result) error {
				_, err := c.Request(ctx, app.FuncFreezeAtTimeNoAck, when, h)
				return err
			}, nil
		}
		return func(ctx context.Context, c *client.Client, _ *result) error {
			return c.FreezeAtTime(ctx, at, time.Duration(interval)*time.Millisecond, h)
		}, nil
	}
	if a.has("interval-ms") {
		return nil, usagef("--interval-ms needs --at")
	}
	return func(ctx context.Context, c *client.Client, _ *result) error { return c.Freeze(ctx, clear, noAck, h) }, nil
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
	mode := a.str("mode", "direct")
	modes := map[string]client.CommandMode{"direct": client.DirectOperate, "sbo": client.SelectBeforeOperate,
		"direct-no-ack": client.DirectOperateNoAck, "operate": client.SelectBeforeOperate}
	if _, ok := modes[mode]; !ok {
		return nil, usagef("--mode must be direct, sbo, direct-no-ack or operate")
	}
	if a.has("operate-delay-ms") && mode != "sbo" {
		return nil, usagef("--operate-delay-ms needs --mode sbo")
	}
	delay, err := a.integer("operate-delay-ms", 0, 0, 3600000)
	if err != nil {
		return nil, err
	}
	var cmd client.Command
	if kind == "g12v1" {
		if a.has("value") {
			return nil, usagef("--value is for g41")
		}
		codeName, err := a.required("code")
		if err != nil {
			return nil, err
		}
		crob, ok := controlCodes[codeName]
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
		crob.Count, crob.OnTime, crob.OffTime = uint8(count), uint32(on), uint32(off)
		cmd = client.Command{Index: uint32(index), Value: crob, GV: app.G12V1}
	} else {
		for _, f := range []string{"code", "count", "on-ms", "off-ms"} {
			if a.has(f) {
				return nil, usagef("--code, --count, --on-ms and --off-ms are for g12v1")
			}
		}
		if _, ok := analogTypes[kind]; !ok {
			return nil, usagef("--type must be g12v1, g41v1, g41v2, g41v3 or g41v4")
		}
		value, err := a.number("value")
		if err != nil {
			return nil, err
		}
		var ok bool
		if cmd, ok = analogCommand(kind, uint16(index), value); !ok {
			return nil, usagef("--value is out of the range of %s", kind)
		}
	}
	if a.has("broadcast") {
		h := app.Indexed(cmd.GV, app.Object{Index: cmd.Index, Value: cmd.Value})
		return func(ctx context.Context, c *client.Client, _ *result) error {
			return c.Broadcast(ctx, link.BroadcastOptionalConfirm, app.FuncDirectOperateNoAck, h)
		}, nil
	}
	if mode == "operate" || delay > 0 {
		// Contract 1.5: an OPERATE without its SELECT, or after a delay.
		// Client.Operate sends both in one turn, so each is a request here.
		h := app.Indexed(cmd.GV, app.Object{Index: cmd.Index, Value: cmd.Value})
		return func(ctx context.Context, c *client.Client, r *result) error {
			if mode == "sbo" {
				if err := controlStep(ctx, c, app.FuncSelect, h, r); err != nil {
					return err
				}
				select {
				case <-time.After(time.Duration(delay) * time.Millisecond):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return controlStep(ctx, c, app.FuncOperate, h, r)
		}, nil
	}
	return func(ctx context.Context, c *client.Client, r *result) error {
		statuses, err := c.Operate(ctx, modes[mode], cmd)
		if len(statuses) > 0 && mode != "direct-no-ack" {
			st := uint8(statuses[0])
			r.commandStatus = &st
		}
		return err
	}, nil
}

// controlStep sends one SELECT or OPERATE of one control and reports its
// status as Client.Operate does: a status other than SUCCESS is a
// *dnp3.CommandError.
func controlStep(ctx context.Context, c *client.Client, fc app.FunctionCode, h app.Header, r *result) error {
	res, err := c.Request(ctx, fc, h)
	var ie *dnp3.IINError
	if err != nil && (!errors.As(err, &ie) || res == nil) {
		return err
	}
	for _, e := range res.Headers() {
		for _, o := range e.Objects {
			var st app.CommandStatus
			switch v := o.Value.(type) {
			case app.CROB:
				st = v.Status
			case app.AnalogOutput:
				st = v.Status
			default:
				continue
			}
			s := uint8(st)
			r.commandStatus = &s
			if st != app.StatusSuccess {
				return &dnp3.CommandError{GV: h.GV, Index: o.Index, Status: st}
			}
			return err
		}
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: no control echo", dnp3.ErrProtocol)
}

// parseControls reads --controls: "g12v1:INDEX:CODE" and "g41vN:INDEX:VALUE"
// items, comma-separated.
func parseControls(text string) ([]client.Command, error) {
	var cmds []client.Command
	for _, item := range strings.Split(text, ",") {
		parts := strings.Split(item, ":")
		if len(parts) != 3 {
			return nil, usagef("--controls items are TYPE:INDEX:CODE or TYPE:INDEX:VALUE")
		}
		index, err := strconv.ParseUint(parts[1], 10, 16)
		if err != nil {
			return nil, usagef("--controls: '%s' is not a point index", parts[1])
		}
		if parts[0] == "g12v1" {
			crob, ok := controlCodes[parts[2]]
			if !ok {
				return nil, usagef("--controls: '%s' is not a control code", parts[2])
			}
			crob.Count, crob.OnTime, crob.OffTime = 1, 100, 100
			cmds = append(cmds, client.Command{Index: uint32(index), Value: crob, GV: app.G12V1})
			continue
		}
		value, err := strconv.ParseFloat(parts[2], 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, usagef("--controls: '%s' is not a number", parts[2])
		}
		cmd, ok := analogCommand(parts[0], uint16(index), value)
		if !ok {
			return nil, usagef("--controls: '%s' is not a control type with a value in its range", item)
		}
		cmds = append(cmds, cmd)
	}
	return cmds, nil
}

func parseOperateMulti(a args) (request, error) {
	text, err := a.required("controls")
	if err != nil {
		return nil, err
	}
	cmds, err := parseControls(text)
	if err != nil {
		return nil, err
	}
	mode := client.DirectOperate
	switch a.str("mode", "direct") {
	case "direct":
	case "sbo":
		mode = client.SelectBeforeOperate
	default:
		return nil, usagef("--mode must be direct or sbo")
	}
	return func(ctx context.Context, c *client.Client, r *result) error {
		statuses, err := c.Operate(ctx, mode, cmds...)
		if len(statuses) == len(cmds) {
			r.commandStatuses = []uint8{}
			first := uint8(0)
			for _, st := range statuses {
				r.commandStatuses = append(r.commandStatuses, uint8(st))
				if first == 0 && st != app.StatusSuccess {
					first = uint8(st)
				}
			}
			r.commandStatus = &first
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
	if !slices.Contains(clientOperations, op) {
		logf("client: unknown operation '%s'", op)
		return exitUsage
	}
	flags := []string{"host", "port", "local-addr", "remote-addr", "connect-timeout-ms", "timeout-ms", "collect-ms", "keep-alive-ms"}
	flags = append(append(flags, clientTransportFlags...), saFlags...)
	switches := append(slices.Clone(clientTransportSwitches), "no-confirm")
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
		switches = append(switches, "no-unsolicited-confirm")
	case "manage":
		flags = append(flags, "function", "spec")
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
		creds                                                           *fileCredentials
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
	if err == nil && slices.Contains(fileOperations, op) {
		creds, err = parseFileCredentials(op, a)
	}
	// --broadcast (contract 1.3): to 0xFFFF, which never answers.
	if err == nil && a.has("broadcast") && a.str("mode", "direct") != "direct-no-ack" {
		err = usagef("--broadcast needs --mode direct-no-ack")
	}
	if err == nil && a.has("no-unsolicited-confirm") && a.has("no-confirm") {
		err = usagef("--no-unsolicited-confirm and --no-confirm do not go together")
	}
	fileOp := slices.Contains(fileOperations, op)
	attributeOp := slices.Contains(attributeOperations, op)
	switch {
	case err != nil, op == "monitor":
	case fileOp:
		freq, err = parseFileOperation(op, a, creds)
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
	h := &measurementHandler{}
	metrics := &clientMetrics{}
	stream := &clientStream{t: tr, host: host, port: port, connectTimeout: time.Duration(connectTimeout) * time.Millisecond}
	ka := time.Duration(keepAlive) * time.Millisecond
	if ka == 0 {
		ka = -1
	}
	opts := []client.Option{
		client.WithAddresses(link.Address(local), link.Address(remote)),
		client.WithResponseTimeout(time.Duration(timeout) * time.Millisecond),
		// The master does nothing on its own but clear the restart bit.
		client.WithAutoIntegrity(false),
		client.WithAutoEventPoll(false),
		client.WithTimeSync(client.TimeSyncNone),
		// No startup sequence at Connect (go-dnp3 v1.11.0 runs an integrity
		// poll by default), which would read and confirm the events a case
		// checks.
		client.WithStartup(client.Startup{}),
		client.WithHandler(h),
		client.WithMetrics(metrics),
		client.WithLogger(libraryLog()),
		client.WithStream(stream.open),
		client.WithLink(client.Link{Confirmed: a.has("link-confirms"), KeepAlive: ka}),
	}
	if op == "operate" && (a.str("mode", "") == "operate" || a.has("operate-delay-ms")) {
		// Contract 1.5: nothing between the SELECT and the OPERATE, or the
		// OPERATE is not the next request: no automatic clear of the
		// restart bit after the SELECT's response.
		opts = append(opts, client.WithAutoClearRestart(false))
	}
	// Contract 1.5: --no-confirm confirms no solicited response, but still
	// the unsolicited ones; --no-unsolicited-confirm the reverse.
	if a.has("no-confirm") {
		opts = append(opts, client.WithSolicitedConfirm(false))
	}
	if a.has("no-unsolicited-confirm") {
		opts = append(opts, client.WithUnsolicitedConfirm(false))
	}
	if sec.on {
		opts = append(opts, client.WithSecureAuthentication(sec.user))
	}

	var fail *failure
	var res result
	var fres fileResult
	var attributes []object
	var c *client.Client
	setupErr := stream.setup()
	if setupErr == nil {
		c, setupErr = client.New(stream.addr, opts...)
	}
	connected := false
	switch {
	case setupErr != nil:
		fail = &failure{"failed", setupErr.Error()}
	default:
		cctx, cancel := context.WithTimeout(context.Background(),
			time.Duration(connectTimeout+2*timeout)*time.Millisecond+time.Second)
		err := c.Connect(cctx)
		cancel()
		connected = stream.wasOpened()
		switch {
		case !connected:
			msg := "no connection to " + stream.addr
			if err != nil {
				msg += ": " + err.Error()
			}
			fail = &failure{"connect-failed", msg}
		case err != nil:
			fail = classify(err)
		}
	}

	if fail == nil {
		// The library bounds every request by the response timeout; this
		// only guards against an operation that never ends. A file
		// transfer is many requests.
		bound := 3*time.Duration(timeout)*time.Millisecond + 2*time.Second
		if fileOp {
			bound = 20*time.Duration(timeout)*time.Millisecond + 2*time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), bound)
		var err error
		switch {
		case op == "monitor":
			end := time.Now().Add(time.Duration(duration) * time.Millisecond)
			for time.Now().Before(end) && c.State() == dnp3.StateConnected {
				time.Sleep(10 * time.Millisecond)
			}
			if c.State() != dnp3.StateConnected {
				err = dnp3.ErrConnectionLost
			}
		case fileOp:
			err = freq(ctx, c, &fres)
			if status, ok := fileStatusOf(err); ok {
				name := fileStatusName(status)
				fres.status = &name
			} else if err == nil {
				name := "SUCCESS"
				fres.status = &name
			}
		case attributeOp:
			err = areq(ctx, c, &attributes)
		default:
			err = req(ctx, c, &res)
		}
		cancel()
		fail = classify(err)
		// A response with a request error is a refusal, whatever the
		// library made of it; for a control the library reports the echo.
		if fail == nil || fail.code == "command-failed" {
			if last := metrics.last(); last != nil && *last&0x0700 != 0 && op != "monitor" &&
				(op == "operate" || op == "operate-multi" || fail != nil) {
				fail = &failure{"rejected", "the outstation refused the request: " + (*last & 0x0700).String()}
			}
		}
		if fail != nil && fail.code == "rejected" {
			res.commandStatus, res.commandStatuses = nil, nil
		}
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
	if c != nil {
		_ = c.Close() // waits for the handler
	}
	stream.close()

	reads := op == "integrity-poll" || op == "event-poll" || op == "read"
	h.mu.Lock()
	objects := h.objects
	h.mu.Unlock()
	doc := object{{"schemaVersion", schemaVersion}, {"adapter", adapter}, {"operation", op}, {"ok", fail == nil}}
	if fail != nil {
		doc = append(doc, field{"error", object{{"code", fail.code}, {"message", fail.message}}})
	} else {
		doc = append(doc, field{"error", nil})
	}
	doc = append(doc, field{"connected", connected})
	if reads && res.iin != nil {
		doc = append(doc, field{"iin", iinList(uint16(*res.iin))})
	} else {
		doc = append(doc, field{"iin", nil})
	}
	if objects == nil {
		objects = []object{}
	}
	doc = append(doc, field{"objects", objects})
	statusName := func() any {
		if res.commandStatus == nil {
			return nil
		}
		return commandStatusName(*res.commandStatus)
	}
	switch op {
	case "operate":
		doc = append(doc, field{"commandStatus", statusName()})
	case "operate-multi":
		doc = append(doc, field{"commandStatus", statusName()})
		if res.commandStatuses != nil {
			names := []string{}
			for _, st := range res.commandStatuses {
				names = append(names, commandStatusName(st))
			}
			doc = append(doc, field{"commandStatuses", names})
		} else {
			doc = append(doc, field{"commandStatuses", nil})
		}
	case "restart":
		if res.restartDelay != nil {
			doc = append(doc, field{"restartDelayMs", res.restartDelay.Milliseconds()})
		} else {
			doc = append(doc, field{"restartDelayMs", nil})
		}
	case "attribute-read":
		if attributes == nil {
			attributes = []object{}
		}
		doc = append(doc, field{"attributes", attributes})
	case "monitor":
		if a.has("no-unsolicited-confirm") {
			doc = append(doc, field{"unsolicitedFragments", metrics.unsolicitedFragments()})
		}
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
func classify(err error) *failure {
	var ie *dnp3.IINError
	var ce *dnp3.CommandError
	var fe *dnp3.FileError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, dnp3.ErrAuthentication):
		return &failure{"auth-failed", err.Error()}
	case errors.As(err, &fe):
		return &failure{"file-error", err.Error()}
	case errors.As(err, &ie):
		if ie.IIN&0x0700 != 0 {
			return &failure{"rejected", err.Error()}
		}
		return &failure{"failed", err.Error()}
	case errors.As(err, &ce):
		return &failure{"command-failed", err.Error()}
	case errors.Is(err, dnp3.ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		return &failure{"timeout", err.Error()}
	case errors.Is(err, dnp3.ErrConnectionLost), errors.Is(err, dnp3.ErrNotConnected), errors.Is(err, dnp3.ErrClosed):
		return &failure{"connection-lost", err.Error()}
	}
	return &failure{"failed", err.Error()}
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
// comma-separated, as g34v3 objects in index order.
func parseDeadbands(a args) ([]app.Object, error) {
	text, err := a.required("deadbands")
	if err != nil {
		return nil, err
	}
	values := map[uint32]float64{}
	for _, item := range strings.Split(text, ",") {
		index, value, ok := strings.Cut(item, ":")
		i, ierr := strconv.ParseUint(index, 10, 16)
		v, verr := strconv.ParseFloat(value, 32)
		if !ok || ierr != nil || verr != nil || v < 0 || math.IsInf(v, 0) {
			return nil, usagef("--deadbands items are INDEX:VALUE, VALUE a non-negative number")
		}
		values[uint32(i)] = float64(float32(v))
	}
	var out []app.Object
	for _, i := range slices.Sorted(maps.Keys(values)) {
		out = append(out, app.Object{Index: i, Value: app.AnalogDeadband{Value: values[i]}})
	}
	return out, nil
}
