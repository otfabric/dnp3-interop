// SPDX-License-Identifier: MIT

// The outstation: serves a fixture as docs/FIXTURES.md describes.

package main

import (
	"cmp"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/otfabric/go-dnp3/app"
	"github.com/otfabric/go-dnp3/link"
	"github.com/otfabric/go-dnp3/server"
)

var pointTypeOf = map[string]server.PointType{
	"binaryInputs":       server.BinaryInput,
	"doubleBitInputs":    server.DoubleBitInput,
	"binaryOutputs":      server.BinaryOutput,
	"counters":           server.Counter,
	"frozenCounters":     server.FrozenCounter,
	"analogInputs":       server.AnalogInput,
	"analogOutputs":      server.AnalogOutput,
	"octetStrings":       server.OctetString,
	"frozenAnalogInputs": server.FrozenAnalogInput,
}

// initialValue is the library's value of a point of the fixture.
func initialValue(kind string, p point) app.Value {
	flags := app.Flags(p.Flags)
	switch kind {
	case "binaryInputs", "binaryOutputs":
		return app.Binary{Value: p.Value != 0, Flags: flags}
	case "doubleBitInputs":
		return app.DoubleBitBinary{Value: app.DoubleBit(p.Value), Flags: flags}
	case "counters", "frozenCounters":
		return app.Counter{Value: uint32(p.Value), Flags: flags}
	case "octetStrings":
		return app.OctetString{Data: p.Octets}
	}
	return app.Analog{Value: p.Value, Flags: flags}
}

// populate adds the points of the fixture to a database.
func populate(db *server.Database, fx *fixture) error {
	for _, kind := range pointTypes {
		for _, p := range fx.Points[kind] {
			cfg := server.PointConfig{Class: server.Class(p.Class), StaticVariation: p.SVar, EventVariation: p.EVar,
				CommandEventClass: server.Class(p.CmdClass), CommandEventVariation: p.CmdVar}
			if kind == "octetStrings" {
				cfg.StaticVariation, cfg.EventVariation = 0, 0
			}
			if err := db.Add(pointTypeOf[kind], uint32(p.Index), cfg, initialValue(kind, p)); err != nil {
				return fmt.Errorf("%s[%d]: %v", kind, p.Index, err)
			}
		}
	}
	for _, t := range fx.TimeIntervals {
		v := app.AbsoluteTime{Time: app.TimestampOf(t.Time), Interval: t.Interval, Units: app.IntervalUnit(t.Units)}
		if err := db.Add(server.TimeAndInterval, uint32(t.Index), server.PointConfig{}, v); err != nil {
			return fmt.Errorf("timeAndIntervals[%d]: %v", t.Index, err)
		}
	}
	return nil
}

// controlHandler carries out the controls of the fixture's outputs and reports
// what the contract's server events report. Its methods are called on the
// association's goroutine.
type controlHandler struct {
	fx  *fixture
	db  func() *server.Database
	sec *serverEvents
}

func functionName(op server.OperateType) string {
	switch op {
	case server.DirectOperate:
		return "direct-operate"
	case server.DirectOperateNoAck:
		return "direct-operate-no-ack"
	}
	return "operate"
}

// check decides a control: the status, and the value it sets.
func (h *controlHandler) check(c server.Command, direct bool) (app.CommandStatus, app.Value) {
	switch v := c.Value.(type) {
	case app.CROB:
		p := h.fx.find("binaryOutputs", uint16(c.Index))
		if c.GV != app.G12V1 || p == nil || c.Index > 65535 {
			return app.StatusNotSupported, nil
		}
		var on bool
		switch v.Code() {
		case 0x01, 0x02: // PULSE_ON, PULSE_OFF: only on a pulse output, which keeps its value
			if !p.Pulse {
				return app.StatusNotSupported, nil
			}
			if direct && p.SelectRequired {
				return app.StatusNotSupported, nil
			}
			return app.StatusSuccess, nil
		case 0x03, 0x41: // LATCH_ON; PULSE_ON with CLOSE
			on = true
		case 0x04, 0x81: // LATCH_OFF; PULSE_ON with TRIP
		default:
			return app.StatusNotSupported, nil
		}
		if direct && p.SelectRequired {
			return app.StatusNotSupported, nil
		}
		return app.StatusSuccess, app.Binary{Value: on}
	case app.AnalogOutput:
		p := h.fx.find("analogOutputs", uint16(c.Index))
		if p == nil || c.Index > 65535 {
			return app.StatusNotSupported, nil
		}
		if (p.Min != nil && v.Value < *p.Min) || (p.Max != nil && v.Value > *p.Max) {
			return app.StatusOutOfRange, nil
		}
		if direct && p.SelectRequired {
			return app.StatusNotSupported, nil
		}
		return app.StatusSuccess, app.Analog{Value: v.Value}
	}
	return app.StatusNotSupported, nil
}

