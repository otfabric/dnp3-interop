// SPDX-License-Identifier: GPL-3.0-or-later

// The outstation: serves a fixture as docs/FIXTURES.md describes.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/dscsystems/go-dnp3"
	"github.com/dscsystems/go-dnp3/outstation"
)

func dnpClass(c int) dnp3.Class {
	switch c {
	case 1:
		return dnp3.Class1
	case 2:
		return dnp3.Class2
	case 3:
		return dnp3.Class3
	}
	return dnp3.ClassNone
}

var dnpTypes = map[string]dnp3.PointType{
	"binaryInputs":    dnp3.TypeBinary,
	"doubleBitInputs": dnp3.TypeDoubleBitBinary,
	"binaryOutputs":   dnp3.TypeBinaryOutputStatus,
	"counters":        dnp3.TypeCounter,
	"frozenCounters":  dnp3.TypeFrozenCounter,
	"analogInputs":    dnp3.TypeAnalog,
	"analogOutputs":   dnp3.TypeAnalogOutputStatus,
	"octetStrings":    dnp3.TypeOctetString,

	"frozenAnalogInputs": dnp3.TypeFrozenAnalog,
}

// servable checks what the library's outstation cannot do with a valid
// fixture: its database holds the indexes 0..n-1 of each type, and it asserts
// NEED_TIME until a master writes the time.
func servable(fx *fixture) error {
	for _, kind := range pointTypes {
		seen := map[uint16]bool{}
		for _, p := range fx.Points[kind] {
			seen[p.Index] = true
		}
		for i := range len(fx.Points[kind]) {
			if !seen[uint16(i)] {
				return fmt.Errorf("%s: the go-dnp3 database holds the indexes 0..n-1 of a type; index %d is missing", kind, i)
			}
		}
	}
	seen := map[uint16]bool{}
	for _, t := range fx.TimeIntervals {
		seen[t.Index] = true
	}
	for i := range len(fx.TimeIntervals) {
		if !seen[uint16(i)] {
			return fmt.Errorf("timeAndIntervals: the go-dnp3 database holds the indexes 0..n-1; index %d is missing", i)
		}
	}
	if !fx.NeedTime {
		return errors.New("outstation.needTime false: go-dnp3 asserts NEED_TIME until a master writes the time")
	}
	return nil
}

func ts(t time.Time) dnp3.Timestamp { return dnp3.Now(t) }

// configure gives every point its variations, then its initial value with no
// event class (so that it raises no event), then its class.
func configure(db *outstation.Database, fx *fixture) error {
	now := ts(time.Now())
	for _, kind := range pointTypes {
		for _, p := range fx.Points[kind] {
			cfg := outstation.PointConfig{StaticVariation: p.SVar, EventVariation: p.EVar}
			if kind == "octetStrings" {
				cfg.StaticVariation, cfg.EventVariation = 0, 0
			}
			if !db.Configure(dnpTypes[kind], p.Index, cfg) {
				return fmt.Errorf("%s[%d]: go-dnp3 refuses static variation %d / event variation %d", kind, p.Index, p.SVar, p.EVar)
			}
			flags := dnp3.Flags(p.Flags)
			switch kind {
			case "binaryInputs":
				db.UpdateBinary(p.Index, dnp3.Binary{Value: p.Value != 0, Flags: flags, Time: now})
			case "doubleBitInputs":
				db.UpdateDoubleBit(p.Index, dnp3.DoubleBitBinary{Value: dnp3.DoubleBit(p.Value), Flags: flags, Time: now})
			case "binaryOutputs":
				db.UpdateBinaryOutputStatus(p.Index, dnp3.BinaryOutputStatus{Value: p.Value != 0, Flags: flags, Time: now})
			case "counters":
				db.UpdateCounter(p.Index, dnp3.Counter{Value: uint32(p.Value), Flags: flags, Time: now})
			case "frozenCounters":
				db.UpdateFrozenCounter(p.Index, dnp3.FrozenCounter{Value: uint32(p.Value), Flags: flags, Time: now})
			case "analogInputs":
				db.UpdateAnalog(p.Index, dnp3.Analog{Value: p.Value, Flags: flags, Time: now})
			case "analogOutputs":
				db.UpdateAnalogOutputStatus(p.Index, dnp3.AnalogOutputStatus{Value: p.Value, Flags: flags, Time: now})
			case "octetStrings":
				db.UpdateOctetString(p.Index, dnp3.OctetString(p.Octets))
			case "frozenAnalogInputs":
				db.UpdateFrozenAnalog(p.Index, dnp3.Analog{Value: p.Value, Flags: flags, Time: now})
			}
			cfg.Class = dnpClass(p.Class)
			// Command events of an output: recorded by the library when a
			// control reaches the control handler.
			cfg.CommandEventClass = dnpClass(p.CmdClass)
			cfg.CommandEventVariation = p.CmdVar
			db.Configure(dnpTypes[kind], p.Index, cfg)
		}
	}
	for _, t := range fx.TimeIntervals {
		db.UpdateTimeAndInterval(t.Index, dnp3.TimeAndInterval{Time: ts(t.Time), Interval: t.Interval, Units: t.Units})
	}
	return nil
}

