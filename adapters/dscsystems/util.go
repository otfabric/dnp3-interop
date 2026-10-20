// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// object keeps the field order of the contract in the JSON documents.
type object []field

type field struct {
	key   string
	value any
}

func (o object) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, f := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(f.key)
		b.Write(k)
		b.WriteByte(':')
		v, err := marshal(f.value)
		if err != nil {
			return nil, err
		}
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

var outMu sync.Mutex

// emit prints one JSON document on a single line of stdout.
func emit(doc object) {
	line, err := marshal(doc)
	if err != nil {
		logf("cannot encode document: %v", err)
		return
	}
	outMu.Lock()
	defer outMu.Unlock()
	_, _ = os.Stdout.Write(append(line, '\n'))
}

func logf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "["+adapter+"] "+format+"\n", a...)
}

func formatTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// number prints a value with no fractional part as an integer, so that every
// adapter prints the same text for it.
func number(v float64) any {
	if !math.IsInf(v, 0) && !math.IsNaN(v) && v == math.Floor(v) && math.Abs(v) < 9007199254740992 {
		return int64(v)
	}
	return v
}

var iinNames = [16]string{
	"BROADCAST", "CLASS_1_EVENTS", "CLASS_2_EVENTS", "CLASS_3_EVENTS", "NEED_TIME", "LOCAL_CONTROL",
	"DEVICE_TROUBLE", "DEVICE_RESTART", "NO_FUNC_CODE_SUPPORT", "OBJECT_UNKNOWN", "PARAMETER_ERROR",
	"EVENT_BUFFER_OVERFLOW", "ALREADY_EXECUTING", "CONFIG_CORRUPT", "RESERVED_2", "RESERVED_1",
}

// iinList names the bits of IIN1 (low octet) and IIN2 (high octet).
func iinList(bits uint16) []string {
	out := []string{}
	for i := range 16 {
		if bits&(1<<i) != 0 {
			out = append(out, iinNames[i])
		}
	}
	return out
}

var commonFlags = [5]string{"ONLINE", "RESTART", "COMM_LOST", "REMOTE_FORCED", "LOCAL_FORCED"}

type family int

const (
	familyBinary family = iota
	familyCounter
	familyAnalog
)

func familyOfGroup(group uint8) family {
	switch group {
	case 20, 21, 22, 23:
		return familyCounter
	case 30, 31, 32, 33, 40, 42:
		return familyAnalog
	}
	return familyBinary
}

func highFlag(f family, bit int, outputs bool) string {
	switch f {
	case familyCounter:
		if bit == 5 {
			return "ROLLOVER"
		}
		return "DISCONTINUITY"
	case familyAnalog:
		if bit == 5 {
			return "OVER_RANGE"
		}
		return "REFERENCE_ERR"
	}
	if bit == 5 && !outputs {
		return "CHATTER_FILTER"
	}
	return ""
}

func flagNames(group uint8, flags uint8) []string {
	out := []string{}
	for bit := range 5 {
		if flags&(1<<bit) != 0 {
			out = append(out, commonFlags[bit])
		}
	}
	f := familyOfGroup(group)
	outputs := group == 10 || group == 11
	for bit := 5; bit < 7; bit++ {
		if flags&(1<<bit) == 0 {
			continue
		}
		if f == familyBinary && (group == 3 || group == 4) && bit == 6 {
			continue // a state bit of the double-bit types
		}
		if name := highFlag(f, bit, outputs); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// flagBit is the bit of a flag name for a point type of the fixture, or -1.
func flagBit(pointType, name string) int {
	for bit, n := range commonFlags {
		if n == name {
			return bit
		}
	}
	f := familyBinary
	switch pointType {
	case "counters", "frozenCounters":
		f = familyCounter
	case "analogInputs", "analogOutputs", "frozenAnalogInputs":
		f = familyAnalog
	}
	for bit := 5; bit < 7; bit++ {
		if f == familyCounter && bit == 5 {
			continue // ROLLOVER is obsolete and not accepted in fixtures
		}
		if n := highFlag(f, bit, pointType == "binaryOutputs"); n != "" && n == name {
			return bit
		}
	}
	return -1
}

var commandStatusNames = []string{
	"SUCCESS", "TIMEOUT", "NO_SELECT", "FORMAT_ERROR", "NOT_SUPPORTED", "ALREADY_ACTIVE", "HARDWARE_ERROR",
	"LOCAL", "TOO_MANY_OPS", "NOT_AUTHORIZED", "AUTOMATION_INHIBIT", "PROCESSING_LIMITED", "OUT_OF_RANGE",
	"DOWNSTREAM_LOCAL", "ALREADY_COMPLETE", "BLOCKED", "CANCELLED", "BLOCKED_OTHER_MASTER", "DOWNSTREAM_FAIL",
}

func commandStatusName(s uint8) string {
	switch {
	case int(s) < len(commandStatusNames):
		return commandStatusNames[s]
	case s == 126:
		return "NON_PARTICIPATING"
	}
	return "UNDEFINED"
}

// usageError ends the process with exit 2 before anything is sent.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usagef(format string, a ...any) error { return usageError{fmt.Sprintf(format, a...)} }

// args is a parsed command line: "--name value" flags and bare "--name" switches.
type args map[string]string

func parseArgs(argv []string, flags, switches []string) (args, error) {
	known := map[string]bool{}
	for _, f := range flags {
		known[f] = true
	}
	isSwitch := map[string]bool{}
	for _, s := range switches {
		isSwitch[s] = true
	}
	out := args{}
	for i := 0; i < len(argv); i++ {
		name, ok := strings.CutPrefix(argv[i], "--")
		if !ok {
			return nil, usagef("unexpected argument '%s'", argv[i])
		}
		switch {
		case isSwitch[name]:
			out[name] = "true"
		case known[name]:
			if i+1 >= len(argv) {
				return nil, usagef("--%s needs a value", name)
			}
			i++
			out[name] = argv[i]
		default:
			return nil, usagef("unknown flag --%s", name)
		}
	}
	return out, nil
}

func (a args) has(name string) bool { _, ok := a[name]; return ok }

func (a args) str(name, def string) string {
	if v, ok := a[name]; ok {
		return v
	}
	return def
}

func (a args) required(name string) (string, error) {
	v, ok := a[name]
	if !ok {
		return "", usagef("--%s is required", name)
	}
	return v, nil
}

func (a args) integer(name string, def, minimum, maximum int64) (int64, error) {
	v, ok := a[name]
	if !ok {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < minimum || n > maximum {
		return 0, usagef("--%s must be an integer in %d..%d", name, minimum, maximum)
	}
	return n, nil
}

func (a args) requiredInteger(name string, minimum, maximum int64) (int64, error) {
	if _, err := a.required(name); err != nil {
		return 0, err
	}
	return a.integer(name, 0, minimum, maximum)
}

func (a args) number(name string) (float64, error) {
	v, err := a.required(name)
	if err != nil {
		return 0, err
	}
	n, perr := strconv.ParseFloat(v, 64)
	if perr != nil || math.IsInf(n, 0) || math.IsNaN(n) {
		return 0, usagef("--%s must be a number", name)
	}
	return n, nil
}

func (a args) classes(name, def string, minimum int) ([]int, error) {
	var out []int
	for _, item := range strings.Split(a.str(name, def), ",") {
		if len(item) != 1 || item[0] < byte('0'+minimum) || item[0] > '3' {
			return nil, usagef("--%s is a comma-separated list of classes %d..3", name, minimum)
		}
		out = append(out, int(item[0]-'0'))
	}
	return out, nil
}
