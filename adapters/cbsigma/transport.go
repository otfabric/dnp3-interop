// SPDX-License-Identifier: MIT

// Transports (contract 1.1 and 1.2): the flags, the TLS configurations and
// the serial line.

package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/goburrow/serial"
)

var (
	serialFlags     = []string{"serial-device", "baud", "data-bits", "parity", "stop-bits"}
	serverTransport = append([]string{"transport", "tls-cert", "tls-key", "tls-ca", "udp-peer", "connect", "tls-server-name"}, serialFlags...)
	clientTransport = append([]string{"transport", "tls-cert", "tls-key", "tls-ca", "tls-server-name", "udp-local-port", "bind-address"}, serialFlags...)
)

// serialLine is the configuration of a serial device.
type serialLine struct {
	device             string
	baud, data, stop   int
	parity, parityName string // the letter (N, E, O) and the flag's word
}

type transport struct {
	kind                      string // tcp, udp or serial
	tls                       bool
	cert, key, ca, serverName string
	udpPeer                   string
	udpLocalPort              int
	line                      serialLine
	connect                   string // server: the master to dial (dual end point)
	listen                    bool   // client: wait for the outstation
}

// name is the transport of the ready event.
func (t transport) name() string {
	if t.tls {
		return "tls"
	}
	return t.kind
}

func parseTransport(a args, server bool) (transport, error) {
	t := transport{kind: a.str("transport", "tcp")}
	if t.kind != "tcp" && t.kind != "udp" && t.kind != "serial" {
		return t, usagef("--transport must be tcp, udp or serial")
	}
	for _, n := range serialFlags {
		if a.has(n) && t.kind != "serial" {
			return t, usagef("--%s needs --transport serial", n)
		}
	}
	for _, n := range []string{"udp-peer", "udp-local-port"} {
		if a.has(n) && t.kind != "udp" {
			return t, usagef("--%s needs --transport udp", n)
		}
	}
	t.cert, t.key, t.ca = a.str("tls-cert", ""), a.str("tls-key", ""), a.str("tls-ca", "")
	t.serverName = a.str("tls-server-name", "")
	if server {
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
		t.udpLocalPort = int(port)
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
		if t.tls {
			return t, usagef("TLS does not go with --transport serial")
		}
		line, err := parseSerial(a)
		if err != nil {
			return t, err
		}
		t.line = line
	}
	return t, nil
}

func parseSerial(a args) (serialLine, error) {
	var l serialLine
	var err error
	if l.device, err = a.required("serial-device"); err != nil {
		return l, err
	}
	// The library's serial URLs take 50 to 921600 baud.
	baud, err := a.integer("baud", 9600, 50, 921600)
	if err != nil {
		return l, err
	}
	data, err := a.integer("data-bits", 8, 7, 8)
	if err != nil {
		return l, err
	}
	l.baud, l.data = int(baud), int(data)
	switch l.parityName = a.str("parity", "none"); l.parityName {
	case "none":
		l.parity = "N"
	case "even":
		l.parity = "E"
	case "odd":
		l.parity = "O"
	default:
		return l, usagef("--parity must be none, even or odd")
	}
	switch a.str("stop-bits", "1") {
	case "1":
		l.stop = 1
	case "2":
		l.stop = 2
	default:
		return l, usagef("--stop-bits must be 1 or 2")
	}
	return l, nil
}

// keyPair and caPool read the PEM files of the TLS flags.
func keyPair(t transport) ([]tls.Certificate, *x509.CertPool, error) {
	cert, err := tls.LoadX509KeyPair(t.cert, t.key)
	if err != nil {
		return nil, nil, fmt.Errorf("certificate %s, key %s: %w", t.cert, t.key, err)
	}
	pem, err := os.ReadFile(t.ca)
	if err != nil {
		return nil, nil, fmt.Errorf("CA %s: %w", t.ca, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, nil, fmt.Errorf("CA %s: no PEM certificate", t.ca)
	}
	return []tls.Certificate{cert}, pool, nil
}

// tlsServer is the configuration of the end that accepts: it demands a
// certificate signed by the CA.
func tlsServer(t transport) (*tls.Config, error) {
	certs, pool, err := keyPair(t)
	if err != nil {
		return nil, err
	}
	return &tls.Config{Certificates: certs, ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert,
		MinVersion: tls.VersionTLS12}, nil
}

// tlsClient is the configuration of the end that dials: it verifies the
// peer's certificate for name.
func tlsClient(t transport, name string) (*tls.Config, error) {
	certs, pool, err := keyPair(t)
	if err != nil {
		return nil, err
	}
	if t.serverName != "" {
		name = t.serverName
	}
	return &tls.Config{Certificates: certs, RootCAs: pool, ServerName: name, MinVersion: tls.VersionTLS12}, nil
}

// serialPort is a serial line opened with every setting of the contract
// (the library's own opener has 8 data bits and 1 stop bit only). Reads
// wait at most 200 ms, so that Close never waits long for a reader.
type serialPort struct {
	mu     sync.RWMutex
	p      serial.Port
	closed bool
}

func openSerial(l serialLine) (*serialPort, error) {
	p, err := serial.Open(&serial.Config{Address: l.device, BaudRate: l.baud, DataBits: l.data, StopBits: l.stop,
		Parity: l.parity, Timeout: 200 * time.Millisecond})
	if err != nil {
		return nil, err
	}
	return &serialPort{p: p}, nil
}

func (s *serialPort) Read(b []byte) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return 0, net.ErrClosed
	}
	n, err := s.p.Read(b)
	if errors.Is(err, serial.ErrTimeout) {
		return n, errSerialTimeout
	}
	return n, err
}

func (s *serialPort) Write(b []byte) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return 0, net.ErrClosed
	}
	return s.p.Write(b)
}

func (s *serialPort) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.p.Close()
}

// errSerialTimeout is a read that found nothing within its timeout; the
// library's serial connection keeps reading on an error that says
// "timeout".
var errSerialTimeout = errors.New("serial read timeout")

// readStream reads a stream until it fails, handing every chunk to f; the
// read timeouts of a serial line are not failures.
func readStream(r io.Reader, f func([]byte)) error {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			f(buf[:n])
		}
		switch {
		case errors.Is(err, errSerialTimeout):
		case err != nil:
			return err
		case n == 0:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// serialURL is the library's URL of a serial line; the opener replaces
// the library's (dnp3.OpenSerial) to apply the data and stop bits.
func serialURL(l serialLine) string {
	return "serial://" + l.device + "?baud=" + fmt.Sprint(l.baud) + "&parity=" + strings.ToLower(l.parity)
}
