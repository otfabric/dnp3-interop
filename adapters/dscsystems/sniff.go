// SPDX-License-Identifier: GPL-3.0-or-later

// What the library does not tell the application, read off the wire: the
// function code of the request being served (to tell GET_FILE_INFO from an
// open for reading) and the authentication errors the outstation sends.

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"sync/atomic"

	"github.com/dscsystems/go-dnp3/channel"
)

// frames scans a byte stream for DNP3 link frames and calls fn with the user
// data of every frame: the transport header, then the segment's application
// octets; and link, when set, with the control octet of every frame. CRCs are
// not checked: the library does that, and this only watches.
type frames struct {
	buf  []byte
	fn   func(app []byte)
	link func(control byte)
}

func (f *frames) feed(p []byte) {
	f.buf = append(f.buf, p...)
	for {
		i := bytes.Index(f.buf, []byte{0x05, 0x64})
		if i < 0 {
			if len(f.buf) > 0 && f.buf[len(f.buf)-1] == 0x05 {
				f.buf = f.buf[len(f.buf)-1:]
			} else {
				f.buf = f.buf[:0]
			}
			return
		}
		f.buf = f.buf[i:]
		if len(f.buf) < 10 {
			return
		}
		length := int(f.buf[2])
		if length < 5 {
			f.buf = f.buf[2:]
			continue
		}
		n := length - 5
		size := 10 + n + 2*((n+15)/16)
		if len(f.buf) < size {
			return
		}
		if f.link != nil {
			f.link(f.buf[3])
		}
		var data []byte
		for off := 10; off < size; {
			k := min(16, size-off-2)
			data = append(data, f.buf[off:off+k]...)
			off += k + 2
		}
		f.buf = f.buf[size:]
		if len(data) >= 1 {
			f.fn(data)
		}
	}
}

// requestFunction is the function code of the last request the outstation
// received.
var requestFunction atomic.Int32

const (
	fcGetFileInfo  = 28
	fcAuthResponse = 0x83
)

// first passes the application octets of first transport segments to fn.
func first(fn func(app []byte)) func(segment []byte) {
	return func(segment []byte) {
		if segment[0]&0x40 != 0 {
			fn(segment[1:])
		}
	}
}

// linkInbound reports a REQUEST_LINK_STATUS from the master (contract 1.3):
// a keep-alive, which the library answers without telling the application.
func linkInbound(control byte) {
	const prm, requestLinkStatus = 0x40, 9
	if control&prm != 0 && control&0x0F == requestLinkStatus {
		emit(object{{"event", "link-status"}})
	}
}

func inbound(app []byte) {
	if len(app) >= 2 {
		requestFunction.Store(int32(app[1]))
	}
}

// outbound reports an authentication error (g120v7 in an AUTH RESPONSE): the
// library counts them but does not tell the application.
func outbound(app []byte) {
	// control, function, IIN (2), then g120v7 with qualifier 0x5B: count (1),
	// size (2), sequence (4), user (2), association (2), error code (1).
	if len(app) < 19 || app[1] != fcAuthResponse || app[4] != 120 || app[5] != 7 || app[6] != 0x5B {
		return
	}
	emit(object{{"event", "auth-error"}, {"user", binary.LittleEndian.Uint16(app[14:16])}, {"code", app[18]}})
}

// sniffChannel watches every connection of a channel in both directions.
type sniffChannel struct{ channel.Channel }

// messageWriter is how the library sends one fragment as one UDP datagram; a
// wrapper must keep it.
type messageWriter interface {
	BeginMessage()
	EndMessage() error
}

func (s sniffChannel) Connect(ctx context.Context) (io.ReadWriteCloser, error) {
	c, err := s.Channel.Connect(ctx)
	if err != nil {
		return nil, err
	}
	w := &sniffRWC{ReadWriteCloser: c, in: frames{fn: first(inbound), link: linkInbound}, out: frames{fn: first(outbound)}}
	if m, ok := c.(messageWriter); ok {
		return sniffMessages{sniffRWC: w, m: m}, nil
	}
	return w, nil
}

type sniffRWC struct {
	io.ReadWriteCloser
	in, out frames
}

func (s *sniffRWC) Read(p []byte) (int, error) {
	n, err := s.ReadWriteCloser.Read(p)
	s.in.feed(p[:n])
	return n, err
}

func (s *sniffRWC) Write(p []byte) (int, error) {
	s.out.feed(p)
	return s.ReadWriteCloser.Write(p)
}

type sniffMessages struct {
	*sniffRWC
	m messageWriter
}

func (s sniffMessages) BeginMessage()     { s.m.BeginMessage() }
func (s sniffMessages) EndMessage() error { return s.m.EndMessage() }