type application struct {
	fx *fixture
}

func (a *application) Now() time.Time { return time.Now() }

func (a *application) WriteAbsoluteTime(t time.Time) bool {
	emit(object{{"event", "time-sync"}, {"time", formatTime(t)}})
	return true
}

func (a *application) ColdRestart() time.Duration {
	emit(object{{"event", "restart"}, {"type", "cold"}})
	return time.Duration(*a.fx.ColdDelayMs) * time.Millisecond
}

func (a *application) WarmRestart() time.Duration {
	emit(object{{"event", "restart"}, {"type", "warm"}})
	return time.Duration(*a.fx.WarmDelayMs) * time.Millisecond
}

func (a *application) SupportsWriteTime() bool   { return true }
func (a *application) SupportsColdRestart() bool { return a.fx.ColdDelayMs != nil }
func (a *application) SupportsWarmRestart() bool { return a.fx.WarmDelayMs != nil }

type controls struct {
	fx      *fixture
	session *outstation.Session
}

func functionName(op outstation.OperateType) string {
	switch op {
	case outstation.OperateDirect:
		return "direct-operate"
	case outstation.OperateDirectNoAck:
		return "direct-operate-no-ack"
	}
	return "operate"
}

func (c *controls) checkCROB(index uint16, crob dnp3.ControlRelayOutputBlock, direct bool) (dnp3.CommandStatus, bool) {
	p := c.fx.find("binaryOutputs", index)
	if p == nil {
		return dnp3.CommandNotSupported, false
	}
	var value bool
	switch uint8(crob.Code) {
	case 0x03, 0x41: // LATCH_ON; PULSE_ON with CLOSE
		value = true
	case 0x04, 0x81: // LATCH_OFF; PULSE_ON with TRIP
		value = false
	default:
		return dnp3.CommandNotSupported, false
	}
	if direct && p.SelectRequired {
		return dnp3.CommandNotSupported, false
	}
	return dnp3.CommandSuccess, value
}

func crobEvent(function string, index uint16, crob dnp3.ControlRelayOutputBlock, status dnp3.CommandStatus) {
	emit(object{{"event", "command"}, {"function", function}, {"type", "g12v1"}, {"index", index},
		{"code", uint8(crob.Code)}, {"count", crob.Count}, {"onMs", crob.OnTime}, {"offMs", crob.OffTime},
		{"status", commandStatusName(uint8(status))}})
}

func (c *controls) SelectCROB(index uint16, crob dnp3.ControlRelayOutputBlock) dnp3.CommandStatus {
	status, _ := c.checkCROB(index, crob, false)
	crobEvent("select", index, crob, status)
	return status
}

