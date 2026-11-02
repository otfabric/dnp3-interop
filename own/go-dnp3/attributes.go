// SPDX-License-Identifier: MIT

// Device attributes, group 0 (contract 1.3).

package main

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/otfabric/go-dnp3/app"
	"github.com/otfabric/go-dnp3/client"
	"github.com/otfabric/go-dnp3/server"
)

var attributeTypeNames = map[app.AttrType]string{
	app.AttrVisibleString: "visibleString", app.AttrUnsignedInt: "unsignedInt", app.AttrSignedInt: "signedInt",
	app.AttrFloat: "float", app.AttrOctetString: "octetString", app.AttrBitString: "bitString",
	app.AttrTime: "time", app.AttrList: "attributeList", app.AttrExtList: "extendedAttributeList",
}

func attributeTypeName(t app.AttrType) string {
	if n, ok := attributeTypeNames[t]; ok {
		return n
	}
	return "octetString"
}

// libraryAttribute is the library's value of an attribute of the fixture.
func libraryAttribute(a attribute) app.Attribute {
	switch a.Type {
	case "unsignedInt":
		return app.UintAttribute(uint32(a.Number))
	case "signedInt":
		return app.IntAttribute(int32(a.Number))
	case "float":
		return app.FloatAttribute(a.Real)
	case "octetString":
		return app.Attribute{Type: app.AttrOctetString, Data: a.Octets}
	case "bitString":
		return app.Attribute{Type: app.AttrBitString, Data: a.Octets}
	case "time":
		return app.TimeAttribute(app.TimestampOf(a.Time))
	}
	return app.StringAttribute(a.Text)
}

// attributeOptions gives the outstation the fixture's attributes, a set at
// a time; the handler reports accepted writes.
func attributeOptions(fx *fixture) []server.Option {
	sets := map[uint8]map[uint8]app.Attribute{}
	writable := map[uint8][]uint8{}
	var order []uint8
	for _, a := range fx.Attributes {
		if sets[a.Set] == nil {
			sets[a.Set] = map[uint8]app.Attribute{}
			order = append(order, a.Set)
		}
		sets[a.Set][a.Variation] = libraryAttribute(a)
		if a.Writable {
			writable[a.Set] = append(writable[a.Set], a.Variation)
		}
	}
	slices.Sort(order)
	var opts []server.Option
	for _, set := range order {
		opts = append(opts, server.WithAttributeSet(uint16(set), sets[set], writable[set]...))
	}
	return opts
}

// attributeValue is the value of an attribute document.
func attributeValue(a app.Attribute) any {
	switch a.Type {
	case app.AttrVisibleString:
		return a.Text()
	case app.AttrUnsignedInt:
		if v, err := a.Uint(); err == nil {
			return v
		}
	case app.AttrSignedInt:
		if v, err := a.Int(); err == nil {
			return v
		}
	case app.AttrFloat:
		if v, err := a.Float(); err == nil {
			return number(v)
		}
	case app.AttrTime:
		if len(a.Data) == 6 {
			var b [8]byte
			copy(b[:], a.Data)
			return formatTime(time.UnixMilli(int64(binary.LittleEndian.Uint64(b[:]))))
		}
	case app.AttrList, app.AttrExtList:
		return nil // the contract gives lists no value
	}
	return hex.EncodeToString(a.Data)
}

var attributeOperations = []string{"attribute-read", "attribute-write"}

type attributeRequest func(ctx context.Context, c *client.Client, r *[]object) error

func parseAttributeOperation(op string, a args) (attributeRequest, error) {
	set, err := a.requiredInteger("set", 0, 255)
	if err != nil {
		return nil, err
	}
	if op == "attribute-read" {
		variation, err := a.integer("variation", 254, 1, 254)
		if err != nil {
			return nil, err
		}
		// A READ of the set as the range; the attributes in the order
		// they arrived (Client.ReadAttributeSet returns a map).
		return func(ctx context.Context, c *client.Client, r *[]object) error {
			res, err := c.Read(ctx, app.IndexRange(app.GV(0, uint8(variation)), uint32(set), uint32(set)))
			if res == nil {
				return err
			}
			for _, h := range res.Headers() {
				if h.GV.Group() != 0 {
					continue
				}
				for _, o := range h.Objects {
					if v, ok := o.Value.(app.Attribute); ok {
						*r = append(*r, object{{"set", o.Index}, {"variation", h.GV.Variation()},
							{"type", attributeTypeName(v.Type)}, {"value", attributeValue(v)}})
					}
				}
			}
			return err
		}, nil
	}
	variation, err := a.requiredInteger("variation", 1, 253)
	if err != nil {
		return nil, err
	}
	var w app.Attribute
	given := 0
	if a.has("text") {
		w, given = app.StringAttribute(a.str("text", "")), given+1
	}
	if a.has("uint") {
		n, err := strconv.ParseInt(a.str("uint", ""), 10, 64)
		if err != nil || n < 0 || n > 4294967295 {
			return nil, usagef("--uint must be an integer in 0..4294967295")
		}
		w, given = app.UintAttribute(uint32(n)), given+1
	}
	if a.has("int") {
		n, err := strconv.ParseInt(a.str("int", ""), 10, 64)
		if err != nil || n < -2147483648 || n > 2147483647 {
			return nil, usagef("--int must be an integer in -2147483648..2147483647")
		}
		w, given = app.IntAttribute(int32(n)), given+1
	}
	if a.has("float") {
		f, err := a.number("float")
		if err != nil || math.Abs(f) > math.MaxFloat32 {
			return nil, usagef("--float must be a number in the range of a 32-bit float")
		}
		w, given = app.Attribute{Type: app.AttrFloat, Data: binary.LittleEndian.AppendUint32(nil, math.Float32bits(float32(f)))}, given+1
	}
	for _, f := range []struct {
		name string
		kind app.AttrType
	}{{"octets", app.AttrOctetString}, {"bits", app.AttrBitString}} {
		if a.has(f.name) {
			b, err := hex.DecodeString(a.str(f.name, ""))
			if err != nil || len(b) > 255 {
				return nil, usagef("--%s must be hex of up to 255 octets", f.name)
			}
			w, given = app.Attribute{Type: f.kind, Data: b}, given+1
		}
	}
	if a.has("time") {
		at, err := time.Parse("2006-01-02T15:04:05.000Z", a.str("time", ""))
		if err != nil {
			return nil, usagef("--time must be YYYY-MM-DDTHH:MM:SS.mmmZ")
		}
		w, given = app.TimeAttribute(app.TimestampOf(at)), given+1
	}
	if given != 1 {
		return nil, usagef("attribute-write takes exactly one of --text, --uint, --int, --float, --octets, --bits, --time")
	}
	return func(ctx context.Context, c *client.Client, _ *[]object) error {
		return c.WriteAttributeSet(ctx, uint16(set), uint8(variation), w)
	}, nil
}
