// SPDX-License-Identifier: MIT

//! The outstation: serves a fixture as docs/FIXTURES.md describes.

use crate::fixture::{self, Fixture, Point};
use crate::logf;
use crate::util::*;

use dnp3::app::control::*;
use dnp3::app::measurement::*;
use dnp3::app::*;
use dnp3::link::*;
use dnp3::outstation::database::*;
use dnp3::outstation::*;
use dnp3::tcp::*;

use crate::transport::{self, Kind, Transport};
use dnp3::app::attr::{AttrProp, AttrSet, Attribute, OwnedAttrValue, OwnedAttribute};
use dnp3::serial::*;
use dnp3::tcp::tls::*;
use dnp3::udp::*;
use serde_json::json;
use std::net::{IpAddr, SocketAddr};
use std::path::Path;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::Duration;
use tokio::sync::watch;

fn now() -> Time {
    Time::Synchronized(Timestamp::new(now_ms()))
}

fn event_class(class: u8) -> Option<EventClass> {
    match class {
        1 => Some(EventClass::Class1),
        2 => Some(EventClass::Class2),
        3 => Some(EventClass::Class3),
        _ => None,
    }
}

fn flags(p: &Point) -> Flags {
    Flags { value: p.flags }
}

/// The database of the fixture: points with their variations and classes, and
/// the initial values, which produce no events.
/// The fixture's device attributes (contract 1.3); the first the library
/// refuses, if any.
fn define_attributes(db: &mut Database, fx: &Fixture) -> Option<String> {
    for a in &fx.attributes {
        let value = match &a.value {
            fixture::AttributeValue::Text(t) => OwnedAttrValue::VisibleString(t.clone()),
            fixture::AttributeValue::Unsigned(n) => OwnedAttrValue::UnsignedInt(*n),
            fixture::AttributeValue::Signed(n) => OwnedAttrValue::SignedInt(*n),
        };
        let prop = if a.writable {
            AttrProp::writable()
        } else {
            AttrProp::default()
        };
        let attr = OwnedAttribute::new(AttrSet::new(a.set), a.variation, value);
        if let Err(e) = db.define_attr(prop, attr) {
            return Some(format!(
                "deviceAttributes: set {} variation {}: {e}",
                a.set, a.variation
            ));
        }
    }
    None
}