// OperateCROB runs on the session goroutine, which owns the database.
func (c *controls) OperateCROB(index uint16, crob dnp3.ControlRelayOutputBlock, op outstation.OperateType) dnp3.CommandStatus {
	status, value := c.checkCROB(index, crob, op != outstation.OperateSelected)
	if status == dnp3.CommandSuccess {
		p := c.fx.find("binaryOutputs", index)
		now := ts(time.Now())
		db := c.session.Database()
		db.UpdateBinaryOutputStatus(index, dnp3.BinaryOutputStatus{Value: value, Flags: dnp3.Flags(p.Flags), Time: now})
		if p.Target != nil {
			t := c.fx.find("binaryInputs", *p.Target)
			db.UpdateBinary(t.Index, dnp3.Binary{Value: value, Flags: dnp3.Flags(t.Flags), Time: now})
		}
	}
	crobEvent(functionName(op), index, crob, status)
	return status
}

func (c *controls) checkAnalog(index uint16, value float64, direct bool) dnp3.CommandStatus {
	p := c.fx.find("analogOutputs", index)
	if p == nil {
		return dnp3.CommandNotSupported
	}
	if (p.Min != nil && value < *p.Min) || (p.Max != nil && value > *p.Max) {
		return dnp3.CommandOutOfRange
	}
	if direct && p.SelectRequired {
		return dnp3.CommandNotSupported
	}
	return dnp3.CommandSuccess
}

func analogEvent(function string, index uint16, v outstation.AnalogOutput, status dnp3.CommandStatus) {
	emit(object{{"event", "command"}, {"function", function}, {"type", "g41v" + strconv.Itoa(int(v.Variation))},
		{"index", index}, {"value", number(v.Value)}, {"status", commandStatusName(uint8(status))}})
}

func (c *controls) SelectAnalog(index uint16, v outstation.AnalogOutput) dnp3.CommandStatus {
	status := c.checkAnalog(index, v.Value, false)
	analogEvent("select", index, v, status)
	return status
}

func (c *controls) OperateAnalog(index uint16, v outstation.AnalogOutput, op outstation.OperateType) dnp3.CommandStatus {
	status := c.checkAnalog(index, v.Value, op != outstation.OperateSelected)
	if status == dnp3.CommandSuccess {
		p := c.fx.find("analogOutputs", index)
		now := ts(time.Now())
		db := c.session.Database()
		db.UpdateAnalogOutputStatus(index, dnp3.AnalogOutputStatus{Value: v.Value, Flags: dnp3.Flags(p.Flags), Time: now})
		if p.Target != nil {
			t := c.fx.find("analogInputs", *p.Target)
			db.UpdateAnalog(t.Index, dnp3.Analog{Value: v.Value, Flags: dnp3.Flags(t.Flags), Time: now})
		}
	}
	analogEvent(functionName(op), index, v, status)
	return status
}

// listener is the outstation's TCP channel. It listens from the start (so
// that readiness means the port is open and a busy port fails the server),
// reports connections as events, and lets a new connection replace the
// current one: the library serves one connection at a time and would leave a
// second one waiting.
type listener struct {
	ln    net.Listener
	conns chan net.Conn

	mu     sync.Mutex
	active *eventConn
}

func newListener(addr string) (*listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	l := &listener{ln: ln, conns: make(chan net.Conn)}
	go l.accept()
	return l, nil
}

func (l *listener) accept() {
	for {
		c, err := l.ln.Accept()
		if err != nil {
			close(l.conns)
			return
		}
		l.mu.Lock()
		old := l.active
		l.mu.Unlock()
		if old != nil {
			_ = old.Close()
		}
		l.conns <- c
	}
}

