// SPDX-License-Identifier: MIT

// Secure authentication v5 (contract 1.1).

package main

import (
	"crypto/rand"
	"encoding/hex"
	"slices"
	"strings"

	dnp3 "github.com/cB-Sigma/dnp3"
)

var (
	saFlags         = []string{"sa-update-key", "sa-user", "sa-mac"}
	statisticsFlags = []string{"sa-statistics-class", "sa-statistics-threshold"}
	macAlgorithms   = []string{"hmac-sha1-4", "hmac-sha1-8", "hmac-sha1-10", "hmac-sha256-8", "hmac-sha256-16", "aes-gmac"}
	// The MAC algorithms the self-tests exercise with this image. The
	// library computes HMAC-SHA-1 truncated to 8 and 10 octets and
	// HMAC-SHA-256 truncated to 8 too; no test runs them.
	supportedMACs = []string{"hmac-sha256-16"}
	keyWrap       = []string{"aes-128", "aes-256"}
)

// criticalFunctions are the function codes the outstation challenges: the
// library's default, the mandatory critical requests of IEEE 1815-2012
// table 7-7.
func criticalFunctions() []int {
	var out []int
	for fc := range 256 {
		if dnp3.CriticalMandatory(byte(fc)) {
			out = append(out, fc)
		}
	}
	return out
}

type security struct {
	on   bool
	user uint16
	key  []byte
	mac  uint8
}

func parseSecurity(a args, server bool) (security, error) {
	mac := a.str("sa-mac", "hmac-sha256-16")
	if !slices.Contains(macAlgorithms, mac) {
		return security{}, usagef("--sa-mac must be one of %s", strings.Join(macAlgorithms, ", "))
	}
	if !slices.Contains(supportedMACs, mac) {
		return security{}, usagef("--sa-mac %s: this image has %s", mac, strings.Join(supportedMACs, ", "))
	}
	if server {
		for _, n := range statisticsFlags {
			if a.has(n) {
				return security{}, usagef("--%s: the library raises its security statistic events in class 3 at the thresholds of table 7-6, which cannot be set", n)
			}
		}
	}
	if !a.has("sa-update-key") {
		for _, n := range []string{"sa-user", "sa-mac"} {
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
	return security{on: true, user: uint16(user), key: key, mac: dnp3.MACByName(mac)}, nil
}

// outstation is the association's secure authentication. The library
// requires the default user 1; when the configured user is another one,
// user 1 gets a random update key that no master has.
func (s security) outstation() (*dnp3.OutstationAuth, error) {
	if !s.on {
		return nil, nil
	}
	users := []dnp3.OutstationUser{{Number: s.user, Name: "user", UpdateKey: s.key, Role: dnp3.RoleSingleUser}}
	if s.user != dnp3.UserDefault {
		other := make([]byte, len(s.key))
		if _, err := rand.Read(other); err != nil {
			return nil, err
		}
		users = append(users, dnp3.OutstationUser{Number: dnp3.UserDefault, Name: "default", UpdateKey: other, Role: dnp3.RoleViewer})
	}
	return dnp3.NewOutstationAuth(dnp3.OutstationAuthConfig{Users: users, MAL: s.mac, Log: libraryLog()})
}

func (s security) master() (*dnp3.Auth, error) {
	if !s.on {
		return nil, nil
	}
	return dnp3.NewAuth(&dnp3.AuthConfig{User: s.user, UpdateKey: s.key, MAC: s.mac})
}
