// SPDX-License-Identifier: GPL-3.0-or-later

// Loads a fixture and applies the rules of fixtures/schema/dnp3-fixture.schema.json
// and of docs/FIXTURES.md ("cross-field rules"), as scripts/validate-fixtures.py does.

package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
)

var pointTypes = []string{
	"binaryInputs", "doubleBitInputs", "binaryOutputs", "counters", "frozenCounters",
	"analogInputs", "analogOutputs", "octetStrings",
}

type point struct {
	Index          uint16
	Value          float64 // numeric types; booleans as 0/1; the double-bit state
	Octets         []byte
	Flags          uint8
	Class          int
	SVar, EVar     uint8
	Target         *uint16
	SelectRequired bool
	Min, Max       *float64
}

type fixture struct {
	Name                     string
	Address, MasterAddress   uint16
	Unsolicited, NeedTime    bool
	ColdDelayMs, WarmDelayMs *uint16
	Points                   map[string][]point
	// Files are served by file transfer: name to content. Directories holds
	// every directory, those of the files and the listed empty ones.
	Files       map[string][]byte
	Directories []string
}

func (f *fixture) find(kind string, index uint16) *point {
	for i := range f.Points[kind] {
		if f.Points[kind][i].Index == index {
			return &f.Points[kind][i]
		}
	}
	return nil
}

type rule struct {
	svars, evars       []uint8
	sdefault, edefault uint8
}

var rules = map[string]rule{
	"binaryInputs":    {[]uint8{1, 2}, []uint8{1, 2}, 2, 2},
	"doubleBitInputs": {[]uint8{2}, []uint8{1, 2}, 2, 2},
	"binaryOutputs":   {[]uint8{2}, []uint8{1, 2}, 2, 2},
	"counters":        {[]uint8{1, 2, 5, 6}, []uint8{1, 2, 5, 6}, 1, 1},
	"frozenCounters":  {[]uint8{1, 2, 5, 6, 9, 10}, []uint8{1, 2, 5, 6}, 1, 1},
	"analogInputs":    {[]uint8{1, 2, 3, 4, 5, 6}, []uint8{1, 2, 3, 4, 5, 6, 7, 8}, 1, 1},
	"analogOutputs":   {[]uint8{1, 2, 3, 4}, []uint8{1, 2, 3, 4, 5, 6, 7, 8}, 1, 1},
	"octetStrings":    {nil, nil, 0, 0},
}

func isFloat32(v float64) bool { return float64(float32(v)) == v }

// integer reads a JSON number that must be an integer.
func integer(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, err := n.Int64()
	return i, err == nil
}

func float(v any) (float64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	f, err := n.Float64()
	return f, err == nil
}