fn populate(db: &mut Database, fx: &Fixture) {
    let t = now();
    let quiet = UpdateOptions::no_event();
    for p in fx.points("binaryInputs") {
        let s_var = match p.svar {
            1 => StaticBinaryInputVariation::Group1Var1,
            _ => StaticBinaryInputVariation::Group1Var2,
        };
        let e_var = match p.evar {
            1 => EventBinaryInputVariation::Group2Var1,
            _ => EventBinaryInputVariation::Group2Var2,
        };
        db.add(
            p.index,
            event_class(p.class),
            BinaryInputConfig { s_var, e_var },
        );
        db.update(
            p.index,
            &BinaryInput::new(p.value != 0.0, flags(p), t),
            quiet,
        );
    }
    for p in fx.points("doubleBitInputs") {
        let e_var = match p.evar {
            1 => EventDoubleBitBinaryInputVariation::Group4Var1,
            _ => EventDoubleBitBinaryInputVariation::Group4Var2,
        };
        db.add(
            p.index,
            event_class(p.class),
            DoubleBitBinaryInputConfig {
                s_var: StaticDoubleBitBinaryInputVariation::Group3Var2,
                e_var,
            },
        );
        let state = match p.value as u8 {
            0 => DoubleBit::Intermediate,
            1 => DoubleBit::DeterminedOff,
            2 => DoubleBit::DeterminedOn,
            _ => DoubleBit::Indeterminate,
        };
        db.update(
            p.index,
            &DoubleBitBinaryInput::new(state, flags(p), t),
            quiet,
        );
    }
    for p in fx.points("binaryOutputs") {
        let e_var = match p.evar {
            1 => EventBinaryOutputStatusVariation::Group11Var1,
            _ => EventBinaryOutputStatusVariation::Group11Var2,
        };
        db.add(
            p.index,
            event_class(p.class),
            BinaryOutputStatusConfig {
                s_var: StaticBinaryOutputStatusVariation::Group10Var2,
                e_var,
            },
        );
        db.update(
            p.index,
            &BinaryOutputStatus::new(p.value != 0.0, flags(p), t),
            quiet,
        );
    }
    for p in fx.points("counters") {
        let s_var = match p.svar {
            2 => StaticCounterVariation::Group20Var2,
            5 => StaticCounterVariation::Group20Var5,
            6 => StaticCounterVariation::Group20Var6,
            _ => StaticCounterVariation::Group20Var1,
        };
        let e_var = match p.evar {
            2 => EventCounterVariation::Group22Var2,
            5 => EventCounterVariation::Group22Var5,
            6 => EventCounterVariation::Group22Var6,
            _ => EventCounterVariation::Group22Var1,
        };
        db.add(
            p.index,
            event_class(p.class),
            CounterConfig {
                s_var,
                e_var,
                deadband: 0,
            },
        );
        db.update(p.index, &Counter::new(p.value as u32, flags(p), t), quiet);
    }
    for p in fx.points("frozenCounters") {
        let s_var = match p.svar {
            2 => StaticFrozenCounterVariation::Group21Var2,
            5 => StaticFrozenCounterVariation::Group21Var5,
            6 => StaticFrozenCounterVariation::Group21Var6,
            9 => StaticFrozenCounterVariation::Group21Var9,
            10 => StaticFrozenCounterVariation::Group21Var10,
            _ => StaticFrozenCounterVariation::Group21Var1,
        };
        let e_var = match p.evar {
            2 => EventFrozenCounterVariation::Group23Var2,
            5 => EventFrozenCounterVariation::Group23Var5,
            6 => EventFrozenCounterVariation::Group23Var6,
            _ => EventFrozenCounterVariation::Group23Var1,
        };
        db.add(
            p.index,
            event_class(p.class),
            FrozenCounterConfig {
                s_var,
                e_var,
                deadband: 0,
            },
        );
        db.update(
            p.index,
            &FrozenCounter::new(p.value as u32, flags(p), t),
            quiet,
        );
    }
    for p in fx.points("analogInputs") {
        let s_var = match p.svar {
            2 => StaticAnalogInputVariation::Group30Var2,
            3 => StaticAnalogInputVariation::Group30Var3,
            4 => StaticAnalogInputVariation::Group30Var4,
            5 => StaticAnalogInputVariation::Group30Var5,
            6 => StaticAnalogInputVariation::Group30Var6,
            _ => StaticAnalogInputVariation::Group30Var1,
        };
        let e_var = match p.evar {
            2 => EventAnalogInputVariation::Group32Var2,
            3 => EventAnalogInputVariation::Group32Var3,
            4 => EventAnalogInputVariation::Group32Var4,
            5 => EventAnalogInputVariation::Group32Var5,
            6 => EventAnalogInputVariation::Group32Var6,
            7 => EventAnalogInputVariation::Group32Var7,
            8 => EventAnalogInputVariation::Group32Var8,
            _ => EventAnalogInputVariation::Group32Var1,
        };
        db.add(
            p.index,
            event_class(p.class),
            AnalogInputConfig {
                s_var,
                e_var,
                deadband: 0.0,
            },
        );
        db.update(p.index, &AnalogInput::new(p.value, flags(p), t), quiet);
    }
    for p in fx.points("analogOutputs") {
        let s_var = match p.svar {
            2 => StaticAnalogOutputStatusVariation::Group40Var2,
            3 => StaticAnalogOutputStatusVariation::Group40Var3,
            4 => StaticAnalogOutputStatusVariation::Group40Var4,
            _ => StaticAnalogOutputStatusVariation::Group40Var1,
        };
        let e_var = match p.evar {
            2 => EventAnalogOutputStatusVariation::Group42Var2,
            3 => EventAnalogOutputStatusVariation::Group42Var3,
            4 => EventAnalogOutputStatusVariation::Group42Var4,
            5 => EventAnalogOutputStatusVariation::Group42Var5,
            6 => EventAnalogOutputStatusVariation::Group42Var6,
            7 => EventAnalogOutputStatusVariation::Group42Var7,
            8 => EventAnalogOutputStatusVariation::Group42Var8,
            _ => EventAnalogOutputStatusVariation::Group42Var1,
        };
        db.add(
            p.index,
            event_class(p.class),
            AnalogOutputStatusConfig {
                s_var,
                e_var,
                deadband: 0.0,
            },
        );
        db.update(
            p.index,
            &AnalogOutputStatus::new(p.value, flags(p), t),
            quiet,
        );
    }
    for p in fx.points("octetStrings") {
        db.add(p.index, event_class(p.class), OctetStringConfig);
        if let Ok(value) = OctetString::new(&p.octets) {
            db.update(p.index, &value, quiet);
        }
    }
}

