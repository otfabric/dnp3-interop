// SPDX-License-Identifier: GPL-3.0-or-later

// Device attributes, group 0 (contract 1.3).

package main

import (
	"context"
	"encoding/hex"
	"strconv"

	"github.com/dscsystems/go-dnp3"
	"github.com/dscsystems/go-dnp3/master"
	"github.com/dscsystems/go-dnp3/outstation"
)

var attributeTypes = map[string]dnp3.AttributeType{
	"visibleString": dnp3.AttrVisibleString, "unsignedInt": dnp3.AttrUnsignedInt, "signedInt": dnp3.AttrSignedInt,
}

var attributeTypeNames = map[dnp3.AttributeType]string{
	dnp3.AttrVisibleString: "visibleString", dnp3.AttrUnsignedInt: "unsignedInt", dnp3.AttrSignedInt: "signedInt",
	dnp3.AttrFloat: "float", dnp3.AttrOctetString: "octetString", dnp3.AttrBitString: "bitString",
	dnp3.AttrTime: "time", dnp3.AttrAttributeList: "attributeList", dnp3.AttrExtAttributeList: "extendedAttributeList",
}

func libraryAttribute(a attribute) dnp3.Attribute {
	return dnp3.Attribute{Set: a.Set, Variation: a.Variation, Type: attributeTypes[a.Type], Text: a.Text, Number: a.Number}
}

// attributeConfig gives the outstation the fixture's attributes; an accepted
// write is a server event.
func attributeConfig(fx *fixture, cfg *outstation.Config) {
	for _, a := range fx.Attributes {
		cfg.Attributes = append(cfg.Attributes, libraryAttribute(a))
		if a.Writable {
			cfg.WritableAttributes = append(cfg.WritableAttributes, outstation.AttributeID{Set: a.Set, Variation: a.Variation})
		}
	}
	cfg.AttributeWrite = func(a dnp3.Attribute) bool {
		emit(object{{"event", "attribute-write"}, {"set", a.Set}, {"variation", a.Variation},
			{"type", attributeTypeNames[a.Type]}, {"value", attributeValue(a)}})
		return true
	}
}

func attributeValue(a dnp3.Attribute) any {
	switch a.Type {
	case dnp3.AttrVisibleString:
		return a.Text
	case dnp3.AttrUnsignedInt, dnp3.AttrSignedInt:
		return a.Number
	case dnp3.AttrFloat:
		return number(a.Real)
	case dnp3.AttrTime:
		return formatTime(a.Time)
	case dnp3.AttrAttributeList, dnp3.AttrExtAttributeList:
		return nil // the contract gives lists no value
	}
	return hex.EncodeToString(a.Octets)
}

var attributeOperations = []string{"attribute-read", "attribute-write"}

type attributeRequest func(ctx context.Context, s *master.Session, r *[]object) error

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
		return func(ctx context.Context, s *master.Session, r *[]object) error {
			var list []dnp3.Attribute
			if variation == 254 {
				list, err = s.ReadAttributeSet(ctx, uint8(set))
			} else {
				var one dnp3.Attribute
				if one, err = s.ReadAttribute(ctx, uint8(set), uint8(variation)); err == nil {
					list = []dnp3.Attribute{one}
				}
			}
			for _, x := range list {
				*r = append(*r, object{{"set", x.Set}, {"variation", x.Variation},
					{"type", attributeTypeNames[x.Type]}, {"value", attributeValue(x)}})
			}
			return err
		}, nil
	}
	variation, err := a.requiredInteger("variation", 1, 253)
	if err != nil {
		return nil, err
	}
	w := dnp3.Attribute{Set: uint8(set), Variation: uint8(variation)}
	given := 0
	if a.has("text") {
		w.Type, w.Text, given = dnp3.AttrVisibleString, a.str("text", ""), given+1
	}
	for _, f := range []struct {
		name   string
		kind   dnp3.AttributeType
		lo, hi int64
	}{{"uint", dnp3.AttrUnsignedInt, 0, 4294967295}, {"int", dnp3.AttrSignedInt, -2147483648, 2147483647}} {
		if a.has(f.name) {
			n, err := strconv.ParseInt(a.str(f.name, ""), 10, 64)
			if err != nil || n < f.lo || n > f.hi {
				return nil, usagef("--%s must be an integer in %d..%d", f.name, f.lo, f.hi)
			}
			w.Type, w.Number, given = f.kind, n, given+1
		}
	}
	if given != 1 {
		return nil, usagef("attribute-write takes exactly one of --text, --uint, --int")
	}
	return func(ctx context.Context, s *master.Session, _ *[]object) error {
		return s.WriteAttribute(ctx, w)
	}, nil
}