func checkPoint(kind string, raw any, position int, errs *[]string) point {
	r := rules[kind]
	p := point{Flags: 0x01, SVar: r.sdefault, EVar: r.edefault}
	add := func(format string, a ...any) { *errs = append(*errs, fmt.Sprintf(format, a...)) }
	where := fmt.Sprintf("%s[%d]", kind, position)
	obj, ok := raw.(map[string]any)
	if !ok {
		add("%s: not an object", where)
		return p
	}
	allowed := []string{"index", "value", "class"}
	if kind != "octetStrings" {
		allowed = append(allowed, "flags", "staticVariation", "eventVariation")
	}
	if kind == "binaryOutputs" || kind == "analogOutputs" {
		allowed = append(allowed, "target", "selectRequired")
	}
	if kind == "analogOutputs" {
		allowed = append(allowed, "min", "max")
	}
	for k := range obj {
		if !slices.Contains(allowed, k) {
			add("%s: unknown field '%s'", where, k)
		}
	}
	i, ok := integer(obj["index"])
	if !ok || i < 0 || i > 65535 {
		add("%s: index must be an integer in 0..65535", where)
		return p
	}
	p.Index = uint16(i)
	where = fmt.Sprintf("%s[%d]", kind, p.Index)

	if v, present := obj["class"]; present {
		c, ok := integer(v)
		if !ok || c < 0 || c > 3 {
			add("%s: class must be 0..3", where)
		} else {
			p.Class = int(c)
		}
	}
	if v, present := obj["staticVariation"]; present {
		n, ok := integer(v)
		if !ok || n < 0 || n > 255 || !slices.Contains(r.svars, uint8(n)) {
			add("%s: staticVariation is not one of the allowed variations", where)
		} else {
			p.SVar = uint8(n)
		}
	}
	if v, present := obj["eventVariation"]; present {
		n, ok := integer(v)
		if !ok || n < 0 || n > 255 || !slices.Contains(r.evars, uint8(n)) {
			add("%s: eventVariation is not one of the allowed variations", where)
		} else {
			p.EVar = uint8(n)
		}
	}
	if v, present := obj["flags"]; present {
		p.Flags = 0
		list, ok := v.([]any)
		if !ok {
			add("%s: flags must be an array", where)
		}
		for _, f := range list {
			name, _ := f.(string)
			bit := flagBit(kind, name)
			switch {
			case bit < 0:
				add("%s: flag %v is not allowed for %s", where, f, kind)
			case p.Flags&(1<<bit) != 0:
				add("%s: flag %v is repeated", where, f)
			default:
				p.Flags |= 1 << bit
			}
		}
	}
	// A static variation without flags reports the point ONLINE.
	noFlags := (kind == "binaryInputs" && p.SVar == 1) || (kind == "counters" && (p.SVar == 5 || p.SVar == 6)) ||
		(kind == "frozenCounters" && (p.SVar == 9 || p.SVar == 10)) || (kind == "analogInputs" && (p.SVar == 3 || p.SVar == 4))
	if noFlags && p.Flags != 0x01 {
		add("%s: static variation %d has no flags, so flags must be [\"ONLINE\"]", where, p.SVar)
	}
	if v, present := obj["selectRequired"]; present {
		b, ok := v.(bool)
		if !ok {
			add("%s: selectRequired must be a boolean", where)
		}
		p.SelectRequired = b
	}
	if v, present := obj["target"]; present {
		t, ok := integer(v)
		if !ok || t < 0 || t > 65535 {
			add("%s: target must be an index", where)
		} else {
			u := uint16(t)
			p.Target = &u
		}
	}
	for _, bound := range []string{"min", "max"} {
		v, present := obj[bound]
		if !present {
			continue
		}
		x, ok := float(v)
		if !ok {
			add("%s: %s must be a number", where, bound)
			continue
		}
		if !isFloat32(x) {
			add("%s: %s is not exactly representable in single precision", where, bound)
		}
		if bound == "min" {
			p.Min = &x
		} else {
			p.Max = &x
		}
	}

	v, present := obj["value"]
	if !present {
		add("%s: value is required", where)
		return p
	}
	switch kind {
	case "binaryInputs", "binaryOutputs":
		b, ok := v.(bool)
		if !ok {
			add("%s: value must be a boolean", where)
		} else if b {
			p.Value = 1
		}
	case "doubleBitInputs":
		d, ok := integer(v)
		if !ok || d < 0 || d > 3 {
			add("%s: value must be 0..3", where)
		} else {
			p.Value = float64(d)
		}
	case "octetStrings":
		s, _ := v.(string)
		if !regexp.MustCompile(`^([0-9a-f]{2}){1,255}$`).MatchString(s) {
			add("%s: value must be 1..255 octets in lower-case hex", where)
		} else {
			p.Octets, _ = hex.DecodeString(s)
		}
	case "counters", "frozenCounters":
		c, ok := integer(v)
		if !ok || c < 0 || c > 4294967295 {
			add("%s: value must be an integer in 0..4294967295", where)
			break
		}
		p.Value = float64(c)
		s16 := []uint8{2, 6}
		if kind == "frozenCounters" {
			s16 = append(s16, 10)
		}
		if (slices.Contains(s16, p.SVar) || (p.Class != 0 && (p.EVar == 2 || p.EVar == 6))) && c > 65535 {
			add("%s: value does not fit a 16-bit variation", where)
		}
	default: // analog inputs and outputs
		x, ok := float(v)
		if !ok {
			add("%s: value must be a number", where)
			return p
		}
		p.Value = x
		sint, s16 := []uint8{1, 2}, []uint8{2}
		if kind == "analogInputs" {
			sint, s16 = []uint8{1, 2, 3, 4}, []uint8{2, 4}
		}
		events := p.Class != 0
		if (slices.Contains(sint, p.SVar) || (events && slices.Contains([]uint8{1, 2, 3, 4}, p.EVar))) && x != math.Floor(x) {
			add("%s: value is not an integer, but a variation of the point is", where)
		}
		if slices.Contains(s16, p.SVar) || (events && (p.EVar == 2 || p.EVar == 4)) {
			if x < -32768 || x > 32767 {
				add("%s: value does not fit a 16-bit variation", where)
			}
		} else if x < -2147483648 || x > 2147483647 {
			add("%s: value does not fit a 32-bit variation", where)
		}
		if !isFloat32(x) {
			add("%s: value is not exactly representable in single precision", where)
		}
		if p.Min != nil && p.Max != nil && *p.Min > *p.Max {
			add("%s: min is larger than max", where)
		}
		if (p.Min != nil && x < *p.Min) || (p.Max != nil && x > *p.Max) {
			add("%s: value is outside [min, max]", where)
		}
	}
	return p
}