struct Application {
    fx: Arc<Fixture>,
    need_time: Arc<AtomicBool>,
}

impl OutstationApplication for Application {
    fn write_device_attr(&mut self, attr: Attribute) -> MaybeAsync<bool> {
        let (kind, value) = crate::client::attribute_value(&attr.value);
        emit(&json!({
            "event": "attribute-write", "set": attr.set.value(), "variation": attr.variation,
            "type": kind, "value": value,
        }));
        MaybeAsync::ready(true)
    }

    fn write_absolute_time(&mut self, time: Timestamp) -> Result<(), RequestError> {
        self.need_time.store(false, Ordering::SeqCst);
        emit(&json!({"event": "time-sync", "time": format_time(time.raw_value())}));
        Ok(())
    }

    fn get_application_iin(&self) -> ApplicationIin {
        ApplicationIin {
            need_time: self.need_time.load(Ordering::SeqCst),
            ..Default::default()
        }
    }

    fn cold_restart(&mut self) -> Option<RestartDelay> {
        let delay = self.fx.cold_restart_delay_ms?;
        emit(&json!({"event": "restart", "type": "cold"}));
        Some(RestartDelay::Milliseconds(delay))
    }

    fn warm_restart(&mut self) -> Option<RestartDelay> {
        let delay = self.fx.warm_restart_delay_ms?;
        emit(&json!({"event": "restart", "type": "warm"}));
        Some(RestartDelay::Milliseconds(delay))
    }

    /// The library leaves the freeze to the application: every selected
    /// counter is copied to the frozen counter of the same index, if there
    /// is one, and cleared for FREEZE AND CLEAR.
    fn freeze_counter(
        &mut self,
        indices: FreezeIndices,
        freeze_type: FreezeType,
        database: &mut DatabaseHandle,
    ) -> Result<(), RequestError> {
        let clear = match freeze_type {
            FreezeType::ImmediateFreeze => false,
            FreezeType::FreezeAndClear => true,
            _ => return Err(RequestError::NotSupported),
        };
        let fx = self.fx.clone();
        database.transaction(|db| {
            let t = now();
            for p in fx.points("counters") {
                let selected = match indices {
                    FreezeIndices::All => true,
                    FreezeIndices::Range(a, b) => p.index >= a && p.index <= b,
                };
                if !selected {
                    continue;
                }
                let current: Option<Counter> = db.get(p.index);
                let Some(counter) = current else { continue };
                if fx.find("frozenCounters", p.index).is_some() {
                    db.update(
                        p.index,
                        &FrozenCounter::new(counter.value, counter.flags, t),
                        UpdateOptions::detect_event(),
                    );
                }
                if clear {
                    db.update(
                        p.index,
                        &Counter::new(0, counter.flags, t),
                        UpdateOptions::detect_event(),
                    );
                }
            }
        });
        Ok(())
    }
}

struct Information;
impl OutstationInformation for Information {}

struct Controls {
    fx: Arc<Fixture>,
}

fn status_name(s: CommandStatus) -> &'static str {
    command_status_name(s.as_u8())
}

fn function_name(op: OperateType) -> &'static str {
    match op {
        OperateType::SelectBeforeOperate => "operate",
        OperateType::DirectOperate => "direct-operate",
        OperateType::DirectOperateNoAck => "direct-operate-no-ack",
    }
}

fn raw_code(c: &ControlCode) -> u8 {
    (c.tcc.as_u8() << 6)
        | if c.clear { 0x20 } else { 0 }
        | if c.queue { 0x10 } else { 0 }
        | (c.op_type.as_u8() & 0x0f)
}

