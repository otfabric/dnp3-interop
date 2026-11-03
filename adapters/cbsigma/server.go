// SPDX-License-Identifier: MIT

// The outstation: serves a fixture as docs/FIXTURES.md describes, with one
// library Association per outstation address.

package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	dnp3 "github.com/cB-Sigma/dnp3"
)

// libraryTypes are the library's point types of the fixture's arrays.
var libraryTypes = map[string]string{
	"binaryInputs": "binary", "doubleBitInputs": "double", "binaryOutputs": "binary-output", "counters": "counter",
	"frozenCounters": "frozen-counter", "analogInputs": "analog", "analogOutputs": "analog-output",
	"octetStrings": "octet-string", "frozenAnalogInputs": "frozen-analog",
}

// servable refuses what the library's outstation cannot serve.
func servable(fx *fixture) error {
	for _, kind := range []string{"binaryOutputs", "analogOutputs"} {
		for _, p := range fx.Points[kind] {
			if p.CmdClass != 0 {
				return fmt.Errorf("%s[%d]: commandEventClass: the library's outstation records no command events (g13, g43)", kind, p.Index)
			}
		}
	}
	if len(fx.Attributes) > 0 {
		return errors.New("deviceAttributes: the library's outstation serves device attributes of set 0 only")
	}
	return nil
}

// load gives every point its variations with no event class, then its
// initial value (which so raises no event), then its class.
func load(ctx context.Context, db *dnp3.PointDB, fx *fixture) error {
	now := time.Now()
	for _, kind := range pointTypes {
		typ := libraryTypes[kind]
		for _, p := range fx.Points[kind] {
			dp := dnp3.DBPoint{Type: typ, Index: uint32(p.Index), Class0: true, StaticVar: p.SVar, EventVar: p.EVar}
			if kind == "octetStrings" {
				dp.StaticVar, dp.EventVar = 0, 0
			}
			if err := db.Define(dp); err != nil {
				return fmt.Errorf("%s[%d]: %v", kind, p.Index, err)
			}
			var raw []byte
			if kind == "octetStrings" {
				raw = p.Octets
			}
			if _, err := db.Update(ctx, typ, uint32(p.Index), p.Value, raw, p.Flags, now); err != nil {
				return fmt.Errorf("%s[%d]: %v", kind, p.Index, err)
			}
			if p.Class != 0 {
				db.Assign(typ, []uint32{uint32(p.Index)}, p.Class)
			}
		}
	}
	for _, t := range fx.TimeIntervals {
		if err := db.Define(dnp3.DBPoint{Type: "time-and-interval", Index: uint32(t.Index), Value: float64(t.Interval),
			Time: t.Time, Units: t.Units}); err != nil {
			return fmt.Errorf("timeAndIntervals[%d]: %v", t.Index, err)
		}
		if err := db.SetTimeAndInterval(uint32(t.Index), dnp3.TimeAndInterval{Time: t.Time, Interval: t.Interval, Units: t.Units}); err != nil {
			return fmt.Errorf("timeAndIntervals[%d]: %v", t.Index, err)
		}
	}
	return nil
}

// station is one outstation: its fixture, database and association.
type station struct {
	addr, master uint16
	first        bool // the first outstation reports broadcasts
	fx           *fixture
	db           *dnp3.PointDB
	assoc        *dnp3.Association
	store        *memStore
	sniff        sniffer

	mu       sync.Mutex
	controls int // controls of the current request (maxControlsPerRequest)
	authUser *uint16
}

// ---- the library's Handler ----

func controlFunction(c dnp3.ControlRequest) string {
	switch {
	case c.Select:
		return "select"
	case c.Func == dnp3.FCDirectOperate:
		return "direct-operate"
	case c.Func == dnp3.FCDirectOperateNR:
		return "direct-operate-no-ack"
	}
	return "operate"
}

