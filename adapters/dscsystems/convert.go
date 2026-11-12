// SPDX-License-Identifier: GPL-3.0-or-later

// Checked conversions of fixture, flag and length integers to the widths of
// the protocol fields. The fixture and flag checks bound every value first;
// these conversions fail loudly instead of truncating if a bound and a field
// width ever disagree: a usage error (exit 2) for a flag, an invalid fixture
// (exit 4) for a fixture (docs/CONTAINER_CONTRACT.md, "Exit codes").

package main

import (
	"errors"
	"fmt"
	"math"
)

// errRange is a value that does not fit the field it is converted to.
var errRange = errors.New("value out of range")

func rangeError(n any, field string) error {
	return fmt.Errorf("%v does not fit %s: %w", n, field, errRange)
}

func toUint8(n int64) (uint8, error) {
	if n < 0 || n > math.MaxUint8 {
		return 0, rangeError(n, "8 bits")
	}
	return uint8(n), nil
}

func toUint16(n int64) (uint16, error) {
	if n < 0 || n > math.MaxUint16 {
		return 0, rangeError(n, "16 bits")
	}
	return uint16(n), nil
}

func toUint32(n int64) (uint32, error) {
	if n < 0 || n > math.MaxUint32 {
		return 0, rangeError(n, "32 bits")
	}
	return uint32(n), nil
}

func toInt32(n int64) (int32, error) {
	if n < math.MinInt32 || n > math.MaxInt32 {
		return 0, rangeError(n, "32 signed bits")
	}
	return int32(n), nil
}

// intToUint8 converts a length or a count.
func intToUint8(n int) (uint8, error) {
	if n < 0 || n > math.MaxUint8 {
		return 0, rangeError(n, "8 bits")
	}
	return uint8(n), nil
}

// intToUint32 converts a length or a count.
func intToUint32(n int) (uint32, error) {
	if n < 0 || uint64(n) > math.MaxUint32 {
		return 0, rangeError(n, "32 bits")
	}
	return uint32(n), nil
}

// indexToUint16 converts a point index of a request, which the object
// headers carry in up to 32 bits, to the 16 bits of a fixture index.
func indexToUint16(n uint32) (uint16, bool) {
	if n > math.MaxUint16 {
		return 0, false
	}
	return uint16(n), true
}

// usageUint8, usageUint16 and usageUint32 narrow a flag value bounded by args.integer: a value outside the
// field is a usage error.
func usageUint8(name string, n int64) (uint8, error) {
	v, err := toUint8(n)
	if err != nil {
		return 0, usagef("--%s: %v", name, err)
	}
	return v, nil
}

func usageUint16(name string, n int64) (uint16, error) {
	v, err := toUint16(n)
	if err != nil {
		return 0, usagef("--%s: %v", name, err)
	}
	return v, nil
}

func usageUint32(name string, n int64) (uint32, error) {
	v, err := toUint32(n)
	if err != nil {
		return 0, usagef("--%s: %v", name, err)
	}
	return v, nil
}

// uint8Flag is args.integer as a uint8.
func (a args) uint8Flag(name string, def, minimum, maximum int64) (uint8, error) {
	n, err := a.integer(name, def, minimum, maximum)
	if err != nil {
		return 0, err
	}
	return usageUint8(name, n)
}

// requiredUint8Flag is args.requiredInteger as a uint8.
func (a args) requiredUint8Flag(name string, minimum, maximum int64) (uint8, error) {
	n, err := a.requiredInteger(name, minimum, maximum)
	if err != nil {
		return 0, err
	}
	return usageUint8(name, n)
}

// uint16Flag is args.integer as a uint16.
func (a args) uint16Flag(name string, def, minimum, maximum int64) (uint16, error) {
	n, err := a.integer(name, def, minimum, maximum)
	if err != nil {
		return 0, err
	}
	return usageUint16(name, n)
}

// requiredUint16Flag is args.requiredInteger as a uint16.
func (a args) requiredUint16Flag(name string, minimum, maximum int64) (uint16, error) {
	n, err := a.requiredInteger(name, minimum, maximum)
	if err != nil {
		return 0, err
	}
	return usageUint16(name, n)
}

// uint32Flag is args.integer as a uint32.
func (a args) uint32Flag(name string, def, minimum, maximum int64) (uint32, error) {
	n, err := a.integer(name, def, minimum, maximum)
	if err != nil {
		return 0, err
	}
	return usageUint32(name, n)
}
