// SPDX-License-Identifier: GPL-3.0-or-later

// Command dnp3-interop is the dnp3-interop adapter for DSC Systems go-dnp3.
//
// It implements the container contract in docs/CONTAINER_CONTRACT.md.
package main

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

const (
	adapter          = "dscsystems"
	schemaVersion    = "1.0"
	defaultFixture   = "/fixtures/baseline/fixture.json"
	defaultReadyFile = "/run/dnp3-interop/ready"

	exitOK             = 0
	exitFailed         = 1
	exitUsage          = 2
	exitConnectFailed  = 3
	exitFixtureInvalid = 4
)

// Set by the linker; see the Dockerfile.
var (
	adapterVersion   = "dev"
	upstreamVersion  = "unknown"
	upstreamRevision = "unknown"
)

const usage = `usage: dnp3-interop <command>

  server [--fixture PATH] [--bind-address ADDR] [--port N] [--ready-file PATH]
         [--select-timeout-ms N] [--confirm-timeout-ms N] [LINK]
  client <operation> --host HOST [--port N] [--connect-timeout-ms N] [--timeout-ms N]
         [--collect-ms N] [LINK] [operation flags]
      integrity-poll
      event-poll           [--classes 1,2,3]
      read                 --group G --variation V --start N --stop N   (or --group 60 --variation 1..4)
      operate              --type g12v1|g41v1..4 --index N [--mode direct|sbo|direct-no-ack]
                           g12v1: --code LATCH_ON|LATCH_OFF|PULSE_ON|PULSE_OFF|CLOSE|TRIP
                                  [--count N] [--on-ms N] [--off-ms N]
                           g41:   --value V
      time-sync            [--procedure lan|non-lan]
      restart              [--type cold|warm]
      enable-unsolicited   [--classes 1,2,3]
      disable-unsolicited  [--classes 1,2,3]
      freeze               [--clear]
      monitor              [--duration-ms N]
  print-capabilities
  print-fixture [NAME]
  version

  LINK: [--local-addr N] [--remote-addr N]

See docs/CONTAINER_CONTRACT.md in https://github.com/otfabric/dnp3-interop
`

// libraryLog sends the library's log to stderr: warnings, or everything with
// DNP3_INTEROP_DEBUG=1.
func libraryLog() *slog.Logger {
	level := slog.LevelWarn
	if os.Getenv("DNP3_INTEROP_DEBUG") == "1" {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

func printCapabilities() int {
	emit(object{
		{"schemaVersion", schemaVersion},
		{"adapter", adapter},
		{"adapterVersion", adapterVersion},
		{"protocol", "dnp3"},
		{"upstream", object{
			{"name", "DSC Systems go-dnp3"},
			{"version", upstreamVersion},
			{"revision", upstreamRevision},
			{"license", "GPL-3.0"},
			{"language", "Go"},
			{"independentEngine", true},
		}},
		{"roles", object{{"server", true}, {"client", true}}},
		{"features", object{
			{"integrityPoll", true},
			{"eventPoll", true},
			{"read", true},
			// The master reads start-stop ranges and classes only.
			{"readAllObjectsClient", false},
			{"directOperate", true},
			{"selectBeforeOperate", true},
			// The outstation sets IIN2.2 only for a control of a point that
			// does not exist.
			{"parameterErrorWithNotSupported", false},
			{"directOperateNoAckServer", true},
			{"directOperateNoAckClient", true},
			{"timeSyncLan", true},
			{"timeSyncNonLan", true},
			{"timeSyncOnDemand", true},
			{"restart", true},
			// The master does not hand the announced delay to the caller.
			{"restartDelayClient", false},
			{"unsolicited", true},
			{"assignClassServer", true},
			// The master has no call for ASSIGN CLASS.
			{"assignClassClient", false},
			{"freeze", true},
			// Served, but no master in the self-tests can send it.
			{"freezeNoAckServer", false},
			{"freezeNoAckClient", false},
			{"octetStrings", true},
			{"tcp", true},
			{"udp", false},
			{"tls", false},
			{"serial", false},
			// The library has both; the contract does not yet.
			{"fileTransfer", false},
			{"secureAuthentication", false},
		}},
		{"pointTypes", pointTypes},
		{"commandTypes", []string{"g12v1", "g41v1", "g41v2", "g41v3", "g41v4"}},
		{"clientOperations", []string{"integrity-poll", "event-poll", "read", "operate", "time-sync", "restart",
			"enable-unsolicited", "disable-unsolicited", "freeze", "monitor"}},
	})
	return exitOK
}

func printFixture(name string) int {
	if name == "" || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		logf("print-fixture: '%s' is not a fixture name", name)
		return exitUsage
	}
	f, err := os.Open("/fixtures/" + name + "/fixture.json")
	if err != nil {
		logf("print-fixture: no fixture '%s'", name)
		return exitFailed
	}
	defer f.Close()
	_, _ = io.Copy(os.Stdout, f)
	return exitOK
}

func main() {
	args := os.Args[1:]
	command := "server"
	if len(args) > 0 {
		command, args = args[0], args[1:]
	}
	code := exitUsage
	switch {
	case command == "server":
		code = runServer(args)
	case command == "client":
		code = runClient(args)
	case command == "print-capabilities" && len(args) == 0:
		code = printCapabilities()
	case command == "print-fixture" && len(args) <= 1:
		name := "baseline"
		if len(args) == 1 {
			name = args[0]
		}
		code = printFixture(name)
	case command == "version" && len(args) == 0:
		os.Stdout.WriteString("dnp3-interop " + adapter + " " + adapterVersion + " (go-dnp3 " + upstreamVersion + ", " + upstreamRevision + ")\n")
		code = exitOK
	case command == "help" || command == "--help" || command == "-h":
		os.Stdout.WriteString(usage)
		code = exitOK
	default:
		os.Stderr.WriteString(usage)
	}
	os.Exit(code)
}