// check is the control table of docs/FIXTURES.md: the status of a control
// and the value it gives the output.
func (s *station) check(c dnp3.ControlRequest) (uint8, float64) {
	const notSupported, outOfRange = 4, 12
	direct := c.Func == dnp3.FCDirectOperate || c.Func == dnp3.FCDirectOperateNR
	if c.Group == 12 {
		p := s.fx.find("binaryOutputs", uint16(c.Index))
		if p == nil || c.Index > 65535 {
			return notSupported, 0
		}
		var v float64
		switch c.CROB.Code {
		case 0x01, 0x02:
			if !p.Pulse {
				return notSupported, 0
			}
			v = -1 // a pulse ends where it began
		case 0x03, 0x41:
			v = 1
		case 0x04, 0x81:
		default:
			return notSupported, 0
		}
		if direct && p.SelectRequired {
			return notSupported, 0
		}
		return 0, v
	}
	p := s.fx.find("analogOutputs", uint16(c.Index))
	if p == nil || c.Index > 65535 {
		return notSupported, 0
	}
	if (p.Min != nil && c.Value < *p.Min) || (p.Max != nil && c.Value > *p.Max) {
		return outOfRange, 0
	}
	if direct && p.SelectRequired {
		return notSupported, 0
	}
	return 0, c.Value
}

// follow sets an output and its target to v, keeping their flags.
func (s *station) follow(ctx context.Context, output, input string, index uint16, v float64) {
	now := time.Now()
	if p, ok := s.db.Point(libraryTypes[output], uint32(index)); ok {
		_, _ = s.db.Update(ctx, libraryTypes[output], uint32(index), v, nil, p.Flags, now)
	}
	if fp := s.fx.find(output, index); fp != nil && fp.Target != nil {
		if p, ok := s.db.Point(libraryTypes[input], uint32(*fp.Target)); ok {
			_, _ = s.db.Update(ctx, libraryTypes[input], uint32(*fp.Target), v, nil, p.Flags, now)
		}
	}
}

func (s *station) Control(ctx context.Context, c dnp3.ControlRequest) uint8 {
	if !(c.Group == 12 && c.Var == 1) && c.Group != 41 {
		return 4 // pattern controls are not in the fixture
	}
	s.mu.Lock()
	s.controls++
	tooMany := s.fx.MaxControls > 0 && s.controls > s.fx.MaxControls
	s.mu.Unlock()
	status, v := s.check(c)
	if tooMany {
		status = 8 // TOO_MANY_OPS
	}
	if status == 0 && !c.Select {
		switch {
		case c.Group == 41:
			s.follow(ctx, "analogOutputs", "analogInputs", uint16(c.Index), v)
		case v >= 0:
			s.follow(ctx, "binaryOutputs", "binaryInputs", uint16(c.Index), v)
		}
	}
	ev := object{{"event", "command"}, {"function", controlFunction(c)}}
	if c.Group == 12 {
		ev = append(ev, object{{"type", "g12v1"}, {"index", c.Index}, {"code", c.CROB.Code}, {"count", c.CROB.Count},
			{"onMs", c.CROB.OnTime}, {"offMs", c.CROB.OffTime}}...)
	} else {
		ev = append(ev, object{{"type", "g41v" + strconv.Itoa(int(c.Var))}, {"index", c.Index}, {"value", number(c.Value)}}...)
	}
	emit(append(ev, field{"status", commandStatusName(status)}))
	return status
}

func (s *station) DataSetControl(context.Context, dnp3.DataSetControl) uint8 { return 4 }

func (s *station) Restart(_ context.Context, cold bool) time.Duration {
	kind, delay := "warm", s.fx.WarmDelayMs
	if cold {
		kind, delay = "cold", s.fx.ColdDelayMs
	}
	emit(object{{"event", "restart"}, {"type", kind}})
	if delay == nil {
		return 0
	}
	return time.Duration(*delay) * time.Millisecond
}

// Activate takes every configuration at once (ACTIVATE_CONFIG, contract
// 1.5).
func (s *station) Activate(_ context.Context, names [][]byte) dnp3.ActivateStatus {
	emit(object{{"event", "manage"}, {"function", dnp3.FCActivateConfig}})
	st := dnp3.ActivateStatus{}
	for range names {
		st.Elements = append(st.Elements, dnp3.StatusElement{Code: dnp3.ActivateOK})
	}
	return st
}

// Application accepts INITIALIZE, START and STOP APPLICATION (16..18).
func (s *station) Application(_ context.Context, fc byte, _ [][]byte) dnp3.IIN {
	emit(object{{"event", "manage"}, {"function", fc}})
	return 0
}

