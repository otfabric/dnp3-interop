// SPDX-License-Identifier: MIT

// Transports (contract 1.1): TCP, TLS, UDP and serial; the dual end point
// (1.2).

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"syscall"
	"time"
)

var (
	serverTransportFlags = []string{"transport", "tls-cert", "tls-key", "tls-ca", "udp-peer",
		"serial-device", "baud", "data-bits", "parity", "stop-bits", "connect", "tls-server-name"}
	clientTransportFlags = []string{"transport", "tls-ca", "tls-cert", "tls-key", "tls-server-name",
		"udp-local-port", "serial-device", "baud", "data-bits", "parity", "stop-bits", "bind-address"}
	clientTransportSwitches = []string{"tls", "listen", "link-confirms"}
	serialFlags             = []string{"serial-device", "baud", "data-bits", "parity", "stop-bits"}
)

// serialConfig is a serial line: no flow control.
type serialConfig struct {
	device   string
	baud     int
	dataBits int
	parity   string // none, even or odd
	stopBits int
}

type transport struct {
	kind                      string // tcp, udp or serial
	tls                       bool
	cert, key, ca, serverName string
	udpPeer                   string
	udpLocalPort              int64
	serial                    serialConfig
	// Dual end point: the outstation dials the master at connect; the
	// master listens.
	connect string
	listen  bool
}

// name is what the ready event reports.
func (t transport) name() string {
	if t.tls {
		return "tls"
	}
	return t.kind
}

func parseTransport(a args, server bool) (transport, error) {
	t := transport{kind: a.str("transport", "tcp")}
	switch t.kind {
	case "tcp", "udp", "serial":
	default:
		return t, usagef("--transport must be tcp, udp or serial")
	}
	only := func(kind string, names ...string) error {
		for _, n := range names {
			if a.has(n) && t.kind != kind {
				return usagef("--%s needs --transport %s", n, kind)
			}
		}
		return nil
	}
	if err := only("serial", serialFlags...); err != nil {
		return t, err
	}
	if err := only("udp", "udp-peer", "udp-local-port"); err != nil {
		return t, err
	}

	if server {
		t.cert, t.key, t.ca = a.str("tls-cert", ""), a.str("tls-key", ""), a.str("tls-ca", "")
		if a.has("connect") {
			t.connect = a.str("connect", "")
			if _, _, err := net.SplitHostPort(t.connect); err != nil {
				return t, usagef("--connect must be HOST:PORT")
			}
			for _, n := range []string{"bind-address", "port"} {
				if a.has(n) {
					return t, usagef("--%s does not go with --connect: the outstation dials", n)
				}
			}
			if t.kind != "tcp" {
				return t, usagef("--connect needs --transport tcp")
			}
			t.serverName = a.str("tls-server-name", "")
		} else if a.has("tls-server-name") {
			return t, usagef("--tls-server-name needs --connect")
		}
		t.tls = a.has("tls-cert")
		if !t.tls && (a.has("tls-key") || a.has("tls-ca")) {
			return t, usagef("--tls-key and --tls-ca need --tls-cert")
		}
		if t.tls && (t.key == "" || t.ca == "") {
			return t, usagef("--tls-cert needs --tls-key and --tls-ca: TLS is mutual")
		}
		if a.has("udp-peer") {
			t.udpPeer = a.str("udp-peer", "")
			if _, _, err := net.SplitHostPort(t.udpPeer); err != nil {
				return t, usagef("--udp-peer must be HOST:PORT")
			}
		}
	} else {
		t.tls = a.has("tls")
		t.listen = a.has("listen")
		if t.listen && t.kind != "tcp" {
			return t, usagef("--listen needs --transport tcp")
		}
		if t.listen && a.has("host") {
			return t, usagef("--host does not go with --listen: the master waits for the outstation")
		}
		if !t.listen && a.has("bind-address") {
			return t, usagef("--bind-address needs --listen")
		}
		t.cert, t.key, t.ca = a.str("tls-cert", ""), a.str("tls-key", ""), a.str("tls-ca", "")
		t.serverName = a.str("tls-server-name", "")
		if !t.tls && (a.has("tls-cert") || a.has("tls-key") || a.has("tls-ca") || a.has("tls-server-name")) {
			return t, usagef("the --tls-* flags need --tls")
		}
		if t.tls && (t.cert == "" || t.key == "" || t.ca == "") {
			return t, usagef("--tls needs --tls-ca, --tls-cert and --tls-key: TLS is mutual")
		}
		port, err := a.integer("udp-local-port", 20000, 0, 65535)
		if err != nil {
			return t, err
		}
		t.udpLocalPort = port
	}
	if t.tls && t.kind != "tcp" {
		return t, usagef("TLS needs --transport tcp")
	}

	if t.kind == "serial" {
		for _, n := range []string{"host", "port", "bind-address"} {
			if a.has(n) {
				return t, usagef("--%s does not go with --transport serial", n)
			}
		}
		device, err := a.required("serial-device")
		if err != nil {
			return t, err
		}
		baud, err := a.integer("baud", 9600, 50, 4000000)
		if err != nil {
			return t, err
		}
		bits, err := a.integer("data-bits", 8, 7, 8)
		if err != nil {
			return t, err
		}
		t.serial = serialConfig{device: device, baud: int(baud), dataBits: int(bits), parity: a.str("parity", "none")}
		switch t.serial.parity {
		case "none", "even", "odd":
		default:
			return t, usagef("--parity must be none, even or odd")
		}
		switch a.str("stop-bits", "1") {
		case "1":
			t.serial.stopBits = 1
		case "2":
			t.serial.stopBits = 2
		default:
			return t, usagef("--stop-bits must be 1 or 2")
		}
	}
	return t, nil
}