impl Controls {
    fn check_crob(&self, c: &Group12Var1, index: u16, direct: bool) -> (CommandStatus, bool) {
        let Some(p) = self.fx.find("binaryOutputs", index) else {
            return (CommandStatus::NotSupported, false);
        };
        let value = match raw_code(&c.code) {
            0x03 | 0x41 => true,  // LATCH_ON; PULSE_ON with CLOSE
            0x04 | 0x81 => false, // LATCH_OFF; PULSE_ON with TRIP
            _ => return (CommandStatus::NotSupported, false),
        };
        if direct && p.select_required {
            return (CommandStatus::NotSupported, false);
        }
        (CommandStatus::Success, value)
    }

    fn check_analog(&self, value: f64, index: u16, direct: bool) -> CommandStatus {
        let Some(p) = self.fx.find("analogOutputs", index) else {
            return CommandStatus::NotSupported;
        };
        if p.min.is_some_and(|m| value < m) || p.max.is_some_and(|m| value > m) {
            return CommandStatus::OutOfRange;
        }
        if direct && p.select_required {
            return CommandStatus::NotSupported;
        }
        CommandStatus::Success
    }

    fn crob_event(function: &str, c: &Group12Var1, index: u16, status: CommandStatus) {
        emit(&json!({
            "event": "command", "function": function, "type": "g12v1", "index": index,
            "code": raw_code(&c.code), "count": c.count, "onMs": c.on_time, "offMs": c.off_time,
            "status": status_name(status),
        }));
    }

    fn analog_event(function: &str, kind: &str, value: f64, index: u16, status: CommandStatus) {
        emit(&json!({
            "event": "command", "function": function, "type": kind, "index": index,
            "value": number(value), "status": status_name(status),
        }));
    }

    fn select_analog(&self, value: f64, index: u16, kind: &str) -> CommandStatus {
        let status = self.check_analog(value, index, false);
        Self::analog_event("select", kind, value, index, status);
        status
    }

    fn operate_analog(
        &self,
        value: f64,
        index: u16,
        op: OperateType,
        db: &mut DatabaseHandle,
        kind: &str,
    ) -> CommandStatus {
        let status = self.check_analog(value, index, op != OperateType::SelectBeforeOperate);
        if status == CommandStatus::Success {
            let fx = self.fx.clone();
            db.transaction(|db| {
                let t = now();
                if let Some(p) = fx.find("analogOutputs", index) {
                    db.update(
                        index,
                        &AnalogOutputStatus::new(value, flags(p), t),
                        UpdateOptions::detect_event(),
                    );
                    if let Some(target) = p.target.and_then(|i| fx.find("analogInputs", i)) {
                        db.update(
                            target.index,
                            &AnalogInput::new(value, flags(target), t),
                            UpdateOptions::detect_event(),
                        );
                    }
                }
            });
        }
        Self::analog_event(function_name(op), kind, value, index, status);
        status
    }
}

impl ControlHandler for Controls {}

impl ControlSupport<Group12Var1> for Controls {
    fn select(
        &mut self,
        control: Group12Var1,
        index: u16,
        _db: &mut DatabaseHandle,
    ) -> CommandStatus {
        let (status, _) = self.check_crob(&control, index, false);
        Self::crob_event("select", &control, index, status);
        status
    }

    fn operate(
        &mut self,
        control: Group12Var1,
        index: u16,
        op: OperateType,
        db: &mut DatabaseHandle,
    ) -> CommandStatus {
        let (status, value) =
            self.check_crob(&control, index, op != OperateType::SelectBeforeOperate);
        if status == CommandStatus::Success {
            let fx = self.fx.clone();
            db.transaction(|db| {
                let t = now();
                if let Some(p) = fx.find("binaryOutputs", index) {
                    db.update(
                        index,
                        &BinaryOutputStatus::new(value, flags(p), t),
                        UpdateOptions::detect_event(),
                    );
                    if let Some(target) = p.target.and_then(|i| fx.find("binaryInputs", i)) {
                        db.update(
                            target.index,
                            &BinaryInput::new(value, flags(target), t),
                            UpdateOptions::detect_event(),
                        );
                    }
                }
            });
        }
        Self::crob_event(function_name(op), &control, index, status);
        status
    }
}