func (h *controlHandler) commandEvent(function string, c server.Command, status app.CommandStatus) {
	h.sec.flush()
	switch v := c.Value.(type) {
	case app.CROB:
		emit(object{{"event", "command"}, {"function", function}, {"type", c.GV.String()}, {"index", c.Index},
			{"code", v.Code()}, {"count", v.Count}, {"onMs", v.OnTime}, {"offMs", v.OffTime},
			{"status", commandStatusName(uint8(status))}})
	case app.AnalogOutput:
		emit(object{{"event", "command"}, {"function", function}, {"type", c.GV.String()}, {"index", c.Index},
			{"value", number(v.Value)}, {"status", commandStatusName(uint8(status))}})
	}
}

func (h *controlHandler) Select(_ context.Context, c server.Command) app.CommandStatus {
	status, _ := h.check(c, false)
	h.commandEvent("select", c, status)
	return status
}

func (h *controlHandler) Operate(_ context.Context, c server.Command, op server.OperateType) app.CommandStatus {
	status, value := h.check(c, op != server.SelectBeforeOperate)
	if status == app.StatusSuccess {
		db := h.db()
		switch v := value.(type) {
		case app.Binary:
			p := h.fx.find("binaryOutputs", uint16(c.Index))
			_ = db.Update(server.BinaryOutput, c.Index, app.Binary{Value: v.Value, Flags: app.Flags(p.Flags)}, server.EventOnChange)
			if p.Target != nil {
				t := h.fx.find("binaryInputs", *p.Target)
				_ = db.Update(server.BinaryInput, uint32(t.Index), app.Binary{Value: v.Value, Flags: app.Flags(t.Flags)}, server.EventOnChange)
			}
		case app.Analog:
			p := h.fx.find("analogOutputs", uint16(c.Index))
			_ = db.Update(server.AnalogOutput, c.Index, app.Analog{Value: v.Value, Flags: app.Flags(p.Flags)}, server.EventOnChange)
			if p.Target != nil {
				t := h.fx.find("analogInputs", *p.Target)
				_ = db.Update(server.AnalogInput, uint32(t.Index), app.Analog{Value: v.Value, Flags: app.Flags(t.Flags)}, server.EventOnChange)
			}
		}
	}
	h.commandEvent(functionName(op), c, status)
	return status
}

// Restart (server.RestartHandler) accepts the restarts the fixture has a
// delay for; the library answers the others NO_FUNC_CODE_SUPPORT.
func (h *controlHandler) Restart(_ context.Context, cold bool) (time.Duration, error) {
	delay, kind := h.fx.WarmDelayMs, "warm"
	if cold {
		delay, kind = h.fx.ColdDelayMs, "cold"
	}
	if delay == nil {
		return 0, errors.New("the fixture has no " + kind + " restart")
	}
	h.sec.flush()
	emit(object{{"event", "restart"}, {"type", kind}})
	return time.Duration(*delay) * time.Millisecond, nil
}

// SetTime (server.TimeHandler) is told of every time synchronization.
func (h *controlHandler) SetTime(_ context.Context, t time.Time) {
	h.sec.flush()
	emit(object{{"event", "time-sync"}, {"time", formatTime(t)}})
}

// Application (server.ApplicationHandler) accepts the management functions
// 15..19. ACTIVATE_CONFIG (contract 1.5) is refused with NO_FUNC_CODE_SUPPORT:
// the handler can answer IIN bits only, not the g91v1 status.
func (h *controlHandler) Application(_ context.Context, fc app.FunctionCode, _ []app.Header) app.IIN {
	switch fc {
	case app.FuncInitializeData, app.FuncInitializeApplication, app.FuncStartApplication,
		app.FuncStopApplication, app.FuncSaveConfiguration:
		h.sec.flush()
		emit(object{{"event", "manage"}, {"function", uint8(fc)}})
		return 0
	}
	return app.IINNoFuncCodeSupport
}