// tlsFiles loads the certificate, its key and the CA of the peer.
func (t transport) tlsFiles() (tls.Certificate, *x509.CertPool, error) {
	cert, err := tls.LoadX509KeyPair(t.cert, t.key)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("TLS certificate: %w", err)
	}
	pem, err := os.ReadFile(t.ca)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("TLS CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return tls.Certificate{}, nil, fmt.Errorf("TLS CA: no certificate in %s", t.ca)
	}
	return cert, pool, nil
}

// tlsServer is the configuration of the end that accepts the TLS
// connection: it demands the peer's certificate.
func (t transport) tlsServer() (*tls.Config, error) {
	cert, pool, err := t.tlsFiles()
	if err != nil {
		return nil, err
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, ClientCAs: pool,
		ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12}, nil
}

// tlsClient is the configuration of the end that dials; serverName is the
// name the peer's certificate must carry.
func (t transport) tlsClient(serverName string) (*tls.Config, error) {
	cert, pool, err := t.tlsFiles()
	if err != nil {
		return nil, err
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, RootCAs: pool, ServerName: serverName,
		MinVersion: tls.VersionTLS12}, nil
}

// listener is the outstation's TCP listener. It reports connections as
// events and lets a new connection replace the current one: the outstation
// has one master connection at a time.
type listener struct {
	net.Listener
	mu     sync.Mutex
	active *eventConn
}

func (l *listener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	ec := &eventConn{Conn: c}
	l.mu.Lock()
	old := l.active
	l.active = ec
	l.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	emit(object{{"event", "connection-opened"}})
	return ec, nil
}

// eventConn reports the end of a connection, once.
type eventConn struct {
	net.Conn
	once sync.Once
}

func (c *eventConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { emit(object{{"event", "connection-closed"}}) })
	return err
}

// udpConn is a UDP socket of the master: bound to its local port and
// connected to the outstation. An ICMP port unreachable while the
// outstation is not there makes the next read fail with ECONNREFUSED; that
// datagram is lost, as on a network, and the response timeout does the
// rest.
type udpConn struct{ *net.UDPConn }

func refused(err error) bool { return errors.Is(err, syscall.ECONNREFUSED) }

func (u udpConn) Read(b []byte) (int, error) {
	for {
		n, err := u.UDPConn.Read(b)
		if err == nil || !refused(err) {
			return n, err
		}
		time.Sleep(time.Millisecond)
	}
}

func (u udpConn) Write(b []byte) (int, error) {
	n, err := u.UDPConn.Write(b)
	if err != nil && refused(err) {
		return len(b), nil
	}
	return n, err
}

// clientStream opens the master's channel: it dials (TCP, TLS), opens the
// UDP socket or the serial device, or accepts the outstation's connection.
// It returns the stream and what a failure message names.
type clientStream struct {
	t              transport
	host           string
	port           int64
	connectTimeout time.Duration
	tlsConfig      *tls.Config
	ln             net.Listener // --listen
	mu             sync.Mutex
	opened         bool
	onOpen         func()
	addr           string
}

