// SPDX-License-Identifier: MIT

// Secure authentication v5 (contract 1.1) and security statistics (1.2).

package main

import (
	"encoding/hex"
	"slices"
	"strings"

	"github.com/otfabric/go-dnp3/app"
	"github.com/otfabric/go-dnp3/secauth"
	"github.com/otfabric/go-dnp3/server"
)

var (
	saFlags         = []string{"sa-update-key", "sa-user", "sa-mac"}
	statisticsFlags = []string{"sa-statistics-class", "sa-statistics-threshold"}
)

// The MAC algorithms of the contract and the library's code of each. The
// library has no AES-GMAC (MAC algorithm 6).
var (
	macAlgorithms = []string{"hmac-sha1-4", "hmac-sha1-8", "hmac-sha1-10", "hmac-sha256-8", "hmac-sha256-16", "aes-gmac"}
	macCodes      = map[string]app.MACAlgorithm{
		"hmac-sha1-4": app.MACHMACSHA1Trunc4, "hmac-sha1-8": app.MACHMACSHA1Trunc8,
		"hmac-sha1-10": app.MACHMACSHA1Trunc10, "hmac-sha256-8": app.MACHMACSHA256Trunc8,
		"hmac-sha256-16": app.MACHMACSHA256Trunc16,
	}
	supportedMACs = []string{"hmac-sha1-4", "hmac-sha1-8", "hmac-sha1-10", "hmac-sha256-8", "hmac-sha256-16"}
	keyWrap       = []string{"aes-128", "aes-256"}
)

// criticalFunctions are the function codes the outstation challenges: the
// library's default.
func criticalFunctions() []int {
	var out []int
	for _, fc := range server.DefaultCritical {
		out = append(out, int(fc))
	}
	slices.Sort(out)
	return out
}

type security struct {
	on   bool
	user secauth.User
	// Security statistics: the class of the g122 events and how many
	// increments of a statistic raise one.
	statisticsClass     int
	statisticsThreshold uint32
}

func parseSecurity(a args) (security, error) {
	mac := a.str("sa-mac", "hmac-sha256-16")
	if !slices.Contains(macAlgorithms, mac) {
		return security{}, usagef("--sa-mac must be one of %s", strings.Join(macAlgorithms, ", "))
	}
	if !slices.Contains(supportedMACs, mac) {
		return security{}, usagef("--sa-mac %s: the library has %s only", mac, strings.Join(supportedMACs, ", "))
	}
	if !a.has("sa-update-key") {
		for _, n := range append([]string{"sa-user", "sa-mac"}, statisticsFlags...) {
			if a.has(n) {
				return security{}, usagef("--%s needs --sa-update-key", n)
			}
		}
		return security{}, nil
	}
	key, err := hex.DecodeString(a.str("sa-update-key", ""))
	if err != nil || (len(key) != 16 && len(key) != 32) {
		return security{}, usagef("--sa-update-key must be 16 or 32 octets of hex")
	}
	user, err := a.integer("sa-user", 1, 1, 65535)
	if err != nil {
		return security{}, err
	}
	class, err := a.integer("sa-statistics-class", 1, 1, 3)
	if err != nil {
		return security{}, err
	}
	// At most 65 535 (IEEE 1815-2012 7.6.1.4.2), which go-dnp3 enforces.
	threshold, err := a.integer("sa-statistics-threshold", 10, 1, 65535)
	if err != nil {
		return security{}, err
	}
	return security{on: true, user: secauth.User{Number: uint16(user), UpdateKey: key, MAC: macCodes[mac]},
		statisticsClass: int(class), statisticsThreshold: uint32(threshold)}, nil
}

func (s security) server() server.SecureAuth {
	return server.SecureAuth{User: s.user, StatisticsClass: server.Class(s.statisticsClass),
		StatisticsThreshold: s.statisticsThreshold}
}