func linkAddress(v any) (uint16, bool) {
	a, ok := integer(v)
	if !ok || a < 0 || a > 65519 {
		return 0, false
	}
	return uint16(a), true
}

// loadFixture parses and validates a fixture.
func loadFixture(text []byte) (*fixture, error) {
	dec := json.NewDecoder(bytes.NewReader(text))
	dec.UseNumber()
	var top map[string]any
	if err := dec.Decode(&top); err != nil {
		return nil, fmt.Errorf("not a JSON object: %v", err)
	}
	var errs []string
	add := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }
	fx := &fixture{Points: map[string][]point{}}

	for k := range top {
		if !slices.Contains([]string{"schemaVersion", "name", "description", "outstation", "files", "directories"}, k) && !slices.Contains(pointTypes, k) {
			add("unknown field '%s'", k)
		}
	}
	if top["schemaVersion"] != "1.0" {
		add("schemaVersion must be \"1.0\"")
	}
	if name, ok := top["name"].(string); ok && regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`).MatchString(name) {
		fx.Name = name
	} else {
		add("name is missing or invalid")
	}
	if d, present := top["description"]; present {
		if _, ok := d.(string); !ok {
			add("description must be a string")
		}
	}
	if o, ok := top["outstation"].(map[string]any); !ok {
		add("outstation is required")
	} else {
		for k := range o {
			if !slices.Contains([]string{"address", "masterAddress", "unsolicited", "needTime", "coldRestartDelayMs", "warmRestartDelayMs"}, k) {
				add("outstation: unknown field '%s'", k)
			}
		}
		var okA, okM bool
		if fx.Address, okA = linkAddress(o["address"]); !okA {
			add("outstation: address must be 0..65519")
		}
		if fx.MasterAddress, okM = linkAddress(o["masterAddress"]); !okM {
			add("outstation: masterAddress must be 0..65519")
		} else if okA && fx.Address == fx.MasterAddress {
			add("outstation: address and masterAddress are equal")
		}
		for key, target := range map[string]*bool{"unsolicited": &fx.Unsolicited, "needTime": &fx.NeedTime} {
			if v, present := o[key]; present {
				b, ok := v.(bool)
				if !ok {
					add("outstation: %s must be a boolean", key)
				}
				*target = b
			}
		}
		for key, target := range map[string]**uint16{"coldRestartDelayMs": &fx.ColdDelayMs, "warmRestartDelayMs": &fx.WarmDelayMs} {
			if v, present := o[key]; present {
				d, ok := integer(v)
				if !ok || d < 0 || d > 65535 {
					add("outstation: %s must be 0..65535", key)
				} else {
					u := uint16(d)
					*target = &u
				}
			}
		}
	}

	for _, kind := range pointTypes {
		raw, present := top[kind]
		if !present {
			continue
		}
		list, ok := raw.([]any)
		if !ok {
			add("%s must be an array", kind)
			continue
		}
		seen := map[uint16]bool{}
		for i, item := range list {
			p := checkPoint(kind, item, i, &errs)
			if seen[p.Index] {
				add("%s[%d]: duplicate index", kind, p.Index)
			}
			seen[p.Index] = true
			fx.Points[kind] = append(fx.Points[kind], p)
		}
	}

	for _, pair := range [][2]string{{"binaryOutputs", "binaryInputs"}, {"analogOutputs", "analogInputs"}} {
		targets := map[uint16]uint16{}
		for _, p := range fx.Points[pair[0]] {
			if p.Target == nil {
				continue
			}
			t := *p.Target
			if fx.find(pair[1], t) == nil {
				add("%s[%d]: target %d is not one of the %s", pair[0], p.Index, t, pair[1])
			} else if other, dup := targets[t]; dup {
				add("%s[%d]: target %d is also the target of output %d", pair[0], p.Index, t, other)
			}
			targets[t] = p.Index
		}
	}
	for _, p := range fx.Points["frozenCounters"] {
		if fx.find("counters", p.Index) == nil {
			add("frozenCounters[%d]: there is no counter with this index to freeze", p.Index)
		}
	}
	checkFiles(fx, top, add)
	if len(errs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return fx, nil
}

var fileName = regexp.MustCompile(`^(/[A-Za-z0-9_-][A-Za-z0-9._-]*)+$`)

// checkFiles reads the files and directories of a fixture.
func checkFiles(fx *fixture, top map[string]any, add func(string, ...any)) {
	dirs := map[string]bool{}
	if raw, present := top["files"]; present {
		fx.Files = map[string][]byte{}
		list, ok := raw.([]any)
		if !ok {
			add("files must be an array")
		}
		for i, item := range list {
			f, ok := item.(map[string]any)
			if !ok {
				add("files[%d] must be an object", i)
				continue
			}
			for k := range f {
				if k != "name" && k != "content" {
					add("files[%d]: unknown field '%s'", i, k)
				}
			}
			name, _ := f["name"].(string)
			text, _ := f["content"].(string)
			content, err := hex.DecodeString(text)
			switch {
			case !fileName.MatchString(name) || len(name) > 200:
				add("files[%d]: name is missing or invalid", i)
			case err != nil || strings.ToLower(text) != text || len(content) > 65536:
				add("files[%d]: content must be up to 65536 octets of lower-case hex", i)
			case fx.Files[name] != nil:
				add("files: %s is listed twice", name)
			default:
				fx.Files[name] = content
			}
		}
	}
	if raw, present := top["directories"]; present {
		list, ok := raw.([]any)
		if !ok {
			add("directories must be an array")
		}
		for i, item := range list {
			name, _ := item.(string)
			switch {
			case !fileName.MatchString(name) || len(name) > 200:
				add("directories[%d]: name is missing or invalid", i)
			case dirs[name]:
				add("directories: %s is listed twice", name)
			default:
				dirs[name] = true
			}
		}
	}
	for name := range fx.Files {
		for d := name[:strings.LastIndex(name, "/")]; d != ""; d = d[:strings.LastIndex(d, "/")] {
			dirs[d] = true
		}
	}
	for d := range dirs {
		for p := d[:strings.LastIndex(d, "/")]; p != ""; p = p[:strings.LastIndex(p, "/")] {
			dirs[p] = true
		}
	}
	for name := range fx.Files {
		if dirs[name] {
			add("files: %s is also a directory", name)
		}
	}
	for d := range dirs {
		fx.Directories = append(fx.Directories, d)
	}
	slices.Sort(fx.Directories)
}