macro_rules! analog_support {
    ($group:ty, $kind:expr) => {
        impl ControlSupport<$group> for Controls {
            fn select(
                &mut self,
                control: $group,
                index: u16,
                _db: &mut DatabaseHandle,
            ) -> CommandStatus {
                self.select_analog(control.value as f64, index, $kind)
            }

            fn operate(
                &mut self,
                control: $group,
                index: u16,
                op: OperateType,
                db: &mut DatabaseHandle,
            ) -> CommandStatus {
                self.operate_analog(control.value as f64, index, op, db, $kind)
            }
        }
    };
}

analog_support!(Group41Var1, "g41v1");
analog_support!(Group41Var2, "g41v2");
analog_support!(Group41Var3, "g41v3");
analog_support!(Group41Var4, "g41v4");

/// Reports the connections of an outstation that dials its master.
struct ClientEvents {
    open: bool,
}

impl Listener<ClientState> for ClientEvents {
    fn update(&mut self, value: ClientState) -> MaybeAsync<()> {
        let open = value == ClientState::Connected;
        if open && !self.open {
            emit(&json!({"event": "connection-opened"}));
        } else if !open && self.open {
            emit(&json!({"event": "connection-closed"}));
        }
        self.open = open;
        MaybeAsync::ready(())
    }
}

struct PortListener {
    tx: watch::Sender<bool>,
}

impl Listener<PortState> for PortListener {
    fn update(&mut self, value: PortState) -> MaybeAsync<()> {
        let _ = self.tx.send(matches!(value, PortState::Open));
        MaybeAsync::ready(())
    }
}

struct ConnectionListener {
    open: bool,
}

impl Listener<ConnectionState> for ConnectionListener {
    fn update(&mut self, value: ConnectionState) -> MaybeAsync<()> {
        let open = value == ConnectionState::Connected;
        if open && !self.open {
            emit(&json!({"event": "connection-opened"}));
        } else if !open && self.open {
            emit(&json!({"event": "connection-closed"}));
        }
        self.open = open;
        MaybeAsync::ready(())
    }
}

pub fn run(argv: &[String]) -> i32 {
    let parsed = (|| -> Result<_, UsageError> {
        let mut flags = vec![
            "fixture",
            "bind-address",
            "port",
            "ready-file",
            "select-timeout-ms",
            "confirm-timeout-ms",
            "local-addr",
            "remote-addr",
        ];
        flags.extend(transport::SERVER_FLAGS);
        let args = Args::parse(argv, &flags, &[])?;
        let transport = transport::parse(&args, true)?;
        let bind: IpAddr = match args.str("bind-address", "0.0.0.0").parse() {
            Ok(a) => a,
            Err(_) => return usage("--bind-address must be an IP address"),
        };
        Ok((
            transport,
            args.str("fixture", DEFAULT_FIXTURE),
            bind,
            args.integer("port", 20000, 1, 65535)? as u16,
            args.str("ready-file", DEFAULT_READY_FILE),
            args.integer("select-timeout-ms", 5000, 1, 3_600_000)? as u64,
            args.integer("confirm-timeout-ms", 5000, 1, 3_600_000)? as u64,
            if args.has("local-addr") {
                Some(args.integer("local-addr", 0, 0, 65519)? as u16)
            } else {
                None
            },
            if args.has("remote-addr") {
                Some(args.integer("remote-addr", 0, 0, 65519)? as u16)
            } else {
                None
            },
        ))
    })();
    let (
        transport,
        fixture_path,
        bind,
        port,
        ready_file,
        select_timeout,
        confirm_timeout,
        local,
        remote,
    ) = match parsed {
        Ok(v) => v,
        Err(UsageError(m)) => {
            logf!("server: {m}");
            return EXIT_USAGE;
        }
    };

    let fx = match std::fs::read_to_string(&fixture_path)
        .map_err(|e| format!("cannot read: {e}"))
        .and_then(|text| fixture::load(&text))
    {
        Ok(fx) => Arc::new(fx),
        Err(e) => {
            logf!("server: fixture {fixture_path}: {e}");
            return EXIT_FIXTURE_INVALID;
        }
    };
    let local = local.unwrap_or(fx.address);
    let remote = remote.unwrap_or(fx.master_address);

    let runtime = match tokio::runtime::Runtime::new() {
        Ok(r) => r,
        Err(e) => {
            logf!("server: {e}");
            return EXIT_FAILED;
        }
    };
    runtime.block_on(serve(
        transport,
        fx,
        bind,
        port,
        ready_file,
        select_timeout,
        confirm_timeout,
        local,
        remote,
    ))
}

