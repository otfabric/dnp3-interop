// SPDX-License-Identifier: MIT

// Server events the library does not hand to an application, taken from
// its metrics callbacks (dnp3.Metrics): the link-status and broadcast
// events, the connections of a dialling outstation, and the outcome of a
// secure authentication challenge.

package main

import (
	"net"
	"sync"
	"time"

	dnp3 "github.com/otfabric/go-dnp3"
	"github.com/otfabric/go-dnp3/app"
	"github.com/otfabric/go-dnp3/link"
)

// wireState is what the frames of the shared channel say about the
// fragments they carry: the requests that came to a broadcast address, by
// sequence number and function code. The frames are read on the channel's
// goroutine, the fragments handled later on the association's, after
// frames that came behind them may have been read already.
type wireState struct {
	mu         sync.Mutex
	broadcasts []broadcastRequest
}

type broadcastRequest struct{ seq, function uint8 }

// take reports whether the fragment came to a broadcast address.
func (w *wireState) take(f *app.Fragment) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i, b := range w.broadcasts {
		if b.seq == f.Control.Seq && b.function == uint8(f.Function) {
			w.broadcasts = append(w.broadcasts[:i], w.broadcasts[i+1:]...)
			return true
		}
	}
	return false
}

// serverEvents is one outstation's dnp3.Metrics and dnp3.ServerMetrics.
type serverEvents struct {
	dnp3.NopMetrics
	wire *wireState
	// lead is the first outstation: the one that reports what the
	// channel's frames show.
	lead bool
	// dial: connections are reported here, not by the listener.
	dial bool

	mu sync.Mutex
	// critical is the function code of the last critical request
	// received, challenged is set when a challenge for it was sent, and
	// replied is the user of the reply received for it.
	critical   app.FunctionCode
	challenged bool
	replied    *uint16
}

func (m *serverEvents) OnConnect(net.Addr) {
	if m.dial && m.lead {
		emit(object{{"event", "connection-opened"}})
	}
}

func (m *serverEvents) OnDisconnect(net.Addr, error) {
	if m.dial && m.lead {
		emit(object{{"event", "connection-closed"}})
	}
}

func (m *serverEvents) OnFrameReceived(_ net.Addr, f *link.Frame, _ int) {
	if !m.lead {
		return
	}
	if f.Dir && f.Function == link.PrimaryRequestLinkStatus {
		emit(object{{"event", "link-status"}})
	}
	// The first segment of a request to a broadcast address: transport
	// header (FIR), application control, function code.
	if f.Dest.Broadcast() && len(f.Data) >= 3 && f.Data[0]&0x40 != 0 {
		m.wire.mu.Lock()
		m.wire.broadcasts = append(m.wire.broadcasts, broadcastRequest{seq: f.Data[1] & 0x0F, function: f.Data[2]})
		if len(m.wire.broadcasts) > 16 {
			m.wire.broadcasts = m.wire.broadcasts[1:]
		}
		m.wire.mu.Unlock()
	}
}

func (m *serverEvents) OnFragmentReceived(_ net.Addr, f *app.Fragment, _ int) {
	if m.lead && m.wire.take(f) {
		emit(object{{"event", "broadcast"}, {"function", uint8(f.Function)}})
	}
	switch f.Function {
	case app.FuncConfirm:
		return
	case app.FuncAuthRequest, app.FuncAuthRequestNoAck:
		for _, h := range f.Headers {
			if h.GV != app.G120V2 || len(h.Objects) == 0 {
				continue
			}
			if r, ok := h.Objects[0].Value.(app.AuthReply); ok {
				m.mu.Lock()
				if m.challenged {
					u := r.User
					m.replied = &u
				}
				m.mu.Unlock()
			}
		}
		return
	}
	// Another request: a reply before it that drew no error was
	// authenticated.
	m.flush()
	m.mu.Lock()
	m.critical, m.challenged = f.Function, false
	m.mu.Unlock()
}

func (m *serverEvents) OnFragmentSent(_ net.Addr, f *app.Fragment, _ int) {
	if f.Function != app.FuncAuthResponse {
		m.flush()
		return
	}
	for _, h := range f.Headers {
		if len(h.Objects) == 0 {
			continue
		}
		switch v := h.Objects[0].Value.(type) {
		case app.AuthChallenge:
			m.mu.Lock()
			m.challenged, m.replied = true, nil
			m.mu.Unlock()
		case app.AuthError:
			m.mu.Lock()
			m.challenged, m.replied = false, nil
			m.mu.Unlock()
			emit(object{{"event", "auth-error"}, {"user", v.User}, {"code", uint8(v.Code)}})
		}
	}
}

// flush reports a critical request whose challenge was answered and drew
// no authentication error: it is being executed.
func (m *serverEvents) flush() {
	m.mu.Lock()
	user, fc := m.replied, m.critical
	m.replied, m.challenged = nil, false
	m.mu.Unlock()
	if user != nil {
		emit(object{{"event", "auth"}, {"user", *user}, {"function", uint8(fc)}})
	}
}

func (m *serverEvents) OnHandled(net.Addr, app.FunctionCode, time.Duration) {}
func (m *serverEvents) OnEventsDiscarded(int, int)                          {}
func (m *serverEvents) OnUnsolicited(net.Addr, int, error)                  {}
