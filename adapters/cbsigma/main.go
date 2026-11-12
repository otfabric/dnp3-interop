// SPDX-License-Identifier: MIT

// Command dnp3-interop is the dnp3-interop adapter for cB-Sigma dnp3.
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
	adapter          = "cbsigma"
	schemaVersion    = "1.0"
	defaultFixture   = "/fixtures/baseline/fixture.json"
	fixturesDir      = "/fixtures"
	defaultReadyFile = "/run/dnp3-interop/ready"
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
         [--connect HOST:PORT [--tls-server-name NAME]] [--file-user U --file-password P]
         [--control-socket PATH] [--frozen-event-policy on-change]
  counter-increment --index N [--by D] [--control-socket PATH] [--timeout-ms N]
  client <operation> --host HOST [--port N] [--connect-timeout-ms N] [--timeout-ms N]
         [--collect-ms N] [--link-confirms] [--keep-alive-ms N] [LINK] [TRANSPORT] [SA] [operation flags]
         [--tls --tls-ca PATH --tls-cert PATH --tls-key PATH [--tls-server-name NAME]]
         [--udp-local-port N] [--listen [--bind-address ADDR]]
      integrity-poll
      event-poll           [--classes 1,2,3]
      read                 --group G --variation V [--start N --stop N]
      operate              --type g12v1|g41v1..4 --index N [--mode direct|sbo|direct-no-ack|operate]
                           [--operate-delay-ms N] [--broadcast]
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
      monitor              [--duration-ms N]
      manage               --function initialize-data|initialize-application|start-application|
                                      stop-application|save-configuration|activate-config [--spec NAME]
      deadband-write       --deadbands INDEX:VALUE,...
      file-get             --name NAME
      file-put             --name NAME --content HEX [--append]
      file-ls              --name DIRECTORY
      file-info            --name NAME
      file-delete          --name NAME
                           file operations but file-info: [--file-user U --file-password P]
      file-authenticate    --file-user U --file-password P
      attribute-read       --set N [--variation V]
      attribute-write      --set N --variation V (--text S | --uint N | --int N | --float X | --octets HEX | --bits HEX | --time T)
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

// unsupported says why each feature declared false is missing.
var unsupported = object{
	{"parameterErrorWithNotSupported", "the outstation sets IIN2.2 only for a control of an output that does not exist; a refused control of one that does is echoed NOT_SUPPORTED alone"},
	{"commandEventsServer", "the library's outstation records no command events (g13, g43) and its events carry no command status"},
	{"securityStatisticsServer", "the library serves g121 but raises its g122 events in class 3 at the fixed thresholds of IEEE 1815-2012 table 7-6; --sa-statistics-class and --sa-statistics-threshold cannot be honoured"},
	{"deviceAttributesServer", "the library's outstation serves device attributes of set 0 only, not the private sets of the attributes fixture"},
	{"manageServer", "the library answers INITIALIZE_DATA (15) and SAVE_CONFIGURATION (19) with NO_FUNC_CODE_SUPPORT; 16 to 18 are accepted and reported"},
	{"linkConfirmsServer", "the library's outstation sends its user data as UNCONFIRMED_USER_DATA only"},
	{"analogFreezeServer", "the library freezes analog inputs into frozen analog inputs, but the self-test needs the extended fixture, which this outstation cannot serve (command events)"},
	{"deadbandReadServer", "the library's outstation does not serve a READ of group 34 (OBJECT_UNKNOWN)"},
	{"deadbandReadClient", "the master decodes group 34, but no outstation of the self-tests serves a read of it"},
	{"noConfirmClient", "the library's master confirms every response that asks for confirmation"},
	{"noUnsolicitedConfirmClient", "the library's master confirms every unsolicited response before the application sees it"},
}

func printCapabilities() int {
	features := object{
		{"integrityPoll", true},
		{"eventPoll", true},
		{"read", true},
		{"readAllObjectsClient", true},
		{"directOperate", true},
		{"selectBeforeOperate", true},
		{"parameterErrorWithNotSupported", false},
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
		{"commandEventsServer", false},
		{"commandEventsClient", true},
		{"frozenAnalogInputsClient", true},
		{"timeAndIntervalClient", true},
		{"dualEndpointServer", true},
		{"dualEndpointClient", true},
		{"dualEndpointClientTls", true},
		{"securityStatisticsServer", false},
		{"securityStatisticsClient", true},
		{"deviceAttributesServer", false},
		{"deviceAttributesClient", true},
		{"multipleControlsClient", true},
		{"maxControlsServer", true},
		{"eventBufferServer", true},
		{"linkConfirmsServer", false},
		{"linkConfirmsClient", true},
		{"keepAliveClient", true},
		{"linkStatusEventServer", true},
		{"broadcastServer", true},
		{"broadcastEventServer", true},
		{"broadcastClient", true},
		{"manageServer", false},
		{"manageClient", true},
		{"multipleOutstationsServer", true},
		{"freezeAtTimeServer", true},
		{"freezeAtTimeClient", true},
		{"analogFreezeServer", false},
		{"analogFreezeClient", true},
		{"deadbandWriteServer", true},
		{"deadbandWriteClient", true},
		{"deadbandReadServer", false},
		{"deadbandReadClient", false},
		{"counterIncrementServer", true},
		{"frozenCounterEventsServer", true},
		{"frozenCounterEventTimeClient", true},
		{"activateConfigServer", true},
		{"activateConfigClient", true},
		{"operateDelayClient", true},
		{"operateWithoutSelectClient", true},
		{"noConfirmClient", false},
		{"noUnsolicitedConfirmClient", false},
	}
	emit(object{
		{"schemaVersion", schemaVersion},
		{"adapter", adapter},
		{"adapterVersion", adapterVersion},
		{"protocol", "dnp3"},
		{"upstream", object{
			{"name", "cB-Sigma dnp3"},
			{"version", upstreamVersion},
			{"revision", upstreamRevision},
			{"license", "Apache-2.0"},
			{"language", "Go"},
			{"independentEngine", true},
		}},
		{"roles", object{{"server", true}, {"client", true}}},
		{"features", features},
		// A freeze raises a frozen counter event when it changes the frozen
		// value; every event stays buffered until it is confirmed.
		{"frozenEventPolicies", []string{"on-change"}},
		{"frozenEventRetention", "all"},
		{"unsupportedReasons", unsupported},
		{"pointTypes", append(slices.Clone(pointTypes), "timeAndIntervals")},
		{"commandTypes", []string{"g12v1", "g41v1", "g41v2", "g41v3", "g41v4"}},
		{"clientOperations", append(append(slices.Clone(operations), fileOperations...), attributeOperations...)},
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
	case command == "counter-increment":
		code = runCounterIncrement(args)
	case command == "print-capabilities" && len(args) == 0:
		code = printCapabilities()
	case command == "print-fixture" && len(args) <= 1:
		name := "baseline"
		if len(args) == 1 {
			name = args[0]
		}
		code = printFixture(name)
	case command == "version" && len(args) == 0:
		_, _ = os.Stdout.WriteString("dnp3-interop " + adapter + " " + adapterVersion + " (cB-Sigma dnp3 " + upstreamVersion + ", " + upstreamRevision + ")\n")
		code = exitOK
	case command == "help" || command == "--help" || command == "-h":
		_, _ = os.Stdout.WriteString(usage)
		code = exitOK
	default:
		_, _ = os.Stderr.WriteString(usage)
	}
	os.Exit(code)
}