func (l *listener) Connect(ctx context.Context) (io.ReadWriteCloser, error) {
	select {
	case c, ok := <-l.conns:
		if !ok {
			return nil, net.ErrClosed
		}
		ec := &eventConn{Conn: c}
		l.mu.Lock()
		l.active = ec
		l.mu.Unlock()
		emit(object{{"event", "connection-opened"}})
		return ec, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (l *listener) Close() error   { return l.ln.Close() }
func (l *listener) String() string { return "tcp-server " + l.ln.Addr().String() }

type eventConn struct {
	net.Conn
	once sync.Once
}

func (c *eventConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { emit(object{{"event", "connection-closed"}}) })
	return err
}

func runServer(argv []string) int {
	flags := []string{"fixture", "bind-address", "port", "ready-file", "select-timeout-ms",
		"confirm-timeout-ms", "local-addr", "remote-addr"}
	flags = append(append(append(flags, serverTransportFlags...), saFlags...), statisticsFlags...)
	flags = append(flags, "file-user", "file-password")
	a, err := parseArgs(argv, flags, nil)
	var port, selectTimeout, confirmTimeout, local, remote int64
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
	_, hasUser := a["file-user"]
	if _, hasPassword := a["file-password"]; err == nil && hasUser != hasPassword {
		err = usagef("--file-user and --file-password go together")
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

	counts := outstation.DatabaseConfig{
		Binary:             len(fx.Points["binaryInputs"]),
		DoubleBitBinary:    len(fx.Points["doubleBitInputs"]),
		BinaryOutputStatus: len(fx.Points["binaryOutputs"]),
		Counter:            len(fx.Points["counters"]),
		FrozenCounter:      len(fx.Points["frozenCounters"]),
		Analog:             len(fx.Points["analogInputs"]),
		AnalogOutputStatus: len(fx.Points["analogOutputs"]),
		OctetString:        len(fx.Points["octetStrings"]),
		FrozenAnalog:       len(fx.Points["frozenAnalogInputs"]),
		TimeAndInterval:    len(fx.TimeIntervals),
	}
	var files outstation.FileConfig
	if fx.Files != nil || fx.Directories != nil {
		root, err := fileTree(fx)
		var h *outstation.DirFileHandler
		if err == nil {
			h, err = outstation.OpenDir(root)
		}
		if err != nil {
			logf("server: cannot set up the files of the fixture: %v", err)
			return exitFailed
		}
		files.Handler = fileEvents{h}
	}
	if user, ok := a["file-user"]; ok {
		if files.Handler == nil {
			logf("server: --file-user needs a fixture with files")
			return exitUsage
		}
		files.Authenticate = fileAuthenticator(user, a["file-password"])
	}
	ctrl := &controls{fx: fx}
	config := outstation.Config{
		LocalAddr:      uint16(local),
		RemoteAddr:     uint16(remote),
		Database:       counts,
		Events:         outstation.EventBufferConfig{MaxEvents: 1000},
		ConfirmTimeout: time.Duration(confirmTimeout) * time.Millisecond,
		SelectTimeout:  time.Duration(selectTimeout) * time.Millisecond,

		MaxControlsPerRequest: fx.MaxControls,
		Unsolicited:           outstation.UnsolicitedConfig{Enabled: fx.Unsolicited},
		Files:                 files,
		Log:                   libraryLog(),

		SecureAuthentication: sec.outstation(),
	}
	attributeConfig(fx, &config)
	session := outstation.New(config, &application{fx: fx}, ctrl)
	ctrl.session = session
	if err := configure(session.Database(), fx); err != nil {
		logf("server: fixture %s: %v", path, err)
		return exitFixtureInvalid
	}

	ch, address, listening, err := serverChannel(tr, bind, port)
	if err != nil {
		logf("server: cannot open the %s channel on %s: %v", tr.name(), net.JoinHostPort(bind, strconv.Itoa(int(port))), err)
		return exitFailed
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- session.Run(ctx, sniffChannel{ch}) }()
	for deadline := time.Now().Add(5 * time.Second); !listening(); time.Sleep(10 * time.Millisecond) {
		select {
		case err := <-done:
			logf("server: cannot listen on %s: %v", address, err)
			return exitFailed
		default:
		}
		if time.Now().After(deadline) {
			logf("server: not listening on %s after 5 s", address)
			return exitFailed
		}
	}
	if err := os.WriteFile(readyFile, nil, 0o644); err != nil {
		logf("server: cannot write the readiness file %s: %v", readyFile, err)
		return exitFailed
	}
	emit(object{{"event", "ready"}, {"adapter", adapter}, {"address", address}, {"fixture", fx.Name},
		{"linkAddress", local}, {"masterAddress", remote}, {"transport", tr.name()}, {"dial", tr.connect != ""}})

	select {
	case <-ctx.Done():
		logf("server: signal, stopping")
	case err := <-done:
		logf("server: the session ended: %v", err)
	}
	_ = os.Remove(readyFile)
	_ = ch.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
	emit(object{{"event", "stopped"}})
	return exitOK
}
