// SPDX-License-Identifier: GPL-3.0-or-later

// Transports (contract 1.1): TCP, TLS, UDP and serial, on the library's
// channels.

package main

import (
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dscsystems/go-dnp3/channel"
)

var (
	serverTransportFlags = []string{"transport", "tls-cert", "tls-key", "tls-ca", "udp-peer",
		"serial-device", "baud", "data-bits", "parity", "stop-bits", "connect", "tls-server-name"}
	clientTransportFlags = []string{"transport", "tls-ca", "tls-cert", "tls-key", "tls-server-name",
		"udp-local-port", "serial-device", "baud", "data-bits", "parity", "stop-bits", "bind-address"}
	clientTransportSwitches = []string{"tls", "listen", "link-confirms"}
	serialFlags             = []string{"serial-device", "baud", "data-bits", "parity", "stop-bits"}
)

type transport struct {
	kind                      string // tcp, udp or serial
	tls                       bool
	cert, key, ca, serverName string
	udpPeer                   string
	udpLocalPort              int64
	serial                    channel.SerialConfig
	// Dual end point (contract 1.2): the outstation dials the master at
	// connect; the master listens.
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
		t.serial = channel.SerialConfig{Device: device, Baud: int(baud), DataBits: int(bits)}
		switch a.str("parity", "none") {
		case "none":
			t.serial.Parity = channel.ParityNone
		case "even":
			t.serial.Parity = channel.ParityEven
		case "odd":
			t.serial.Parity = channel.ParityOdd
		default:
			return t, usagef("--parity must be none, even or odd")
		}
		switch a.str("stop-bits", "1") {
		case "1":
			t.serial.StopBits = channel.StopBits1
		case "2":
			t.serial.StopBits = channel.StopBits2
		default:
			return t, usagef("--stop-bits must be 1 or 2")
		}
	}
	return t, nil
}

func (t transport) tlsConfig() channel.TLSConfig {
	return channel.TLSConfig{CertFile: t.cert, KeyFile: t.key, CAFile: t.ca, ServerName: t.serverName}
}

var clientRetry = channel.Retry{Min: 100 * time.Millisecond, Max: time.Second}

// clientChannel is the master's channel to host:port (or the serial device).
// The address is what a failure message names.
func clientChannel(t transport, host string, port int64) (channel.Channel, string, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(int(port)))
	switch {
	case t.listen && t.tls:
		ch, err := channel.TLSServer(addr, t.tlsConfig())
		return ch, addr, err
	case t.listen:
		return channel.TCPServer(addr), addr, nil
	case t.kind == "serial":
		return channel.SerialChannel(t.serial, clientRetry), t.serial.Device, nil
	case t.kind == "udp":
		return channel.UDPChannel(channel.UDPConfig{
			LocalAddr: ":" + strconv.Itoa(int(t.udpLocalPort)), RemoteAddr: addr}), addr, nil
	case t.tls:
		ch, err := channel.TLSClient(addr, t.tlsConfig(), clientRetry)
		return ch, addr, err
	}
	return channel.TCPClient(addr, clientRetry), addr, nil
}

// opened is a channel whose first connection was made before the session ran,
// so that readiness means the socket is bound or the device open. Later
// connections come from the channel itself.
type opened struct {
	channel.Channel
	mu    sync.Mutex
	first io.ReadWriteCloser
}

func (o *opened) Connect(ctx context.Context) (io.ReadWriteCloser, error) {
	o.mu.Lock()
	first := o.first
	o.first = nil
	o.mu.Unlock()
	if first != nil {
		return first, nil
	}
	return o.Channel.Connect(ctx)
}

// eventChannel reports the connections of a channel as server events.
type eventChannel struct{ channel.Channel }

func (e eventChannel) Connect(ctx context.Context) (io.ReadWriteCloser, error) {
	c, err := e.Channel.Connect(ctx)
	if err != nil {
		return nil, err
	}
	emit(object{{"event", "connection-opened"}})
	return &eventRWC{ReadWriteCloser: c}, nil
}

type eventRWC struct {
	io.ReadWriteCloser
	once sync.Once
}

func (c *eventRWC) Close() error {
	err := c.ReadWriteCloser.Close()
	c.once.Do(func() { emit(object{{"event", "connection-closed"}}) })
	return err
}

// serverChannel opens the outstation's channel. The address is what the ready
// event reports; listening says when the channel accepts connections, after
// the session started on it (the library's TLS server listens on its first
// Connect).
func serverChannel(t transport, bind string, port int64) (ch channel.Channel, address string, listening func() bool, err error) {
	addr := net.JoinHostPort(bind, strconv.Itoa(int(port)))
	now := func() bool { return true }
	switch {
	case t.connect != "" && t.tls:
		ch, err := channel.TLSClient(t.connect, t.tlsConfig(), clientRetry)
		if err != nil {
			return nil, "", nil, err
		}
		return eventChannel{ch}, t.connect, now, nil
	case t.connect != "":
		return eventChannel{channel.TCPClient(t.connect, clientRetry)}, t.connect, now, nil
	case t.kind == "serial":
		ch := channel.SerialChannel(t.serial, channel.Retry{Min: 100 * time.Millisecond, Max: time.Second})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c, err := ch.Connect(ctx)
		if err != nil {
			return nil, "", nil, err
		}
		return &opened{Channel: ch, first: c}, t.serial.Device, now, nil
	case t.kind == "udp":
		ch := udpOutstation{channel.UDPChannel(channel.UDPConfig{LocalAddr: addr, RemoteAddr: t.udpPeer})}
		c, err := ch.Connect(context.Background())
		if err != nil {
			return nil, "", nil, err
		}
		return &opened{Channel: ch, first: c}, addr, now, nil
	case t.tls:
		ch, err := channel.TLSServer(addr, t.tlsConfig())
		if err != nil {
			return nil, "", nil, err
		}
		return eventChannel{ch}, addr, func() bool { return channel.ServerAddr(ch) != nil }, nil
	}
	ln, err := newListener(addr)
	if err != nil {
		return nil, "", nil, err
	}
	return ln, addr, now, nil
}

// udpOutstation works around DSC go-dnp3 v0.8.1: an outstation without a
// configured peer fails its first unsolicited response ("no UDP peer known
// yet"), the session takes that for a lost connection and reconnects, and
// the readers of the abandoned sessions keep taking datagrams from the
// shared socket, so requests go missing. A datagram to nobody is dropped here
// instead, as UDP would lose it; the session carries on.
type udpOutstation struct{ channel.Channel }

func (u udpOutstation) Connect(ctx context.Context) (io.ReadWriteCloser, error) {
	c, err := u.Channel.Connect(ctx)
	if err != nil {
		return nil, err
	}
	w := &noPeer{ReadWriteCloser: c}
	if m, ok := c.(messageWriter); ok {
		return noPeerMessages{noPeer: w, m: m}, nil
	}
	return w, nil
}

func dropped(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no UDP peer known yet")
}

type noPeer struct{ io.ReadWriteCloser }

func (n *noPeer) Write(p []byte) (int, error) {
	k, err := n.ReadWriteCloser.Write(p)
	if dropped(err) {
		return len(p), nil
	}
	return k, err
}

type noPeerMessages struct {
	*noPeer
	m messageWriter
}

func (n noPeerMessages) BeginMessage() { n.m.BeginMessage() }

func (n noPeerMessages) EndMessage() error {
	if err := n.m.EndMessage(); !dropped(err) {
		return err
	}
	return nil
}