#[allow(clippy::too_many_arguments)]
async fn serve(
    transport: Transport,
    fx: Arc<Fixture>,
    bind: IpAddr,
    port: u16,
    ready_file: String,
    select_timeout: u64,
    confirm_timeout: u64,
    local: u16,
    remote: u16,
) -> i32 {
    let (Ok(outstation_address), Ok(master_address)) = (
        EndpointAddress::try_new(local),
        EndpointAddress::try_new(remote),
    ) else {
        logf!("server: invalid link address");
        return EXIT_USAGE;
    };
    let mut config = OutstationConfig::new(
        outstation_address,
        master_address,
        EventBufferConfig::all_types(1000),
    );
    config.class_zero.octet_string = true;
    config.features.unsolicited = if fx.unsolicited {
        Feature::Enabled
    } else {
        Feature::Disabled
    };
    if let Ok(t) = Timeout::from_millis(select_timeout) {
        config.select_timeout = t;
    }
    if let Ok(t) = Timeout::from_millis(confirm_timeout) {
        config.confirm_timeout = t;
    }
    config.keep_alive_timeout = None;
    config.decode_level = crate::decode_level();

    let need_time = Arc::new(AtomicBool::new(fx.need_time));
    let application = Box::new(Application {
        fx: fx.clone(),
        need_time,
    });
    let controls = Box::new(Controls { fx: fx.clone() });
    let address = match (&transport.connect, transport.kind) {
        (Some(addr), _) => addr.clone(),
        (None, Kind::Serial) => transport.device.clone(),
        _ => format!("{bind}:{port}"),
    };
    // What has to live as long as the outstation runs.
    let mut server_handle = None;
    let outstation = match (transport.kind, &transport.tls) {
        (Kind::Tcp, tls) if transport.connect.is_some() => {
            // The outstation dials the master and redials after a
            // connection ends.
            let endpoints = EndpointList::single(address.clone());
            let strategy = ConnectStrategy::new(
                Duration::from_millis(100),
                Duration::from_secs(1),
                Duration::from_millis(100),
            );
            let listener = Box::new(ClientEvents { open: false });
            let mut outstation = match tls {
                Some(tls) => {
                    let host = address
                        .rsplit_once(':')
                        .map(|(h, _)| h.to_string())
                        .unwrap_or_default();
                    let tls_config = match TlsClientConfig::full_pki(
                        Some(tls.server_name.clone().unwrap_or(host)),
                        Path::new(&tls.ca),
                        Path::new(&tls.cert),
                        Path::new(&tls.key),
                        None,
                        MinTlsVersion::V12,
                    ) {
                        Ok(c) => c,
                        Err(e) => {
                            logf!("server: TLS: {e}");
                            return EXIT_FAILED;
                        }
                    };
                    spawn_outstation_tls_client(
                        LinkErrorMode::Close,
                        endpoints,
                        strategy,
                        ConnectOptions::default(),
                        config,
                        application,
                        Box::new(Information),
                        controls,
                        listener,
                        tls_config,
                    )
                }
                None => spawn_outstation_tcp_client(
                    LinkErrorMode::Close,
                    endpoints,
                    strategy,
                    ConnectOptions::default(),
                    config,
                    application,
                    Box::new(Information),
                    controls,
                    listener,
                ),
            };
            if outstation.enable().await.is_err() {
                logf!("server: the outstation task ended");
                return EXIT_FAILED;
            }
            outstation
        }
        (Kind::Serial, _) => {
            let (tx, mut rx) = watch::channel(false);
            let outstation = spawn_outstation_serial_2(
                &transport.device,
                transport.serial,
                config,
                RetryStrategy::new(Duration::from_millis(100), Duration::from_secs(1)),
                application,
                Box::new(Information),
                controls,
                Box::new(PortListener { tx }),
            );
            let open =
                tokio::time::timeout(Duration::from_secs(5), rx.wait_for(|open| *open)).await;
            if !matches!(open, Ok(Ok(_))) {
                logf!("server: cannot open {}", transport.device);
                return EXIT_FAILED;
            }
            outstation
        }
        (Kind::Udp, _) => {
            // Without a peer, unsolicited responses go to the discard port:
            // there is no master to send them to.
            let remote_endpoint = match &transport.udp_peer {
                Some(peer) => match transport::resolve(peer) {
                    Some(a) => a,
                    None => {
                        logf!("server: cannot resolve --udp-peer {peer}");
                        return EXIT_FAILED;
                    }
                },
                None => SocketAddr::from(([127, 0, 0, 1], 9)),
            };
            if std::net::UdpSocket::bind(SocketAddr::new(bind, port)).is_err() {
                logf!("server: cannot bind {address}/udp");
                return EXIT_FAILED;
            }
            let outstation = spawn_outstation_udp(
                OutstationUdpConfig {
                    local_endpoint: SocketAddr::new(bind, port),
                    remote_endpoint,
                    socket_mode: UdpSocketMode::OneToMany,
                    link_read_mode: LinkReadMode::Datagram,
                    retry_delay: Timeout::from_secs(1).expect("valid timeout"),
                },
                config,
                application,
                Box::new(Information),
                controls,
            );
            let deadline = std::time::Instant::now() + Duration::from_secs(5);
            while !transport::udp_bound(port) {
                if std::time::Instant::now() > deadline {
                    logf!("server: not bound to {address}/udp after 5 s");
                    return EXIT_FAILED;
                }
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
            outstation
        }
        (Kind::Tcp, tls) => {
            let mut server = match tls {
                Some(tls) => {
                    let tls_config = match TlsServerConfig::full_pki(
                        None,
                        Path::new(&tls.ca),
                        Path::new(&tls.cert),
                        Path::new(&tls.key),
                        None,
                        MinTlsVersion::V12,
                    ) {
                        Ok(c) => c,
                        Err(e) => {
                            logf!("server: TLS: {e}");
                            return EXIT_FAILED;
                        }
                    };
                    Server::new_tls_server(
                        LinkErrorMode::Close,
                        SocketAddr::new(bind, port),
                        tls_config,
                    )
                }
                None => Server::new_tcp_server(LinkErrorMode::Close, SocketAddr::new(bind, port)),
            };
            let outstation = match server.add_outstation(
                config,
                application,
                Box::new(Information),
                controls,
                Box::new(ConnectionListener { open: false }),
                AddressFilter::Any,
            ) {
                Ok(o) => o,
                Err(e) => {
                    logf!("server: {e}");
                    return EXIT_FAILED;
                }
            };
            match server.bind().await {
                Ok(h) => server_handle = Some(h),
                Err(e) => {
                    logf!("server: cannot listen on {bind}:{port}: {e}");
                    return EXIT_FAILED;
                }
            }
            outstation
        }
    };
    outstation.transaction(|db| populate(db, &fx));
    let refused = outstation.transaction(|db| define_attributes(db, &fx));
    if let Some(e) = refused {
        logf!("server: fixture: {e}");
        return EXIT_FIXTURE_INVALID;
    }

    if let Err(e) = std::fs::write(&ready_file, b"") {
        logf!("server: cannot write the readiness file {ready_file}: {e}");
        return EXIT_FAILED;
    }
    emit(&json!({
        "event": "ready", "adapter": ADAPTER, "address": address,
        "fixture": fx.name, "linkAddress": local, "masterAddress": remote,
        "transport": transport.name(), "dial": transport.connect.is_some(),
    }));

    let mut term = match tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate()) {
        Ok(s) => s,
        Err(e) => {
            logf!("server: {e}");
            return EXIT_FAILED;
        }
    };
    tokio::select! {
        _ = term.recv() => logf!("server: SIGTERM, stopping"),
        _ = tokio::signal::ctrl_c() => logf!("server: SIGINT, stopping"),
    }
    let _ = std::fs::remove_file(&ready_file);
    drop(server_handle); // shuts down the server and the outstation
    drop(outstation);
    tokio::time::sleep(std::time::Duration::from_millis(100)).await;
    emit(&json!({"event": "stopped"}));
    EXIT_OK
}
