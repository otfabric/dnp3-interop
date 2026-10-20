// SPDX-License-Identifier: GPL-3.0-or-later

// Command dnp3-interop is the dnp3-interop adapter for DSC Systems go-dnp3.
//
// It implements the container contract in docs/CONTAINER_CONTRACT.md.
package main

import (
	"io"
	"log/slog"
	"os"
	"slices"
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
         [--select-timeout-ms N] [--confirm-timeout-ms N] [LINK] [TRANSPORT] [SA]
         [--tls-cert PATH --tls-key PATH --tls-ca PATH] [--udp-peer HOST:PORT]
  client <operation> --host HOST [--port N] [--connect-timeout-ms N] [--timeout-ms N]
         [--collect-ms N] [LINK] [TRANSPORT] [SA] [operation flags]
         [--tls --tls-ca PATH --tls-cert PATH --tls-key PATH [--tls-server-name NAME]]
         [--udp-local-port N]
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
      file-get             --name NAME
      file-put             --name NAME --content HEX
      file-ls              --name DIRECTORY
      file-info            --name NAME
      file-delete          --name NAME
  print-capabilities
  print-fixture [NAME]
  version

  LINK: [--local-addr N] [--remote-addr N]
  TRANSPORT: [--transport tcp|udp|serial]
             serial: --serial-device PATH [--baud N] [--data-bits 7|8] [--parity none|even|odd] [--stop-bits 1|2]
  SA: [--sa-update-key HEX [--sa-user N] [--sa-mac hmac-sha256-16]]

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
			{"freezeNoAckServer", true},
			{"freezeNoAckClient", false},
			{"octetStrings", true},
			{"tcp", true},
			{"udp", true},
			{"tls", true},
			{"serial", true},
			{"fileTransfer", true},
			{"fileServer", true},
			{"fileClient", true},
			{"fileDeleteClient", true},
			{"secureAuthentication", true},
			// Contract 1.2. The outstation records command events (g13, g43)
			// when a control reaches the control handler; the master
			// decodes them and frozen analogs, but drops g50v4.
			{"commandEventsServer", true},
			{"commandEventsClient", true},
			{"frozenAnalogInputsClient", true},
			{"timeAndIntervalClient", false},
		}},
		{"pointTypes", append(slices.Clone(pointTypes), "timeAndIntervals")},
		{"commandTypes", []string{"g12v1", "g41v1", "g41v2", "g41v3", "g41v4"}},
		{"clientOperations", []string{"integrity-poll", "event-poll", "read", "operate", "time-sync", "restart",
			"enable-unsolicited", "disable-unsolicited", "freeze", "monitor",
			"file-get", "file-put", "file-ls", "file-info", "file-delete"}},
		{"secureAuthentication", object{
			{"macAlgorithms", supportedMACs},
			{"keyWrapAlgorithms", keyWrap},
			{"criticalFunctions", criticalRequest},
			{"aggressiveMode", false},
		}},
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
