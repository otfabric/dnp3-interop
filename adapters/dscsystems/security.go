// SPDX-License-Identifier: GPL-3.0-or-later

// Secure authentication v5 (contract 1.1).

package main

import (
	"encoding/hex"
	"slices"
	"strings"

	"github.com/dscsystems/go-dnp3/master"
	"github.com/dscsystems/go-dnp3/outstation"
)

var saFlags = []string{"sa-update-key", "sa-user", "sa-mac"}

// The library has HMAC-SHA-256 truncated to 16 octets (MAC algorithm 4) only.
var (
	macAlgorithms = []string{"hmac-sha1-4", "hmac-sha1-8", "hmac-sha1-10", "hmac-sha256-8", "hmac-sha256-16", "aes-gmac"}
	supportedMACs = []string{"hmac-sha256-16"}
	keyWrap       = []string{"aes-128", "aes-256"}
	// The function codes the outstation challenges: every request but READ,
	// CONFIRM and DELAY MEASURE (outstation/security.go, criticalFunction).
	criticalRequest = []int{2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22,
		24, 25, 26, 27, 28, 29, 30, 31}
)

type security struct {
	on   bool
	user uint16
	key  []byte
	// Security statistics (contract 1.2): the class of the g122 events and
	// how many increments of a statistic raise one.
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
	threshold, err := a.integer("sa-statistics-threshold", 10, 1, 1000000)
	if err != nil {
		return security{}, err
	}
	return security{on: true, user: uint16(user), key: key, statisticsClass: int(class),
		statisticsThreshold: uint32(threshold)}, nil
}

func (s security) outstation() *outstation.SecureAuthenticationConfig {
	if !s.on {
		return nil
	}
	return &outstation.SecureAuthenticationConfig{
		Users:               map[uint16][]byte{s.user: s.key},
		StatisticsClass:     dnpClass(s.statisticsClass),
		StatisticsThreshold: s.statisticsThreshold,
		// Called for a critical request whose challenge was answered with a
		// valid MAC, just before it is executed.
		Authorize: func(user uint16, function uint8) bool {
			emit(object{{"event", "auth"}, {"user", user}, {"function", function}})
			return true
		},
	}
}

func (s security) master() *master.SecureAuthenticationConfig {
	if !s.on {
		return nil
	}
	return &master.SecureAuthenticationConfig{User: s.user, UpdateKey: s.key}
}

// authenticationFailed recognises the library's authentication error, which
// it keeps in an internal package.
func authenticationFailed(err error) bool {
	return err != nil && strings.Contains(err.Error(), "authentication failed")
}
