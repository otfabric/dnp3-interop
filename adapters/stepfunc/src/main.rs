// SPDX-License-Identifier: MIT

//! dnp3-interop adapter for the Step Function I/O dnp3 crate. Implements
//! docs/CONTAINER_CONTRACT.md.

// The capability document is one json! literal, deeper than the default
// macro recursion limit.
#![recursion_limit = "256"]

mod client;
mod files;
mod fixture;
mod server;
mod transport;
mod util;

use serde_json::json;
use util::*;

/// Release of this repository, set by the Dockerfile.
const ADAPTER_VERSION: &str = match option_env!("ADAPTER_VERSION") {
    Some(v) => v,
    None => "dev",
};
/// The dnp3 crate version, set by the Dockerfile from Cargo.lock.
const UPSTREAM_VERSION: &str = match option_env!("UPSTREAM_VERSION") {
    Some(v) => v,
    None => "unknown",
};
/// The commit of the release tag.
const UPSTREAM_REVISION: &str = match option_env!("UPSTREAM_REVISION") {
    Some(v) => v,
    None => "unknown",
};

const USAGE: &str = "usage: dnp3-interop <command>

  server [--fixture PATH] [--bind-address ADDR] [--port N] [--ready-file PATH]
         [--select-timeout-ms N] [--confirm-timeout-ms N] [LINK] [TRANSPORT]
         [--tls-cert PATH --tls-key PATH --tls-ca PATH] [--udp-peer HOST:PORT]
         [--connect HOST:PORT [--tls-server-name NAME]]
  client <operation> --host HOST [--port N] [--connect-timeout-ms N] [--timeout-ms N]
         [--collect-ms N] [LINK] [TRANSPORT] [operation flags]
         [--tls --tls-ca PATH --tls-cert PATH --tls-key PATH [--tls-server-name NAME]]
         [--udp-local-port N] [--listen [--bind-address ADDR]]
      integrity-poll
      event-poll           [--classes 1,2,3]
      read                 --group G --variation V [--start N --stop N]
      operate              --type g12v1|g41v1..4 --index N [--mode direct|sbo]
                           g12v1: --code LATCH_ON|LATCH_OFF|PULSE_ON|PULSE_OFF|CLOSE|TRIP
                                  [--count N] [--on-ms N] [--off-ms N]
                           g41:   --value V
      time-sync            [--procedure lan|non-lan]
      restart              [--type cold|warm]
      enable-unsolicited   [--classes 1,2,3]
      disable-unsolicited  [--classes 1,2,3]
      assign-class         --class C --group G [--start N --stop N]
      freeze               [--clear]
      monitor              [--duration-ms N]
      file-get             --name NAME
      file-put             --name NAME --content HEX
      file-ls              --name DIRECTORY
      file-info            --name NAME
      attribute-read       --set N [--variation V]
      attribute-write      --set N --variation V (--text S | --uint N | --int N)
  print-capabilities
  print-fixture [NAME]
  version

  LINK: [--local-addr N] [--remote-addr N]
  TRANSPORT: [--transport tcp|udp|serial]
             serial: --serial-device PATH [--baud N] [--data-bits 7|8] [--parity none|even|odd] [--stop-bits 1|2]

See docs/CONTAINER_CONTRACT.md in https://github.com/otfabric/dnp3-interop
";

/// Library logging goes to stderr: warnings, or everything down to the
/// decoded frames with DNP3_INTEROP_DEBUG=1.
fn debug() -> bool {
    std::env::var("DNP3_INTEROP_DEBUG").as_deref() == Ok("1")
}

pub fn decode_level() -> dnp3::decode::DecodeLevel {
    if debug() {
        dnp3::decode::AppDecodeLevel::ObjectValues.into()
    } else {
        dnp3::decode::DecodeLevel::nothing()
    }
}

