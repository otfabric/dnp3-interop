// SPDX-License-Identifier: GPL-3.0-or-later

// Security statistics (contract 1.2): the outstation serves them as go-dnp3
// does with secure authentication on; the library's master drops groups 121
// and 122 (no handler knows the type), so the adapter reads them off the
// wire, from responses that carry nothing else.

package main

import (
	"context"
	"encoding/binary"
	"io"
	"time"

	"github.com/dscsystems/go-dnp3/channel"
)

var statisticsFlags = []string{"sa-statistics-class", "sa-statistics-threshold"}

// fragments reassembles the application fragments of a stream of transport
// segments.
type fragments struct {
	buf []byte
	on  bool
	fn  func(fragment []byte)
}

func (f *fragments) segment(s []byte) {
	if s[0]&0x40 != 0 { // FIR
		f.buf, f.on = f.buf[:0], true
	}
	if !f.on {
		return
	}
	f.buf = append(f.buf, s[1:]...)
	if s[0]&0x80 != 0 { // FIN
		f.on = false
		f.fn(append([]byte(nil), f.buf...))
	}
}

// statisticsChannel passes what the master receives to h.statistics.
type statisticsChannel struct {
	channel.Channel
	h *handler
}

func (s statisticsChannel) Connect(ctx context.Context) (io.ReadWriteCloser, error) {
	c, err := s.Channel.Connect(ctx)
	if err != nil {
		return nil, err
	}
	f := &fragments{fn: s.h.statistics}
	w := &statisticsRWC{ReadWriteCloser: c, in: frames{fn: f.segment}}
	if m, ok := c.(messageWriter); ok {
		return statisticsMessages{statisticsRWC: w, m: m}, nil
	}
	return w, nil
}

type statisticsRWC struct {
	io.ReadWriteCloser
	in frames
}

func (s *statisticsRWC) Read(p []byte) (int, error) {
	n, err := s.ReadWriteCloser.Read(p)
	s.in.feed(p[:n])
	return n, err
}

type statisticsMessages struct {
	*statisticsRWC
	m messageWriter
}

func (s statisticsMessages) BeginMessage()     { s.m.BeginMessage() }
func (s statisticsMessages) EndMessage() error { return s.m.EndMessage() }

// statistics reads g121v1 and g122v1/v2 objects from a response fragment.
// It stops at the first header of another group: it cannot know that
// group's object size.
func (h *handler) statistics(fragment []byte) {
	if len(fragment) < 4 || (fragment[1] != 0x81 && fragment[1] != 0x82) {
		return
	}
	unsolicited := fragment[1] == 0x82
	data := fragment[4:]
	var found []object
	for len(data) >= 3 {
		g, v, q := data[0], data[1], data[2]
		data = data[3:]
		if (g != 121 || v != 1) && (g != 122 || (v != 1 && v != 2)) {
			break
		}
		size := 7
		if g == 122 && v == 2 {
			size = 13
		}
		var indexes []uint16
		prefix := 0
		switch q & 0x0F {
		case 0x00, 0x01: // start-stop, 8 or 16 bits
			w := 1 << (q & 0x0F)
			if len(data) < 2*w {
				return
			}
			start, stop := uint16(data[0]), uint16(data[w])
			if w == 2 {
				start, stop = binary.LittleEndian.Uint16(data), binary.LittleEndian.Uint16(data[2:])
			}
			data = data[2*w:]
			for i := int(start); i <= int(stop); i++ {
				indexes = append(indexes, uint16(i))
			}
		case 0x07, 0x08: // count, 8 or 16 bits, with an index prefix
			w := int(q&0x0F) - 6
			if len(data) < w {
				return
			}
			count := int(data[0])
			if w == 2 {
				count = int(binary.LittleEndian.Uint16(data))
			}
			data = data[w:]
			prefix = int(q>>4) & 0x07 // 1: 8-bit index, 2: 16-bit index
			for range count {
				indexes = append(indexes, 0)
			}
		default:
			return
		}
		for k := range indexes {
			if prefix == 1 || prefix == 2 {
				if len(data) < prefix {
					return
				}
				indexes[k] = uint16(data[0])
				if prefix == 2 {
					indexes[k] = binary.LittleEndian.Uint16(data)
				}
				data = data[prefix:]
			}
			if len(data) < size {
				return
			}
			o := object{{"group", g}, {"variation", v}, {"index", indexes[k]},
				{"value", binary.LittleEndian.Uint32(data[3:7])}, {"flags", flagNames(g, data[0])},
				{"association", binary.LittleEndian.Uint16(data[1:3])}}
			if size == 13 {
				ms := uint64(data[7]) | uint64(data[8])<<8 | uint64(data[9])<<16 | uint64(data[10])<<24 |
					uint64(data[11])<<32 | uint64(data[12])<<40
				o = append(o, field{"time", formatTime(time.UnixMilli(int64(ms)).UTC())})
			}
			found = append(found, append(o, field{"unsolicited", unsolicited}))
			data = data[size:]
		}
	}
	h.mu.Lock()
	h.objects = append(h.objects, found...)
	h.mu.Unlock()
}
