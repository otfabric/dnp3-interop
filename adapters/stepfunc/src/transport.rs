// SPDX-License-Identifier: MIT

//! Transports (contract 1.1): TCP, TLS, UDP and serial, as the library has
//! them.

use crate::util::*;

use dnp3::serial::{DataBits, FlowControl, Parity, SerialSettings, StopBits};
use std::net::{SocketAddr, ToSocketAddrs};

pub const SERVER_FLAGS: [&str; 12] = [
    "connect",
    "tls-server-name",
    "transport",
    "tls-cert",
    "tls-key",
    "tls-ca",
    "udp-peer",
    "serial-device",
    "baud",
    "data-bits",
    "parity",
    "stop-bits",
];
pub const CLIENT_FLAGS: [&str; 12] = [
    "bind-address",
    "transport",
    "tls-ca",
    "tls-cert",
    "tls-key",
    "tls-server-name",
    "udp-local-port",
    "serial-device",
    "baud",
    "data-bits",
    "parity",
    "stop-bits",
];
pub const CLIENT_SWITCHES: [&str; 2] = ["tls", "listen"];
const SERIAL_FLAGS: [&str; 5] = ["serial-device", "baud", "data-bits", "parity", "stop-bits"];

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Kind {
    Tcp,
    Udp,
    Serial,
}

pub struct Tls {
    pub cert: String,
    pub key: String,
    pub ca: String,
    /// The client's --tls-server-name.
    pub server_name: Option<String>,
}

pub struct Transport {
    pub kind: Kind,
    pub tls: Option<Tls>,
    pub udp_peer: Option<String>,
    pub udp_local_port: u16,
    pub device: String,
    pub serial: SerialSettings,
    /// Dual end point (contract 1.2): the outstation dials the master here.
    pub connect: Option<String>,
    /// Dual end point: the master listens for the outstation.
    pub listen: bool,
}

impl Transport {
    /// What the ready event reports.
    pub fn name(&self) -> &'static str {
        match (self.kind, self.tls.is_some()) {
            (Kind::Tcp, true) => "tls",
            (Kind::Tcp, false) => "tcp",
            (Kind::Udp, _) => "udp",
            (Kind::Serial, _) => "serial",
        }
    }
}

pub fn parse(args: &Args, server: bool) -> Result<Transport, UsageError> {
    let kind = match args.str("transport", "tcp").as_str() {
        "tcp" => Kind::Tcp,
        "udp" => Kind::Udp,
        "serial" => Kind::Serial,
        _ => return usage("--transport must be tcp, udp or serial"),
    };
    for n in SERIAL_FLAGS {
        if args.has(n) && kind != Kind::Serial {
            return usage(format!("--{n} needs --transport serial"));
        }
    }
    for n in ["udp-peer", "udp-local-port"] {
        if args.has(n) && kind != Kind::Udp {
            return usage(format!("--{n} needs --transport udp"));
        }
    }

    let mut t = Transport {
        kind,
        tls: None,
        udp_peer: None,
        udp_local_port: 20000,
        device: String::new(),
        serial: SerialSettings::default(),
        connect: None,
        listen: false,
    };
    let files = || Tls {
        cert: args.str("tls-cert", ""),
        key: args.str("tls-key", ""),
        ca: args.str("tls-ca", ""),
        server_name: args
            .has("tls-server-name")
            .then(|| args.str("tls-server-name", "")),
    };
    if server {
        if args.has("connect") {
            let addr = args.str("connect", "");
            if addr.rsplit_once(':').is_none() {
                return usage("--connect must be HOST:PORT");
            }
            for n in ["bind-address", "port"] {
                if args.has(n) {
                    return usage(format!(
                        "--{n} does not go with --connect: the outstation dials"
                    ));
                }
            }
            if kind != Kind::Tcp {
                return usage("--connect needs --transport tcp");
            }
            t.connect = Some(addr);
        } else if args.has("tls-server-name") {
            return usage("--tls-server-name needs --connect");
        }
        if args.has("tls-cert") {
            if !args.has("tls-key") || !args.has("tls-ca") {
                return usage("--tls-cert needs --tls-key and --tls-ca: TLS is mutual");
            }
            t.tls = Some(files());
        } else if args.has("tls-key") || args.has("tls-ca") {
            return usage("--tls-key and --tls-ca need --tls-cert");
        }
        if args.has("udp-peer") {
            let peer = args.str("udp-peer", "");
            if peer.rsplit_once(':').is_none() {
                return usage("--udp-peer must be HOST:PORT");
            }
            t.udp_peer = Some(peer);
        }
    } else {
        t.listen = args.has("listen");
        if t.listen {
            if kind != Kind::Tcp {
                return usage("--listen needs --transport tcp");
            }
            if args.has("host") {
                return usage(
                    "--host does not go with --listen: the master waits for the outstation",
                );
            }
            if args.has("tls") {
                return usage("--listen with --tls: the library's master listens over TCP only");
            }
        } else if args.has("bind-address") {
            return usage("--bind-address needs --listen");
        }
        if args.has("tls") {
            if !args.has("tls-ca") || !args.has("tls-cert") || !args.has("tls-key") {
                return usage("--tls needs --tls-ca, --tls-cert and --tls-key: TLS is mutual");
            }
            t.tls = Some(files());
        } else if ["tls-ca", "tls-cert", "tls-key", "tls-server-name"]
            .iter()
            .any(|n| args.has(n))
        {
            return usage("the --tls-* flags need --tls");
        }
        t.udp_local_port = args.integer("udp-local-port", 20000, 0, 65535)? as u16;
    }
    if t.tls.is_some() && kind != Kind::Tcp {
        return usage("TLS needs --transport tcp");
    }

    if kind == Kind::Serial {
        for n in ["host", "port", "bind-address"] {
            if args.has(n) {
                return usage(format!("--{n} does not go with --transport serial"));
            }
        }
        t.device = args.required("serial-device")?;
        t.serial.baud_rate = args.integer("baud", 9600, 50, 4_000_000)? as u32;
        t.serial.data_bits = match args.integer("data-bits", 8, 7, 8)? {
            7 => DataBits::Seven,
            _ => DataBits::Eight,
        };
        t.serial.parity = match args.str("parity", "none").as_str() {
            "none" => Parity::None,
            "even" => Parity::Even,
            "odd" => Parity::Odd,
            _ => return usage("--parity must be none, even or odd"),
        };
        t.serial.stop_bits = match args.str("stop-bits", "1").as_str() {
            "1" => StopBits::One,
            "2" => StopBits::Two,
            _ => return usage("--stop-bits must be 1 or 2"),
        };
        t.serial.flow_control = FlowControl::None;
    }
    Ok(t)
}

/// Resolves HOST:PORT (a name or an address).
pub fn resolve(address: &str) -> Option<SocketAddr> {
    address.to_socket_addrs().ok()?.next()
}

/// Whether a UDP socket is bound to `port` in this container: the library
/// binds its socket in a task of its own and does not say when, so readiness
/// is read from the kernel's table.
pub fn udp_bound(port: u16) -> bool {
    let wanted = format!(":{port:04X}");
    ["/proc/net/udp", "/proc/net/udp6"].iter().any(|table| {
        std::fs::read_to_string(table).is_ok_and(|text| {
            text.lines().skip(1).any(|line| {
                line.split_whitespace()
                    .nth(1)
                    .is_some_and(|local| local.ends_with(&wanted))
            })
        })
    })
}
