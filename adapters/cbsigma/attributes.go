// SPDX-License-Identifier: MIT

// Device attributes, group 0 (contract 1.3 and 1.5): the master's
// attribute-read and attribute-write.

package main

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"math"
	"strconv"
	"time"

	dnp3 "github.com/cB-Sigma/dnp3"
)

var attributeOperations = []string{"attribute-read", "attribute-write"}

// The data type codes of an attribute (IEEE 1815-2012 table 11-1 and
// 5.5.4): 7 is a DNP3 time.
const attrTime = 7

var attributeTypes = map[uint8]string{
	dnp3.AttrVSTR: "visibleString", dnp3.AttrUINT: "unsignedInt", dnp3.AttrINT: "signedInt", dnp3.AttrFLT: "float",
	dnp3.AttrOSTR: "octetString", dnp3.AttrBSTR: "bitString", attrTime: "time",
	dnp3.AttrList: "attributeList", dnp3.AttrExtList: "extendedAttributeList",
}

type attributeRequest struct {
	write          bool
	set, variation uint8
	code           uint8 // the data type written
	data           []byte
}

func parseAttributeOperation(op string, a args) (attributeRequest, error) {
	r := attributeRequest{write: op == "attribute-write"}
	set, err := a.requiredInteger("set", 0, 255)
	if err != nil {
		return r, err
	}
	r.set = uint8(set)
	if !r.write {
		v, err := a.integer("variation", 254, 1, 255)
		if err != nil {
			return r, err
		}
		r.variation = uint8(v)
		return r, nil
	}
	v, err := a.requiredInteger("variation", 1, 253)
	if err != nil {
		return r, err
	}
	r.variation = uint8(v)
	given := 0
	for _, n := range []string{"text", "uint", "int", "float", "octets", "bits", "time"} {
		if a.has(n) {
			given++
		}
	}
	if given != 1 {
		return r, usagef("attribute-write takes exactly one of --text, --uint, --int, --float, --octets, --bits, --time")
	}
	switch {
	case a.has("text"):
		text := a.str("text", "")
		if len(text) > 255 {
			return r, usagef("--text has more than 255 octets")
		}
		r.code, r.data = dnp3.AttrVSTR, []byte(text)
	case a.has("uint"):
		n, err := strconv.ParseUint(a.str("uint", ""), 10, 32)
		if err != nil {
			return r, usagef("--uint must be 0..4294967295")
		}
		r.code, r.data = dnp3.AttrUINT, binary.LittleEndian.AppendUint32(nil, uint32(n))
	case a.has("int"):
		n, err := strconv.ParseInt(a.str("int", ""), 10, 32)
		if err != nil {
			return r, usagef("--int must be -2147483648..2147483647")
		}
		r.code, r.data = dnp3.AttrINT, binary.LittleEndian.AppendUint32(nil, uint32(int32(n)))
	case a.has("float"):
		f, err := strconv.ParseFloat(a.str("float", ""), 64)
		if err != nil || math.IsInf(f, 0) || math.IsNaN(f) || math.Abs(f) > math.MaxFloat32 {
			return r, usagef("--float must be a number a 32-bit float holds")
		}
		r.code, r.data = dnp3.AttrFLT, binary.LittleEndian.AppendUint32(nil, math.Float32bits(float32(f)))
	case a.has("octets"), a.has("bits"):
		name, code := "octets", uint8(dnp3.AttrOSTR)
		if a.has("bits") {
			name, code = "bits", dnp3.AttrBSTR
		}
		b, err := hex.DecodeString(a.str(name, ""))
		if err != nil || len(b) > 255 {
			return r, usagef("--%s must be hex, at most 255 octets", name)
		}
		r.code, r.data = code, b
	case a.has("time"):
		t, err := time.Parse("2006-01-02T15:04:05.000Z", a.str("time", ""))
		if err != nil {
			return r, usagef("--time must be YYYY-MM-DDTHH:MM:SS.mmmZ")
		}
		r.code, r.data = attrTime, dnp3.AppendTime48(nil, t)
	}
	return r, nil
}

// attributeDoc is one attribute of a response.
func attributeDoc(p dnp3.Point) object {
	name, ok := attributeTypes[p.Status]
	if !ok {
		name = "octetString"
	}
	var value any
	switch v := p.Value.(type) {
	case string:
		value = v
		if p.Status == attrTime {
			if b, err := hex.DecodeString(v); err == nil && len(b) == 6 {
				value = formatTime(dnp3.Time48(b))
			}
		}
	case float64:
		value = number(v)
	}
	if p.Status == dnp3.AttrList || p.Status == dnp3.AttrExtList {
		value = nil
	}
	return object{{"set", p.Index}, {"variation", p.Var}, {"type", name}, {"value", value}}
}

func (c *master) runAttribute(ctx context.Context, r attributeRequest, out *[]object) error {
	if r.write {
		objs := dnp3.AppendHeader(nil, 0, r.variation, dnp3.Q8StartStop, uint32(r.set), uint32(r.set))
		objs = append(append(objs, r.code, byte(len(r.data))), r.data...)
		_, err := c.request(ctx, dnp3.FCWrite, objs)
		return err
	}
	frags, err := c.request(ctx, dnp3.FCRead, dnp3.AppendHeader(nil, 0, r.variation, dnp3.Q8StartStop, uint32(r.set), uint32(r.set)))
	for _, f := range frags {
		blocks, _ := dnp3.ParseObjects(f.Objects, true)
		for _, b := range blocks {
			if b.Header.Group != 0 {
				continue
			}
			for _, p := range b.Points {
				*out = append(*out, attributeDoc(p))
			}
		}
	}
	return err
}
