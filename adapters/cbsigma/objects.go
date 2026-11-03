// SPDX-License-Identifier: MIT

// Object documents (contract "Object documents") from the objects the
// library decodes.

package main

import (
	"encoding/hex"
	"time"

	dnp3 "github.com/cB-Sigma/dnp3"
)

// measurement lists the groups a result reports as objects.
var measurement = map[uint8]bool{
	1: true, 2: true, 3: true, 4: true, 10: true, 11: true, 13: true, 20: true, 21: true, 22: true, 23: true,
	30: true, 31: true, 32: true, 33: true, 34: true, 40: true, 42: true, 43: true, 50: true,
	110: true, 111: true, 121: true, 122: true,
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case bool:
		if x {
			return 1
		}
	}
	return 0
}

// objectDocs are the documents of the measurements in a fragment's
// objects. The library stops at an object it does not know; what came
// before it is reported.
func objectDocs(objs []byte, unsolicited bool) []object {
	blocks, _ := dnp3.ParseObjects(objs, true)
	var out []object
	for _, b := range blocks {
		h := b.Header
		if !measurement[h.Group] || (h.Group == 50 && h.Var != 4) {
			continue
		}
		for _, p := range b.Points {
			out = append(out, objectDoc(h.Group, h.Var, p, unsolicited))
		}
	}
	return out
}

func objectDoc(g, v uint8, p dnp3.Point, unsolicited bool) object {
	o := object{{"group", g}, {"variation", v}, {"index", p.Index}}
	switch g {
	case 1, 2, 10, 11, 13:
		b, _ := p.Value.(bool)
		o = append(o, field{"value", b})
	case 110, 111:
		o = append(o, field{"value", hex.EncodeToString(p.Raw)})
	case 50:
		o = append(o, field{"value", p.Interval})
	default:
		o = append(o, field{"value", number(toFloat(p.Value))})
	}
	switch g {
	case 13, 34, 43, 50, 110, 111:
	default:
		flags := p.Flags
		if !p.HasFlags {
			// A variation without flags: the point is reported online
			// (docs/FIXTURES.md).
			flags = 0x01
		}
		o = append(o, field{"flags", flagNames(g, flags)})
	}
	switch {
	case g == 50:
		if t, ok := p.Value.(time.Time); ok {
			o = append(o, field{"time", formatTime(t)})
		}
	case !p.Time.IsZero():
		o = append(o, field{"time", formatTime(p.Time)})
	}
	switch g {
	case 13, 43:
		o = append(o, field{"status", commandStatusName(p.Status)})
	case 50:
		o = append(o, field{"units", p.Units})
	case 121, 122:
		o = append(o, field{"association", p.Assoc})
	}
	return append(o, field{"unsolicited", unsolicited})
}