// WriteAttributeSet (server.AttributeSetWriteHandler) reports an accepted
// write of a writable attribute.
func (h *controlHandler) WriteAttributeSet(_ context.Context, set uint16, variation uint8, a app.Attribute) error {
	h.sec.flush()
	emit(object{{"event", "attribute-write"}, {"set", set}, {"variation", variation},
		{"type", attributeTypeName(a.Type)}, {"value", attributeValue(a)}})
	return nil
}

func runServer(argv []string) int {
	flags := []string{"fixture", "bind-address", "port", "ready-file", "select-timeout-ms",
		"confirm-timeout-ms", "local-addr", "remote-addr"}
	flags = append(append(append(flags, serverTransportFlags...), saFlags...), statisticsFlags...)
	flags = append(flags, "file-user", "file-password", "outstations", "control-socket", "frozen-event-policy")
	a, err := parseArgs(argv, flags, []string{"link-confirms"})
	var port, selectTimeout, confirmTimeout, local, remote, count int64
	var tr transport
	var sec security
	if err == nil {
		tr, err = parseTransport(a, true)
	}
	if err == nil {
		sec, err = parseSecurity(a)
	}
	if err == nil {
		port, err = a.integer("port", 20000, 1, 65535)
	}
	if err == nil {
		selectTimeout, err = a.integer("select-timeout-ms", 5000, 1, 3600000)
	}
	if err == nil {
		confirmTimeout, err = a.integer("confirm-timeout-ms", 5000, 1, 3600000)
	}
	if err == nil {
		local, err = a.integer("local-addr", -1, 0, 65519)
	}
	if err == nil {
		remote, err = a.integer("remote-addr", -1, 0, 65519)
	}
	if err == nil {
		count, err = a.integer("outstations", 1, 1, 16)
	}
	// The library raises a frozen counter event when a freeze changes the
	// frozen value and has no setting for it (contract 1.4).
	if policy := a.str("frozen-event-policy", "on-change"); err == nil && policy != "on-change" {
		err = usagef("--frozen-event-policy: this outstation has only the policy on-change")
	}
	_, hasUser := a["file-user"]
	if _, hasPassword := a["file-password"]; err == nil && hasUser != hasPassword {
		err = usagef("--file-user and --file-password go together")
	}
	if err == nil && count > 1 && tr.connect != "" {
		err = usagef("--outstations does not go with --connect")
	}
	if err != nil {
		logf("server: %v", err)
		return exitUsage
	}
	path := a.str("fixture", defaultFixture)
	bind := a.str("bind-address", "0.0.0.0")
	readyFile := a.str("ready-file", defaultReadyFile)

	text, err := os.ReadFile(path)
	var fx *fixture
	if err == nil {
		fx, err = loadFixture(text)
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
	var files *fileServer
	if fx.Files != nil || fx.Directories != nil {
		files = newFileServer(fx)
	}
	if user, ok := a["file-user"]; ok {
		if files == nil {
			logf("server: --file-user needs a fixture with files")
			return exitUsage
		}
		files.requireAuth(user, a["file-password"])
	}

	var serverTLS *tls.Config
	if tr.tls {
		if tr.connect != "" {
			host, _, _ := net.SplitHostPort(tr.connect)
			serverTLS, err = tr.tlsClient(cmp.Or(tr.serverName, host))
		} else {
			serverTLS, err = tr.tlsServer()
		}
		if err != nil {
			logf("server: %v", err)
			return exitFailed
		}
	}

	limit := cmp.Or(fx.EventBuffer, 1000)
	limits := map[server.PointType]int{}
	for t := server.BinaryInput; t <= server.TimeAndInterval; t++ {
		limits[t] = limit
	}
	wire := &wireState{}
	servers := make([]*server.Server, count)
	for i := range servers {
		events := &serverEvents{wire: wire, lead: i == 0, dial: tr.connect != ""}
		h := &controlHandler{fx: fx, sec: events}
		opts := []server.Option{
			server.WithAddress(link.Address(local + int64(i))),
			server.WithMasters(link.Address(remote)),
			server.WithHandler(h),
			server.WithSelectTimeout(time.Duration(selectTimeout) * time.Millisecond),
			server.WithConfirmTimeout(time.Duration(confirmTimeout) * time.Millisecond),
			server.WithUnsolicited(server.Unsolicited{Enabled: fx.Unsolicited}),
			server.WithEventLimits(limits),
			// The outstation sends no keep-alives of its own.
			server.WithLink(server.Link{Confirmed: a.has("link-confirms"), KeepAlive: -1}),
			server.WithMetrics(events),
			server.WithLogger(libraryLog()),
		}
		if fx.MaxControls > 0 {
			opts = append(opts, server.WithMaxControls(fx.MaxControls))
		}
		if fx.NeedTime {
			// NEED_TIME from the start until a master writes the time, and
			// not again for the life of the process.
			opts = append(opts, server.WithTimeSync(50*365*24*time.Hour))
		}
		if files != nil {
			opts = append(opts, server.WithFileServer(files.forServer()))
		}
		if sec.on {
			opts = append(opts, server.WithSecureAuthentication(sec.server()))
		}
		if serverTLS != nil && tr.connect != "" {
			opts = append(opts, server.WithTLS(serverTLS))
		}
		opts = append(opts, attributeOptions(fx)...)
		srv, err := server.New(opts...)
		if err != nil {
			logf("server: %v", err)
			return exitFailed
		}
		if err := populate(srv.Database(), fx); err != nil {
			logf("server: fixture %s: go-dnp3 does not serve it: %v", path, err)
			return exitFixtureInvalid
		}
		h.db = srv.Database
		servers[i] = srv
	}
	var group *server.Group
	if count > 1 {
		if group, err = server.NewGroup(servers...); err != nil {
			logf("server: %v", err)
			return exitFailed
		}
	}
	closeAll := func() {
		if group != nil {
			_ = group.Close()
			return
		}
		_ = servers[0].Close()
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	done := make(chan error, 1)
	addr := net.JoinHostPort(bind, strconv.Itoa(int(port)))
	address := addr
	switch {
	case tr.connect != "":
		address = tr.connect
		go func() {
			err := servers[0].DialAndServe(ctx, tr.connect, server.Redial{MinDelay: 100 * time.Millisecond, MaxDelay: time.Second})
			done <- err
		}()
	case tr.kind == "serial":
		address = tr.serial.device
		f, err := openSerial(tr.serial)
		if err != nil {
			logf("server: cannot open the serial device %s: %v", tr.serial.device, err)
			return exitFailed
		}
		go func() {
			if group != nil {
				done <- group.ServeConn(f, serialAddr(tr.serial.device))
			} else {
				done <- servers[0].ServeConn(f, serialAddr(tr.serial.device))
			}
		}()
	case tr.kind == "udp":
		pc, err := net.ListenPacket("udp", addr)
		if err != nil {
			logf("server: cannot open the UDP socket %s: %v", addr, err)
			return exitFailed
		}
		var peer net.Addr
		if tr.udpPeer != "" {
			if peer, err = net.ResolveUDPAddr("udp", tr.udpPeer); err != nil {
				logf("server: --udp-peer %s: %v", tr.udpPeer, err)
				return exitFailed
			}
		}
		go func() {
			switch {
			case group != nil:
				done <- group.ServeUDP(pc)
			case peer != nil:
				done <- servers[0].ServeUDPTo(pc, peer)
			default:
				done <- servers[0].ServeUDP(pc)
			}
		}()
	default:
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			logf("server: cannot listen on %s: %v", addr, err)
			return exitFailed
		}
		var l net.Listener = &listener{Listener: ln}
		if serverTLS != nil {
			l = tls.NewListener(l, serverTLS)
		}
		go func() {
			if group != nil {
				done <- group.Serve(l)
			} else {
				done <- servers[0].Serve(l)
			}
		}()
	}

	// The control socket (contract 1.4), for a server of one outstation.
	if count == 1 {
		control := a.str("control-socket", defaultControlSocket)
		l, err := listenControl(control, servers[0].Database())
		if err != nil {
			logf("server: cannot open the control socket %s: %v", control, err)
			closeAll()
			return exitFailed
		}
		defer func() {
			_ = l.Close()
			_ = os.Remove(control)
		}()
	}
	if err := os.WriteFile(readyFile, nil, 0o644); err != nil {
		logf("server: cannot write the readiness file %s: %v", readyFile, err)
		closeAll()
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
		logf("server: the outstation stopped without a signal: %v", err)
	}
	_ = os.Remove(readyFile)
	closeAll()
	if !signalled {
		return exitFailed
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
	emit(object{{"event", "stopped"}})
	return exitOK
}
