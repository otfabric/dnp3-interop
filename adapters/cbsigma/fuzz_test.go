// SPDX-License-Identifier: MIT

// Fuzz targets of what the adapter parses itself: fixtures and command lines.
// The seeds are the repository's fixtures and testdata/fuzz. Run with
//   go test -fuzz=FuzzLoadFixture -fuzztime=30s
// (make fuzz runs every target of both Go adapters).

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// addFixtureSeeds adds every fixture of the repository as a seed; outside a
// checkout there are none, and testdata/fuzz still has some.
func addFixtureSeeds(f *testing.F) {
	paths, _ := filepath.Glob(filepath.Join("..", "..", "fixtures", "*", "fixture.json"))
	for _, p := range paths {
		if text, err := os.ReadFile(p); err == nil {
			f.Add(text)
		}
	}
}

// TestFixturesLoad: every fixture of the repository is valid for the adapter,
// so the fuzz seeds start from inputs that get past the first checks.
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

// FuzzLoadFixture: any input is either a fixture or an error, never a panic,
// and a fixture that loads has a name and no two points of a type share an
// index.
func FuzzLoadFixture(f *testing.F) {
	addFixtureSeeds(f)
	f.Fuzz(func(t *testing.T, text []byte) {
		fx, err := loadFixture(text)
		if err != nil {
			if fx != nil {
				t.Fatalf("a fixture and an error: %v", err)
			}
			return
		}
		if fx.Name == "" {
			t.Fatal("a fixture without a name loaded")
		}
		for kind, points := range fx.Points {
			seen := map[uint16]bool{}
			for _, p := range points {
				if seen[p.Index] {
					t.Fatalf("%s: index %d twice", kind, p.Index)
				}
				seen[p.Index] = true
			}
		}
	})
}

// FuzzParseArgs: a command line parses into the flags and switches it was
// given, or is refused with a usage error.
func FuzzParseArgs(f *testing.F) {
	f.Add("--host\x00srv\x00--port\x0020000\x00--tls")
	f.Add("--host")
	f.Add("integrity-poll")
	f.Add("--unknown\x00x")
	f.Add("--port\x00--host")
	f.Fuzz(func(t *testing.T, line string) {
		argv := strings.Split(line, "\x00")
		a, err := parseArgs(argv, []string{"host", "port"}, []string{"tls"})
		if err != nil {
			if _, ok := err.(usageError); !ok {
				t.Fatalf("not a usage error: %T %v", err, err)
			}
			return
		}
		for name, value := range a {
			switch name {
			case "tls":
				if value != "true" {
					t.Fatalf("switch --tls is %q", value)
				}
			case "host", "port":
			default:
				t.Fatalf("parsed an unknown flag --%s", name)
			}
		}
	})
}
