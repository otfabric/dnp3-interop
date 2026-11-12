// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"math"
	"testing"
)

func TestCheckedConversions(t *testing.T) {
	for _, c := range []struct {
		name string
		f    func(int64) (any, error)
		ok   []int64
		bad  []int64
	}{
		{"toUint8", func(n int64) (any, error) { return toUint8(n) }, []int64{0, 255}, []int64{-1, 256}},
		{"toUint16", func(n int64) (any, error) { return toUint16(n) }, []int64{0, 65535}, []int64{-1, 65536}},
		{"toUint32", func(n int64) (any, error) { return toUint32(n) }, []int64{0, math.MaxUint32}, []int64{-1, math.MaxUint32 + 1}},
		{"toInt32", func(n int64) (any, error) { return toInt32(n) }, []int64{math.MinInt32, math.MaxInt32}, []int64{math.MinInt32 - 1, math.MaxInt32 + 1}},
		{"intToUint8", func(n int64) (any, error) { return intToUint8(int(n)) }, []int64{0, 255}, []int64{-1, 256}},
		{"intToUint32", func(n int64) (any, error) { return intToUint32(int(n)) }, []int64{0, 65536}, []int64{-1}},
	} {
		for _, n := range c.ok {
			if _, err := c.f(n); err != nil {
				t.Errorf("%s(%d): %v", c.name, n, err)
			}
		}
		for _, n := range c.bad {
			if _, err := c.f(n); !errors.Is(err, errRange) {
				t.Errorf("%s(%d): error %v, want errRange", c.name, n, err)
			}
		}
	}
	if v, ok := indexToUint16(65535); !ok || v != 65535 {
		t.Errorf("indexToUint16(65535) = %d, %v", v, ok)
	}
	if _, ok := indexToUint16(65536); ok {
		t.Error("indexToUint16(65536) fits")
	}
}

// TestFlagConversions: a bound wider than the field is a usage error (exit
// 2), not a truncated value.
func TestFlagConversions(t *testing.T) {
	a := args{"wide": "300", "big": "70000", "huge": "5000000000", "fits": "7"}
	var usage usageError
	if _, err := a.uint8Flag("wide", 0, 0, 1000); !errors.As(err, &usage) {
		t.Errorf("uint8Flag: error %v, want a usage error", err)
	}
	if _, err := a.requiredUint8Flag("wide", 0, 1000); !errors.As(err, &usage) {
		t.Errorf("requiredUint8Flag: error %v, want a usage error", err)
	}
	if _, err := a.uint16Flag("big", 0, 0, 100000); !errors.As(err, &usage) {
		t.Errorf("uint16Flag: error %v, want a usage error", err)
	}
	if _, err := a.requiredUint16Flag("big", 0, 100000); !errors.As(err, &usage) {
		t.Errorf("requiredUint16Flag: error %v, want a usage error", err)
	}
	if _, err := a.uint32Flag("huge", 0, 0, 1<<40); !errors.As(err, &usage) {
		t.Errorf("uint32Flag: error %v, want a usage error", err)
	}
	if _, err := a.requiredUint8Flag("missing", 0, 255); !errors.As(err, &usage) {
		t.Errorf("requiredUint8Flag of a missing flag: error %v, want a usage error", err)
	}
	if _, err := a.uint8Flag("fits", 0, 0, 5); !errors.As(err, &usage) {
		t.Errorf("uint8Flag out of its bounds: error %v, want a usage error", err)
	}
	v8, err := a.uint8Flag("fits", 0, 0, 255)
	v16, err16 := a.requiredUint16Flag("fits", 0, 65535)
	v32, err32 := a.uint32Flag("absent", 9, 0, 10)
	if err != nil || err16 != nil || err32 != nil || v8 != 7 || v16 != 7 || v32 != 9 {
		t.Errorf("got %d %d %d, errors %v %v %v", v8, v16, v32, err, err16, err32)
	}
}