func (s *station) TimeSet(t time.Time) {
	emit(object{{"event", "time-sync"}, {"time", formatTime(t)}})
}

func (s *station) DataSetWritten(context.Context, dnp3.DataSet) {}

// filter applies the parts of docs/FIXTURES.md that the library leaves to
// the device profile, through OutstationConfig.Filter: restarts the fixture
// has no delay for and unsolicited responses it does not support are not
// supported (IIN2.0), and a control of an output that does not exist is a
// parameter error (IIN2.2). It also starts the count of the controls of a
// request and reports an authenticated request that is carried out.
func (s *station) filter(req dnp3.Fragment) dnp3.IIN {
	s.mu.Lock()
	s.controls = 0
	user := s.authUser
	s.authUser = nil
	s.mu.Unlock()
	iin := s.refuse(req)
	if iin == 0 && user != nil {
		emit(object{{"event", "auth"}, {"user", *user}, {"function", req.Func}})
	}
	return iin
}

func (s *station) refuse(req dnp3.Fragment) dnp3.IIN {
	switch req.Func {
	case dnp3.FCColdRestart:
		if s.fx.ColdDelayMs == nil {
			return dnp3.IINNoFuncCodeSupport
		}
	case dnp3.FCWarmRestart:
		if s.fx.WarmDelayMs == nil {
			return dnp3.IINNoFuncCodeSupport
		}
	case dnp3.FCEnableUnsolicited, dnp3.FCDisableUnsolicited:
		if !s.fx.Unsolicited {
			return dnp3.IINNoFuncCodeSupport
		}
	case dnp3.FCSelect, dnp3.FCOperate, dnp3.FCDirectOperate, dnp3.FCDirectOperateNR:
		blocks, _ := dnp3.ParseObjects(req.Objects, true)
		for _, b := range blocks {
			kind := ""
			switch {
			case b.Header.Group == 12 && b.Header.Var == 1:
				kind = "binaryOutputs"
			case b.Header.Group == 41:
				kind = "analogOutputs"
			default:
				continue
			}
			for _, p := range b.Points {
				if p.Index > 65535 || s.fx.find(kind, uint16(p.Index)) == nil {
					return dnp3.IINParameterError
				}
			}
		}
	}
	return 0
}

// ---- the server command ----

