// SPDX-License-Identifier: GPL-3.0-or-later

// Several controls in one request (contract 1.3).

package main

import (
	"context"
	"math"
	"strconv"
	"strings"

	"github.com/dscsystems/go-dnp3"
	"github.com/dscsystems/go-dnp3/master"
)

var controlCodes = map[string]dnp3.ControlCode{"LATCH_ON": 0x03, "LATCH_OFF": 0x04, "PULSE_ON": 0x01,
	"PULSE_OFF": 0x02, "CLOSE": 0x41, "TRIP": 0x81}

// parseControls reads --controls: "g12v1:INDEX:CODE" and "g41vN:INDEX:VALUE"
// items, comma-separated.
func parseControls(text string) ([]master.Command, error) {
	var cmds []master.Command
	for _, item := range strings.Split(text, ",") {
		parts := strings.Split(item, ":")
		if len(parts) != 3 {
			return nil, usagef("--controls items are TYPE:INDEX:CODE or TYPE:INDEX:VALUE")
		}
		index, err := strconv.ParseUint(parts[1], 10, 16)
		if err != nil {
			return nil, usagef("--controls: '%s' is not a point index", parts[1])
		}
		idx := uint16(index)
		if parts[0] == "g12v1" {
			code, ok := controlCodes[parts[2]]
			if !ok {
				return nil, usagef("--controls: '%s' is not a control code", parts[2])
			}
			cmds = append(cmds, master.CROB(idx, dnp3.ControlRelayOutputBlock{Code: code, Count: 1, OnTime: 100, OffTime: 100}))
			continue
		}
		value, err := strconv.ParseFloat(parts[2], 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, usagef("--controls: '%s' is not a number", parts[2])
		}
		integral := func(lo, hi float64) bool { return value == math.Floor(value) && value >= lo && value <= hi }
		switch {
		case parts[0] == "g41v1" && integral(math.MinInt32, math.MaxInt32):
			cmds = append(cmds, master.AnalogOutputInt32(idx, int32(value)))
		case parts[0] == "g41v2" && integral(math.MinInt16, math.MaxInt16):
			cmds = append(cmds, master.AnalogOutputInt16(idx, int16(value)))
		case parts[0] == "g41v3" && math.Abs(value) <= math.MaxFloat32:
			cmds = append(cmds, master.AnalogOutputFloat32(idx, float32(value)))
		case parts[0] == "g41v4":
			cmds = append(cmds, master.AnalogOutputFloat64(idx, value))
		default:
			return nil, usagef("--controls: '%s' is not a control type with a value in its range", item)
		}
	}
	return cmds, nil
}

func parseOperateMulti(a args) (request, error) {
	text, err := a.required("controls")
	if err != nil {
		return nil, err
	}
	cmds, err := parseControls(text)
	if err != nil {
		return nil, err
	}
	mode := a.str("mode", "direct")
	if mode != "direct" && mode != "sbo" {
		return nil, usagef("--mode must be direct or sbo")
	}
	return func(ctx context.Context, s *master.Session, r *result) error {
		var res master.CommandResult
		var err error
		if mode == "sbo" {
			res, err = s.SelectAndOperate(ctx, cmds...)
		} else {
			res, err = s.DirectOperate(ctx, cmds...)
		}
		for _, st := range res.Statuses {
			r.commandStatuses = append(r.commandStatuses, uint8(st))
		}
		for _, st := range r.commandStatuses {
			v := st
			if r.commandStatus == nil || (*r.commandStatus == 0 && v != 0) {
				r.commandStatus = &v
			}
		}
		return err
	}, nil
}
