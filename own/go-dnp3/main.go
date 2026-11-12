// SPDX-License-Identifier: MIT

// Command dnp3-interop is the dnp3-interop adapter for OT Fabric's own DNP3
// stack, github.com/otfabric/go-dnp3. It is not an independent
// implementation and not a reference: see README.md.
//
// It implements the container contract in docs/CONTAINER_CONTRACT.md.
package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"

	dnp3 "github.com/otfabric/go-dnp3"
)

const (
	adapter          = "otfabric"
	schemaVersion    = "1.0"
	defaultFixture   = "/fixtures/baseline/fixture.json"
	defaultReadyFile = "/run/dnp3-interop/ready"
	fixturesDir      = "/fixtures"
	// readyFileMode: the readiness file is empty and only its existence
	// counts (the health check is test -f), so only its owner needs access.
	readyFileMode = 0o600

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
         [--select-timeout-ms N] [--confirm-timeout-ms N] [--outstations N] [LINK] [TRANSPORT] [SA]
         [--tls-cert PATH --tls-key PATH --tls-ca PATH] [--udp-peer HOST:PORT]
         [--connect HOST:PORT [--tls-server-name NAME]] [--file-user U --file-password P] [--link-confirms]
         [--control-socket PATH] [--frozen-event-policy always]
  counter-increment --index N [--by D] [--control-socket PATH] [--timeout-ms N]
  client <operation> --host HOST [--port N] [--connect-timeout-ms N] [--timeout-ms N]
         [--collect-ms N] [--link-confirms] [--keep-alive-ms N] [--no-confirm] [LINK] [TRANSPORT] [SA] [operation flags]
         [--tls --tls-ca PATH --tls-cert PATH --tls-key PATH [--tls-server-name NAME]]
         [--udp-local-port N] [--listen [--bind-address ADDR]]
      integrity-poll
      event-poll           [--classes 1,2,3]
      read                 --group G --variation V [--start N --stop N]
      operate              --type g12v1|g41v1..4 --index N [--mode direct|sbo|direct-no-ack [--broadcast]|operate] [--operate-delay-ms N] (sbo only)
                           g12v1: --code LATCH_ON|LATCH_OFF|PULSE_ON|PULSE_OFF|CLOSE|TRIP
                                  [--count N] [--on-ms N] [--off-ms N]
                           g41:   --value V
      operate-multi        --controls g12v1:I:CODE,g41vN:I:VALUE,... [--mode direct|sbo]
      time-sync            [--procedure lan|non-lan]
      restart              [--type cold|warm]
      enable-unsolicited   [--classes 1,2,3]
      disable-unsolicited  [--classes 1,2,3]
      assign-class         --class C --group G [--start N --stop N]
      freeze               [--clear] [--no-ack] [--group 20|30] [--at immediately|+MS|EPOCH_MS [--interval-ms N]]
      monitor              [--duration-ms N] [--no-unsolicited-confirm] (not with --no-confirm)
      manage               --function initialize-data|initialize-application|start-application|
                                      stop-application|save-configuration|
                                      activate-config [--spec NAME]
      file-get             --name NAME
      file-put             --name NAME --content HEX [--append]
      file-ls              --name DIRECTORY
      file-info            --name NAME
      file-delete          --name NAME
                           file operations but file-info: [--file-user U --file-password P]
      file-authenticate    --file-user U --file-password P
      attribute-read       --set N [--variation V]
      attribute-write      --set N --variation V (--text S | --uint N | --int N | --float X | --octets HEX | --bits HEX | --time T)
      deadband-write       --deadbands INDEX:VALUE,...
  print-capabilities
  print-fixture [NAME]
  version

  LINK: [--local-addr N] [--remote-addr N]
  TRANSPORT: [--transport tcp|udp|serial]
             serial: --serial-device PATH [--baud N] [--data-bits 7|8] [--parity none|even|odd] [--stop-bits 1|2]
  SA: [--sa-update-key HEX [--sa-user N] [--sa-mac NAME]]
      server: [--sa-statistics-class 1|2|3] [--sa-statistics-threshold N]

See docs/CONTAINER_CONTRACT.md in https://github.com/otfabric/dnp3-interop
`

// libraryLog sends the library's log to stderr: warnings, or everything with
// DNP3_INTEROP_DEBUG=1.
func libraryLog() dnp3.Logger {
	level := slog.LevelWarn
	if os.Getenv("DNP3_INTEROP_DEBUG") == "1" {
		level = slog.LevelDebug
	}
	return dnp3.NewSlogLogger(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

// clientOperations are the operations of the master, in the order of the
// usage text.
var clientOperations = []string{"integrity-poll", "event-poll", "read", "operate", "operate-multi", "time-sync",
	"restart", "enable-unsolicited", "disable-unsolicited", "assign-class", "freeze", "monitor", "manage",
	"file-get", "file-put", "file-ls", "file-info", "file-delete", "file-authenticate",
	"attribute-read", "attribute-write", "deadband-write"}

func printCapabilities() int {
	emit(object{
		{"schemaVersion", schemaVersion},
		{"adapter", adapter},
		{"adapterVersion", adapterVersion},
		{"protocol", "dnp3"},
		{"upstream", object{
			{"name", "go-dnp3"},
			{"version", upstreamVersion},
			{"revision", upstreamRevision},
			{"license", "MIT"},
			{"language", "Go"},
			// OT Fabric's own stack: not independent of the libraries it is
			// meant to test.
			{"independentEngine", false},
		}},
		{"roles", object{{"server", true}, {"client", true}}},
		{"features", object{
			{"integrityPoll", true},
			{"eventPoll", true},
			{"read", true},
			{"readAllObjectsClient", true},
			{"directOperate", true},
			{"selectBeforeOperate", true},
			// The outstation sets IIN2.2 with every NOT_SUPPORTED echo.
			{"parameterErrorWithNotSupported", true},
			{"directOperateNoAckServer", true},
			{"directOperateNoAckClient", true},
			{"timeSyncLan", true},
			{"timeSyncNonLan", true},
			{"timeSyncOnDemand", true},
			{"restart", true},
			{"restartDelayClient", true},
			{"unsolicited", true},
			{"assignClassServer", true},
			{"assignClassClient", true},
			{"freeze", true},
			{"freezeNoAckServer", true},
			{"freezeNoAckClient", true},
			{"octetStrings", true},
			{"tcp", true},
			{"udp", true},
			{"tls", true},
			{"serial", true},
			{"fileTransfer", true},
			{"fileServer", true},
			{"fileClient", true},
			{"fileDeleteClient", true},
			{"fileAuthServer", true},
			{"fileAuthClient", true},
			{"fileAppendServer", true},
			{"fileAppendClient", true},
			{"secureAuthentication", true},
			{"commandEventsServer", true},
			{"commandEventsClient", true},
			{"frozenAnalogInputsClient", true},
			{"timeAndIntervalClient", true},
			{"dualEndpointServer", true},
			{"dualEndpointClient", true},
			{"dualEndpointClientTls", true},
			{"securityStatisticsServer", true},
			{"securityStatisticsClient", true},
			{"deviceAttributesServer", true},
			{"deviceAttributesClient", true},
			{"multipleControlsClient", true},
			{"maxControlsServer", true},
			// The fixture's eventBufferSize is the limit of each point type.
			{"eventBufferServer", true},
			{"linkConfirmsServer", true},
			{"linkConfirmsClient", true},
			{"keepAliveClient", true},
			// The library tells no application of a REQUEST_LINK_STATUS or a
			// broadcast: the adapter takes them from its metrics callbacks.
			{"linkStatusEventServer", true},
			{"broadcastServer", true},
			{"broadcastEventServer", true},
			{"broadcastClient", true},
			{"manageServer", true},
			{"manageClient", true},
			{"multipleOutstationsServer", true},
			{"freezeAtTimeServer", true},
			{"freezeAtTimeClient", true},
			{"analogFreezeServer", true},
			{"analogFreezeClient", true},
			{"deadbandWriteServer", true},
			{"deadbandWriteClient", true},
			{"deadbandReadServer", true},
			{"deadbandReadClient", true},
			{"counterIncrementServer", true},
			{"frozenCounterEventsServer", true},
			{"frozenCounterEventTimeClient", true},
			// Contract 1.5.
			{"activateConfigServer", true},
			{"activateConfigClient", true},
			{"operateDelayClient", true},
			{"operateWithoutSelectClient", true},
			{"noConfirmClient", true},
			{"noUnsolicitedConfirmClient", true},
		}},
		// A freeze raises a frozen counter event at every freeze, also for an
		// unchanged value (go-dnp3 v1.11.0, IEEE 1815-2012 11.9.5.2); there is
		// no setting for it.
		{"frozenEventPolicies", []string{"always"}},
		// Every event stays buffered until confirmed.
		{"frozenEventRetention", "all"},
		{"pointTypes", append(slices.Clone(pointTypes), "timeAndIntervals")},
		{"commandTypes", []string{"g12v1", "g41v1", "g41v2", "g41v3", "g41v4"}},
		{"clientOperations", clientOperations},
		{"secureAuthentication", object{
			{"macAlgorithms", supportedMACs},
			{"keyWrapAlgorithms", keyWrap},
			{"criticalFunctions", criticalFunctions()},
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
	// The name is opened in an os.Root of the fixtures: it cannot leave them.
	root, err := os.OpenRoot(fixturesDir)
	if err != nil {
		logf("print-fixture: no fixtures: %v", err)
		return exitFailed
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(name + "/fixture.json")
	if err != nil {
		logf("print-fixture: no fixture '%s'", name)
		return exitFailed
	}
	_, copyErr := io.Copy(os.Stdout, f)
	if err := f.Close(); err != nil && copyErr == nil {
		copyErr = err
	}
	if copyErr != nil {
		logf("print-fixture: %v", copyErr)
		return exitFailed
	}
	return exitOK
}

func run(args []string) int {
	command := "server"
	if len(args) > 0 {
		command, args = args[0], args[1:]
	}
	switch {
	case command == "server":
		return runServer(args)
	case command == "client":
		return runClient(args)
	case command == "counter-increment":
		return runCounterIncrement(args)
	case command == "print-capabilities" && len(args) == 0:
		return printCapabilities()
	case command == "print-fixture" && len(args) <= 1:
		name := "baseline"
		if len(args) == 1 {
			name = args[0]
		}
		return printFixture(name)
	case command == "version" && len(args) == 0:
		fmt.Printf("dnp3-interop %s %s (go-dnp3 %s, %s)\n", adapter, adapterVersion, upstreamVersion, upstreamRevision)
		return exitOK
	case command == "help" || command == "--help" || command == "-h":
		_, _ = os.Stdout.WriteString(usage)
		return exitOK
	}
	_, _ = os.Stderr.WriteString(usage)
	return exitUsage
}

func main() { os.Exit(run(os.Args[1:])) }