fn print_capabilities() -> i32 {
    emit(&json!({
        "schemaVersion": SCHEMA_VERSION,
        "adapter": ADAPTER,
        "adapterVersion": ADAPTER_VERSION,
        "protocol": "dnp3",
        "upstream": {
            "name": "Step Function I/O dnp3",
            "version": UPSTREAM_VERSION,
            "revision": UPSTREAM_REVISION,
            "license": "LicenseRef-StepFunction-NonCommercial",
            "language": "Rust",
            "independentEngine": true,
        },
        "roles": {"server": true, "client": true},
        "features": {
            "integrityPoll": true,
            "eventPoll": true,
            "read": true,
            "readAllObjectsClient": true,
            "directOperate": true,
            "selectBeforeOperate": true,
            // The outstation sets IIN2.2 with every NOT_SUPPORTED status.
            "parameterErrorWithNotSupported": true,
            "directOperateNoAckServer": true,
            // The master never sends it.
            "directOperateNoAckClient": false,
            "timeSyncLan": true,
            "timeSyncNonLan": true,
            "timeSyncOnDemand": true,
            "restart": true,
            "restartDelayClient": true,
            "unsolicited": true,
            // The outstation answers ASSIGN CLASS with NO_FUNC_CODE_SUPPORT.
            "assignClassServer": false,
            "assignClassClient": true,
            "freeze": true,
            "freezeNoAckServer": true,
            // The master always expects a response.
            "freezeNoAckClient": false,
            "octetStrings": true,
            "tcp": true,
            "udp": true,
            "tls": true,
            "serial": true,
            // The master reads, lists, describes and writes files; the
            // library has no DELETE_FILE and no outstation side.
            "fileTransfer": true,
            "fileServer": false,
            "fileClient": true,
            "fileDeleteClient": false,
            // Not in the library.
            "secureAuthentication": false,
            // Contract 1.2. The master decodes frozen analogs and command
            // events; it drops g50v4, and the outstation serves none of them.
            "commandEventsServer": false,
            "commandEventsClient": true,
            "frozenAnalogInputsClient": true,
            "timeAndIntervalClient": false,
            // Dual end point: the outstation dials (TCP and TLS); the master
            // listens over TCP only (the crate has no TLS listener).
            "dualEndpointServer": true,
            "dualEndpointClient": true,
            "dualEndpointClientTls": false,
            // Not in the library: no secure authentication, no g121/g122.
            "securityStatisticsServer": false,
            "securityStatisticsClient": false,
            // Contract 1.3: device attributes, both roles.
            "deviceAttributesServer": true,
            "deviceAttributesClient": true,
            // Several controls in one request: sent; only the first refusal
            // is reported, so commandStatuses is null.
            "multipleControlsClient": true,
            "maxControlsServer": true,
            // FREEZE AT TIME both ways (the outstation schedules it); the
            // outstation freezes counters only, the master sends any group.
            "freezeAtTimeServer": true,
            "freezeAtTimeClient": true,
            "analogFreezeServer": false,
            "analogFreezeClient": true,
        },
        "pointTypes": fixture::TYPES,
        "commandTypes": ["g12v1", "g41v1", "g41v2", "g41v3", "g41v4"],
        "clientOperations": ["integrity-poll", "event-poll", "read", "operate", "time-sync", "restart",
            "enable-unsolicited", "disable-unsolicited", "assign-class", "freeze", "monitor",
            "file-get", "file-put", "file-ls", "file-info", "attribute-read", "attribute-write", "operate-multi"],
    }));
    EXIT_OK
}

fn print_fixture(name: &str) -> i32 {
    if name.is_empty() || name.contains('/') || name.contains("..") {
        logf!("print-fixture: '{name}' is not a fixture name");
        return EXIT_USAGE;
    }
    match std::fs::read(format!("/fixtures/{name}/fixture.json")) {
        Ok(bytes) => {
            use std::io::Write;
            let _ = std::io::stdout().write_all(&bytes);
            EXIT_OK
        }
        Err(_) => {
            logf!("print-fixture: no fixture '{name}'");
            EXIT_FAILED
        }
    }
}

fn main() {
    let level = if debug() {
        tracing_subscriber::filter::LevelFilter::DEBUG
    } else {
        tracing_subscriber::filter::LevelFilter::WARN
    };
    tracing_subscriber::fmt()
        .with_writer(std::io::stderr)
        .with_max_level(level)
        .with_target(false)
        .init();

    let args: Vec<String> = std::env::args().skip(1).collect();
    let command = args.first().map(String::as_str).unwrap_or("server");
    let rest: &[String] = if args.is_empty() { &[] } else { &args[1..] };
    let code = match (command, rest.len()) {
        ("server", _) => server::run(rest),
        ("client", _) => client::run(rest),
        ("print-capabilities", 0) => print_capabilities(),
        ("print-fixture", 0) => print_fixture("baseline"),
        ("print-fixture", 1) => print_fixture(&rest[0]),
        ("version", 0) => {
            println!("dnp3-interop {ADAPTER} {ADAPTER_VERSION} (dnp3 {UPSTREAM_VERSION}, {UPSTREAM_REVISION})");
            EXIT_OK
        }
        ("help" | "--help" | "-h", _) => {
            print!("{USAGE}");
            EXIT_OK
        }
        _ => {
            eprint!("{USAGE}");
            EXIT_USAGE
        }
    };
    std::process::exit(code);
}
