// SPDX-License-Identifier: MIT

// The outstation's channel: frames from a stream (TCP, TLS, a serial line)
// or from UDP datagrams go to the associations they are addressed to, from
// their master. A new connection replaces the current one, as DNP3 over TCP
// has it (IEEE 1815-2012 annex: one master connection at a time).

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	dnp3 "github.com/cB-Sigma/dnp3"
)

type router struct {
	stations []*station

	mu      sync.Mutex
	closers []io.Closer
}

func (r *router) closeLater(c io.Closer) {
	r.mu.Lock()
	r.closers = append(r.closers, c)
	r.mu.Unlock()
}

func (r *router) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.closers {
		_ = c.Close()
	}
}

// incoming hands a frame to the outstations it is for.
func (r *router) incoming(f dnp3.Frame) {
	for _, s := range r.stations {
		if f.Src != s.master || !s.assoc.Accepts(f.Dest) {
			continue
		}
		if f.Primary() && f.Func() == dnp3.LinkRequestLinkStatus && f.Dest == s.addr {
			emit(object{{"event", "link-status"}})
		}
		s.sniff.in(f)
		s.assoc.Deliver(f)
	}
}

// outgoing sees a frame an outstation sends.
func (r *router) outgoing(f dnp3.Frame) {
	for _, s := range r.stations {
		if f.Src == s.addr {
			s.sniff.out(f)
			return
		}
	}
}

// attach makes l the link of every outstation.
func (r *router) attach(l dnp3.OutLink) {
	for _, s := range r.stations {
		s.assoc.Attach(l)
	}
}

// streamLink sends frames on a connection.
type streamLink struct {
	r    *router
	mu   sync.Mutex
	conn io.ReadWriteCloser
	once sync.Once
}

func (l *streamLink) WriteFrame(f dnp3.Frame) error {
	b, err := f.Encode()
	if err != nil {
		return err
	}
	l.mu.Lock()
	_, err = l.conn.Write(b)
	l.mu.Unlock()
	if err == nil {
		l.r.outgoing(f)
	}
	return err
}

func (l *streamLink) Close() error {
	var err error
	l.once.Do(func() { err = l.conn.Close() })
	return err
}

// serve runs one connection until it ends; events reports it (TCP, TLS).
func (r *router) serve(conn io.ReadWriteCloser, events bool) error {
	l := &streamLink{r: r, conn: conn}
	if events {
		emit(object{{"event", "connection-opened"}})
	}
	r.attach(l)
	var p dnp3.LinkParser
	err := readStream(conn, func(b []byte) {
		for _, f := range p.Feed(b) {
			r.incoming(f)
		}
	})
	for _, s := range r.stations {
		s.assoc.Detach(l)
	}
	_ = l.Close()
	if events {
		emit(object{{"event", "connection-closed"}})
	}
	return err
}

// datagramLink sends frames as UDP datagrams to the master's endpoint.
type datagramLink struct {
	r     *router
	pc    net.PacketConn
	mu    sync.Mutex
	fixed bool
	peer  net.Addr
}

func (l *datagramLink) WriteFrame(f dnp3.Frame) error {
	b, err := f.Encode()
	if err != nil {
		return err
	}
	l.mu.Lock()
	peer := l.peer
	l.mu.Unlock()
	if peer == nil {
		return nil // nobody to send to yet: lost, as a datagram can be
	}
	if _, err := l.pc.WriteTo(b, peer); err != nil {
		return err
	}
	l.r.outgoing(f)
	return nil
}

// Close keeps the socket: the outstation listens on it for its life.
func (l *datagramLink) Close() error { return nil }

func (l *datagramLink) heard(a net.Addr) {
	if l.fixed {
		return
	}
	l.mu.Lock()
	l.peer = a
	l.mu.Unlock()
}

// handshake runs the TLS handshake of a connection before it replaces the
// current one, so that a refused master does not end it.
func handshake(c *tls.Conn) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.HandshakeContext(ctx); err != nil {
		_ = c.Close()
		return err
	}
	return nil
}

// open opens the channel of the transport and serves it in the background;
// done receives why it ended. The address is what the ready event reports.
func (r *router) open(ctx context.Context, t transport, addr string, done chan<- error) (string, error) {
	switch {
	case t.connect != "":
		var tc *tls.Config
		if t.tls {
			host, _, _ := net.SplitHostPort(t.connect)
			var err error
			if tc, err = tlsClient(t, host); err != nil {
				return "", err
			}
		}
		go r.dial(ctx, t.connect, tc)
		return t.connect, nil
	case t.kind == "serial":
		p, err := openSerial(t.line)
		if err != nil {
			return "", err
		}
		r.closeLater(p)
		go func() { done <- r.serve(p, false) }()
		return t.line.device, nil
	case t.kind == "udp":
		pc, err := net.ListenPacket("udp", addr)
		if err != nil {
			return "", err
		}
		r.closeLater(pc)
		l := &datagramLink{r: r, pc: pc}
		if t.udpPeer != "" {
			peer, err := net.ResolveUDPAddr("udp", t.udpPeer)
			if err != nil {
				_ = pc.Close()
				return "", err
			}
			l.fixed, l.peer = true, peer
		}
		r.attach(l)
		go func() {
			var p dnp3.LinkParser
			buf := make([]byte, 65536)
			for {
				n, from, err := pc.ReadFrom(buf)
				if err != nil {
					done <- err
					return
				}
				l.heard(from)
				for _, f := range p.Feed(buf[:n]) {
					r.incoming(f)
				}
			}
		}()
		return addr, nil
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	r.closeLater(ln)
	var tc *tls.Config
	if t.tls {
		if tc, err = tlsServer(t); err != nil {
			_ = ln.Close()
			return "", err
		}
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				done <- err
				return
			}
			go func() {
				var conn io.ReadWriteCloser = c
				if tc != nil {
					s := tls.Server(c, tc)
					if err := handshake(s); err != nil {
						logf("server: TLS handshake with %s: %v", c.RemoteAddr(), err)
						return
					}
					conn = s
				}
				r.closeLater(conn)
				_ = r.serve(conn, true)
			}()
		}
	}()
	return addr, nil
}

// dial connects to the master (dual end point), and again after each
// connection ends.
func (r *router) dial(ctx context.Context, address string, tc *tls.Config) {
	for ctx.Err() == nil {
		d := net.Dialer{Timeout: 5 * time.Second}
		c, err := d.DialContext(ctx, "tcp", address)
		if err == nil {
			var conn io.ReadWriteCloser = c
			if tc != nil {
				s := tls.Client(c, tc)
				if err = handshake(s); err == nil {
					conn = s
				}
			}
			if err == nil {
				r.closeLater(conn)
				err = r.serve(conn, true)
			}
		}
		if err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.EOF) {
			logf("server: connection to %s: %v", address, err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
}