// setup does what can fail before the connection: certificates, the
// listening socket. A failure is `failed`, with the complete result
// document.
func (s *clientStream) setup() error {
	s.addr = net.JoinHostPort(s.host, strconv.Itoa(int(s.port)))
	if s.t.kind == "serial" {
		s.addr = s.t.serial.device
	}
	var err error
	if s.t.tls {
		if s.t.listen {
			s.tlsConfig, err = s.t.tlsServer()
		} else {
			name := s.t.serverName
			if name == "" {
				name = s.host
			}
			s.tlsConfig, err = s.t.tlsClient(name)
		}
		if err != nil {
			return err
		}
	}
	if s.t.listen {
		l, err := net.Listen("tcp", s.addr)
		if err != nil {
			return err
		}
		s.ln = l
		if s.tlsConfig != nil {
			s.ln = tls.NewListener(l, s.tlsConfig)
		}
	}
	return nil
}

func (s *clientStream) close() {
	if s.ln != nil {
		_ = s.ln.Close()
	}
}

// open is the client's StreamOpener.
func (s *clientStream) open(ctx context.Context) (io.ReadWriteCloser, net.Addr, error) {
	ctx, cancel := context.WithTimeout(ctx, s.connectTimeout)
	defer cancel()
	rw, remote, err := s.openStream(ctx)
	if err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	s.opened = true
	s.mu.Unlock()
	if s.onOpen != nil {
		s.onOpen()
	}
	return rw, remote, nil
}

func (s *clientStream) openStream(ctx context.Context) (io.ReadWriteCloser, net.Addr, error) {
	switch {
	case s.t.listen:
		type accepted struct {
			conn net.Conn
			err  error
		}
		done := make(chan accepted, 1)
		go func() {
			c, err := s.ln.Accept()
			if err == nil {
				if tc, ok := c.(*tls.Conn); ok {
					if err = tc.HandshakeContext(ctx); err != nil {
						_ = c.Close()
					}
				}
			}
			done <- accepted{c, err}
		}()
		select {
		case a := <-done:
			if a.err != nil {
				return nil, nil, a.err
			}
			return a.conn, a.conn.RemoteAddr(), nil
		case <-ctx.Done():
			go func() {
				if a := <-done; a.err == nil {
					_ = a.conn.Close()
				}
			}()
			return nil, nil, ctx.Err()
		}
	case s.t.kind == "serial":
		return openSerialUntil(ctx, s.t.serial)
	case s.t.kind == "udp":
		raddr, err := net.DefaultResolver.LookupNetIP(ctx, "ip", s.host)
		if err != nil || len(raddr) == 0 {
			return nil, nil, fmt.Errorf("cannot resolve %s: %v", s.host, err)
		}
		remote := &net.UDPAddr{IP: raddr[0].AsSlice(), Port: int(s.port)}
		local := &net.UDPAddr{Port: int(s.t.udpLocalPort)}
		c, err := net.DialUDP("udp", local, remote)
		if err != nil {
			return nil, nil, err
		}
		return udpConn{c}, remote, nil
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", s.addr)
	if err != nil {
		return nil, nil, err
	}
	if s.tlsConfig != nil {
		tc := tls.Client(c, s.tlsConfig)
		if err := tc.HandshakeContext(ctx); err != nil {
			_ = c.Close()
			return nil, nil, fmt.Errorf("TLS handshake: %w", err)
		}
		c = tc
	}
	return c, c.RemoteAddr(), nil
}

func (s *clientStream) wasOpened() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opened
}

// openSerialUntil opens the device, trying again until ctx ends: a
// pseudo-terminal bridged by socat may appear a moment late.
func openSerialUntil(ctx context.Context, cfg serialConfig) (io.ReadWriteCloser, net.Addr, error) {
	for {
		f, err := openSerial(cfg)
		if err == nil {
			return f, serialAddr(cfg.device), nil
		}
		select {
		case <-ctx.Done():
			return nil, nil, err
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// serialAddr names a serial device as a net.Addr.
type serialAddr string

func (serialAddr) Network() string  { return "serial" }
func (a serialAddr) String() string { return string(a) }