func runServer(argv []string) int {
	flags := []string{"fixture", "bind-address", "port", "ready-file", "select-timeout-ms",
		"confirm-timeout-ms", "local-addr", "remote-addr", "outstations", "control-socket", "frozen-event-policy",
		"file-user", "file-password"}
	flags = append(append(append(flags, serverTransport...), saFlags...), statisticsFlags...)
	a, err := parseArgs(argv, flags, nil)
	var port, selectTimeout, confirmTimeout, local, remote, count int64
	var tr transport
	var sec security
	if err == nil {
		tr, err = parseTransport(a, true)
	}
	if err == nil {
		sec, err = parseSecurity(a, true)
	}
	for _, f := range []struct {
		dst           *int64
		name          string
		def, min, max int64
	}{
		{&port, "port", 20000, 1, 65535}, {&selectTimeout, "select-timeout-ms", 5000, 1, 3600000},
		{&confirmTimeout, "confirm-timeout-ms", 5000, 1, 3600000}, {&local, "local-addr", -1, 0, 65519},
		{&remote, "remote-addr", -1, 0, 65519}, {&count, "outstations", 1, 1, 16},
	} {
		if err == nil {
			*f.dst, err = a.integer(f.name, f.def, f.min, f.max)
		}
	}
	// A freeze raises a frozen counter event only when it changes the
	// frozen value (the library's update detects the change).
	if policy := a.str("frozen-event-policy", "on-change"); err == nil && policy != "on-change" {
		err = usagef("--frozen-event-policy: this outstation has only the policy on-change")
	}
	if err == nil && a.has("file-user") != a.has("file-password") {
		err = usagef("--file-user and --file-password go together")
	}
	if err != nil {
		logf("server: %v", err)
		return exitUsage
	}
	path := a.str("fixture", defaultFixture)
	text, err := os.ReadFile(path)
	var fx *fixture
	if err == nil {
		fx, err = loadFixture(text)
	}
	if err == nil {
		err = servable(fx)
	}
	if err != nil {
		logf("server: fixture %s: %v", path, err)
		return exitFixtureInvalid
	}
	if local < 0 {
		local = int64(fx.Address)
	}
	if remote < 0 {
		remote = int64(fx.MasterAddress)
	}
	if last := local + count - 1; last > 65519 || (remote >= local && remote <= last) {
		logf("server: --outstations %d from link address %d leaves the range or meets the master's", count, local)
		return exitUsage
	}
	hasFiles := fx.Files != nil || fx.Directories != nil
	if a.has("file-user") && !hasFiles {
		logf("server: --file-user needs a fixture with files")
		return exitUsage
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	r := &router{}
	for i := range count {
		s := &station{addr: uint16(local + i), master: uint16(remote), first: i == 0, fx: fx, db: dnp3.NewPointDB()}
		s.sniff.st = s
		if err := load(ctx, s.db, fx); err != nil {
			logf("server: fixture %s: %v", path, err)
			return exitFixtureInvalid
		}
		auth, err := sec.outstation()
		if err != nil {
			logf("server: secure authentication: %v", err)
			return exitFailed
		}
		size := cmp.Or(fx.EventBuffer, 1000)
		events := dnp3.NewEventBuffer(map[int]int{1: size, 2: size, 3: size}, nil)
		// What does not fit pushes out the oldest event of its class.
		events.SetDropOldest(true)
		var needTime time.Duration
		if fx.NeedTime {
			needTime = 100 * 365 * 24 * time.Hour // until a master writes the time
		}
		cfg := dnp3.OutstationConfig{
			Address: s.addr, Master: s.master, Log: libraryLog(), DB: s.db, Events: events, Handler: s,
			ConfirmTimeout: time.Duration(confirmTimeout) * time.Millisecond,
			SelectTimeout:  time.Duration(selectTimeout) * time.Millisecond,
			Unsolicited:    fx.Unsolicited, UnsolRetries: -1,
			NeedTime: needTime, Auth: auth, Broadcast: true, Filter: s.filter,
		}
		// maxControlsPerRequest: the handler answers TOO_MANY_OPS past the
		// limit; the library would refuse every control of the request.
		if fx.MaxControls > 0 {
			cfg.MaxControls = 65535
		}
		if hasFiles {
			s.store = newStore(fx)
			files := &dnp3.OutstationFiles{Store: s.store}
			if user, ok := a["file-user"]; ok {
				files.Users = map[string]dnp3.FileUser{user: {Password: dnp3.Secret(a["file-password"]), Write: true}}
			}
			cfg.Files = files
		}
		s.assoc = dnp3.NewAssociation(cfg)
		r.stations = append(r.stations, s)
	}

	done := make(chan error, 1)
	address, err := r.open(ctx, tr, net.JoinHostPort(a.str("bind-address", "0.0.0.0"), strconv.Itoa(int(port))), done)
	if err != nil {
		logf("server: cannot open the %s channel: %v", tr.name(), err)
		return exitFailed
	}
	for _, s := range r.stations {
		go s.assoc.Run(ctx)
	}
	// The control socket (contract 1.4), for a server of one outstation.
	if count == 1 {
		control := a.str("control-socket", defaultControlSocket)
		l, err := listenControl(control, r.stations[0].db)
		if err != nil {
			logf("server: cannot open the control socket %s: %v", control, err)
			return exitFailed
		}
		defer func() {
			_ = l.Close()
			_ = os.Remove(control)
		}()
	}
	readyFile := a.str("ready-file", defaultReadyFile)
	if err := os.WriteFile(readyFile, nil, 0o644); err != nil {
		logf("server: cannot write the readiness file %s: %v", readyFile, err)
		return exitFailed
	}
	emit(object{{"event", "ready"}, {"adapter", adapter}, {"address", address}, {"fixture", fx.Name},
		{"linkAddress", local}, {"masterAddress", remote}, {"transport", tr.name()}, {"dial", tr.connect != ""}})

	signalled := false
	select {
	case <-ctx.Done():
		signalled = true
		logf("server: signal, stopping")
	case err := <-done:
		logf("server: the channel ended without a signal: %v", err)
	}
	_ = os.Remove(readyFile)
	r.close()
	if !signalled {
		return exitFailed
	}
	emit(object{{"event", "stopped"}})
	return exitOK
}
