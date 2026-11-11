// SPDX-License-Identifier: GPL-3.0-or-later

// Fuzz targets of what the adapter parses itself: the link frames it sniffs
// off the wire, fixtures and command lines. The seeds are the repository's
// fixtures and testdata/fuzz. Run with
//   go test -fuzz=FuzzFrames -fuzztime=30s
// (make fuzz runs every target of both Go adapters).

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// frame builds a link frame with dummy CRCs (frames does not check them) and
// user data split into blocks of 16 octets.
func frame(control byte, dest, src uint16, data []byte) []byte {
	out := []byte{0x05, 0x64, byte(5 + len(data)), control, byte(dest), byte(dest >> 8), byte(src), byte(src >> 8), 0, 0}
	for len(data) > 0 {
		k := min(16, len(data))
		out = append(out, data[:k]...)
		out = append(out, 0, 0)
		data = data[k:]
	}
	return out
}

func TestFramesReassemblesSplitInput(t *testing.T) {
	user := bytes.Repeat([]byte{0xC0, 0xC1, 0x81, 0x00, 0x00}, 9) // 45 octets: three blocks
	stream := append(append([]byte{0xFF, 0x05}, frame(0x44, 1024, 1, user)...), frame(0xC9, 1024, 1, nil)...)
	for split := range len(stream) {
		var got [][]byte
		var headers int
		f := &frames{fn: func(app []byte) { got = append(got, bytes.Clone(app)) }, link: func([]byte) { headers++ }}
		f.feed(stream[:split])
		f.feed(stream[split:])
		if headers != 2 || len(got) != 1 || !bytes.Equal(got[0], user) {
			t.Fatalf("split at %d: %d headers, user data %x", split, headers, got)
		}
	}
}

// FuzzFrames: any byte stream, fed in two parts, gives link headers of 10
// octets and user data no longer than a frame can carry, never a panic; and
// the buffer never holds more than the largest frame.
func FuzzFrames(f *testing.F) {
	f.Add(frame(0x44, 1024, 1, []byte{0xC0, 0xC1, 0x01, 0x3C, 0x01, 0x06}), 3)
	f.Add(frame(0xC9, 0xFFFF, 1, nil), 0)
	f.Add([]byte{0x05, 0x64, 0x04, 0x05, 0x64, 0xFF}, 2)
	f.Add(append([]byte{0x05}, frame(0x73, 1, 1024, bytes.Repeat([]byte{0xAA}, 250))...), 100)
	f.Fuzz(func(t *testing.T, stream []byte, split int) {
		f := &frames{
			fn: func(app []byte) {
				if len(app) == 0 || len(app) > 250 {
					t.Fatalf("user data of %d octets", len(app))
				}
			},
			link: func(header []byte) {
				if len(header) != 10 || header[0] != 0x05 || header[1] != 0x64 {
					t.Fatalf("link header %x", header)
				}
			},
		}
		if split < 0 || split > len(stream) {
			split = len(stream) / 2
		}
		f.feed(stream[:split])
		f.feed(stream[split:])
		// 10 header octets, 250 user octets and 16 CRCs: 292.
		if len(f.buf) >= 292 {
			t.Fatalf("%d octets left in the buffer", len(f.buf))
		}
	})
}

// FuzzSegments: the application octets of a first segment go to the
// function and transport segments of any length are taken without a panic,
// and so are the inbound and outbound sniffers that read them.
func FuzzSegments(f *testing.F) {
	f.Add([]byte{0xC0, 0xC1, 0x01, 0x3C, 0x01, 0x06})
	f.Add([]byte{0x40})
	f.Add([]byte{0xC0, 0xC0, 0x83, 0x00, 0x00, 120, 7, 0x5B, 1, 10, 0, 1, 0, 0, 0, 3, 0, 1, 0, 2})
	f.Fuzz(func(t *testing.T, segment []byte) {
		if len(segment) == 0 {
			return // frames passes user data of 1 octet or more
		}
		var app []byte
		first(func(a []byte) { app = a })(segment)
		if segment[0]&0x40 != 0 && !bytes.Equal(app, segment[1:]) {
			t.Fatalf("first segment %x gave %x", segment, app)
		}
		if segment[0]&0x40 == 0 && app != nil {
			t.Fatalf("a segment that is not first gave %x", app)
		}
		if outboundSilenced(segment[1:]) {
			outbound(segment[1:])
		}
	})
}

// outboundSilenced is true for the application octets outbound ignores,
// which is every input it can take without writing a document to stdout.
func outboundSilenced(app []byte) bool {
	return len(app) < 19 || app[1] != fcAuthResponse || app[4] != 120 || app[5] != 7 || app[6] != 0x5B
}

func TestFixturesLoad(t *testing.T) {
	paths, _ := filepath.Glob(filepath.Join("..", "..", "fixtures", "*", "fixture.json"))
	if len(paths) == 0 {
		t.Skip("no fixtures: not in a checkout of the repository")
	}
	for _, p := range paths {
		text, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := loadFixture(text); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}

// FuzzLoadFixture: any input is either a fixture or an error, never a panic.
func FuzzLoadFixture(f *testing.F) {
	paths, _ := filepath.Glob(filepath.Join("..", "..", "fixtures", "*", "fixture.json"))
	for _, p := range paths {
		if text, err := os.ReadFile(p); err == nil {
			f.Add(text)
		}
	}
	f.Fuzz(func(t *testing.T, text []byte) {
		fx, err := loadFixture(text)
		if (fx == nil) == (err == nil) {
			t.Fatalf("fixture %v and error %v", fx != nil, err)
		}
	})
}

// FuzzParseArgs: a command line parses, or is refused with a usage error.
func FuzzParseArgs(f *testing.F) {
	f.Add("--host\x00srv\x00--tls")
	f.Add("--port")
	f.Fuzz(func(t *testing.T, line string) {
		a, err := parseArgs(strings.Split(line, "\x00"), []string{"host", "port"}, []string{"tls"})
		if err != nil {
			if _, ok := err.(usageError); !ok {
				t.Fatalf("not a usage error: %T %v", err, err)
			}
			return
		}
		if v, ok := a["tls"]; ok && v != "true" {
			t.Fatalf("switch --tls is %q", v)
		}
	})
}
