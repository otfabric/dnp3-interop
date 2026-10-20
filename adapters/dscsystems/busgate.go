// SPDX-License-Identifier: GPL-3.0-or-later

// server --outstations N on the library's multidrop bus (contract 1.3).
//
// The bus detaches every station when the connection drops and starts routing
// as soon as the next connection is up, while the station sessions are still
// on their way back: a request that arrives first is addressed to a station
// with no session and is lost. The gate holds the bus's first read of a new
// connection until every station has re-attached, so nothing arrives before
// there is a session to take it.

package main

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/dscsystems/go-dnp3/channel"
)

// gateTimeout bounds the wait for the stations: a station that does not come
// back must not hold up the others.
const gateTimeout = 2 * time.Second

// gate counts the stations attached to the current connection.
type gate struct {
	mu       sync.Mutex
	want     int
	attached int
	ready    chan struct{}
}

func newGate(stations int) *gate {
	return &gate{want: stations, ready: make(chan struct{})}
}

// connected starts a new connection: none of the stations is on it yet. It
// returns what the first read waits for.
func (g *gate) connected() <-chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.attached = 0
	g.ready = make(chan struct{})
	return g.ready
}

// attach counts a station that has connected to the current connection.
func (g *gate) attach() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.attached++
	if g.attached == g.want {
		close(g.ready)
	}
}

// gatedLine is the channel under the bus.
type gatedLine struct {
	channel.Channel
	g *gate
}

func (l gatedLine) Connect(ctx context.Context) (io.ReadWriteCloser, error) {
	c, err := l.Channel.Connect(ctx)
	if err != nil {
		return nil, err
	}
	gc := &gatedConn{ReadWriteCloser: c, ready: l.g.connected()}
	if m, ok := c.(messageWriter); ok {
		return gatedMessages{gatedConn: gc, m: m}, nil
	}
	return gc, nil
}

type gatedConn struct {
	io.ReadWriteCloser
	ready <-chan struct{}
	once  sync.Once
}

func (c *gatedConn) Read(p []byte) (int, error) {
	c.once.Do(func() {
		select {
		case <-c.ready:
		case <-time.After(gateTimeout):
		}
	})
	return c.ReadWriteCloser.Read(p)
}

// gatedMessages keeps the message framing of a UDP connection.
type gatedMessages struct {
	*gatedConn
	m messageWriter
}

func (g gatedMessages) BeginMessage()     { g.m.BeginMessage() }
func (g gatedMessages) EndMessage() error { return g.m.EndMessage() }

// gatedStation is the channel a station session runs on: it reports each
// connection to the gate.
type gatedStation struct {
	channel.Channel
	g *gate
}

func (s gatedStation) Connect(ctx context.Context) (io.ReadWriteCloser, error) {
	c, err := s.Channel.Connect(ctx)
	if err == nil {
		s.g.attach()
	}
	return c, err
}
