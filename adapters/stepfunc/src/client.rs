// SPDX-License-Identifier: MIT

//! The master: one bounded operation per process, one result document.

use crate::logf;
use crate::util::*;

use dnp3::app::control::*;
use dnp3::app::measurement::*;
use dnp3::app::*;
use dnp3::link::*;
use dnp3::master::*;
use dnp3::tcp::*;

use serde_json::{json, Value};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};
use tokio::sync::watch;

use crate::files::{self, FileOp, FileResult};
use crate::transport::{self, Kind, Transport};
use dnp3::serial::*;
use dnp3::tcp::tls::*;
use dnp3::udp::*;
use std::net::SocketAddr;
use std::path::Path;

const OPERATIONS: [&str; 11] = [
    "integrity-poll",
    "event-poll",
    "read",
    "operate",
    "time-sync",
    "restart",
    "enable-unsolicited",
    "disable-unsolicited",
    "assign-class",
    "freeze",
    "monitor",
];

/// Variations with a time stamp; objects of other variations carry no `time`.
const TIMED: [(u8, u8); 28] = [
    (2, 2),
    (2, 3),
    (4, 2),
    (4, 3),
    (11, 2),
    (21, 5),
    (21, 6),
    (22, 5),
    (22, 6),
    (23, 5),
    (23, 6),
    (32, 3),
    (32, 4),
    (32, 7),
    (32, 8),
    (42, 3),
    (42, 4),
    (42, 7),
    (42, 8),
    (13, 2),
    (33, 3),
    (33, 4),
    (33, 7),
    (33, 8),
    (43, 3),
    (43, 4),
    (43, 7),
    (43, 8),
];

/// What the library reports from its tasks.
#[derive(Default)]
struct Received {
    objects: Vec<Value>,
    /// IIN of the last solicited response: (IIN1, IIN2)
    iin: Option<(u8, u8)>,
}

struct Handler {
    received: Arc<Mutex<Received>>,
    unsolicited: bool,
}

/// "g30v5" (the Display of Variation) -> (30, 5)
fn group_variation(v: Variation) -> (u8, u8) {
    let text = v.to_string();
    let parse = || -> Option<(u8, u8)> {
        let rest = text.strip_prefix('g')?;
        let (g, v) = rest.split_once('v')?;
        Some((g.parse().ok()?, v.parse().ok()?))
    };
    parse().unwrap_or((0, 0))
}

impl Handler {
    fn push<T>(
        &mut self,
        info: HeaderInfo,
        iter: &mut dyn Iterator<Item = (T, u16)>,
        value: impl Fn(&T) -> Value,
        flags: impl Fn(&T) -> u8,
        time: impl Fn(&T) -> Option<Time>,
    ) {
        let (group, variation) = group_variation(info.variation);
        let timed = TIMED.contains(&(group, variation));
        let mut r = self.received.lock().unwrap_or_else(|e| e.into_inner());
        for (x, index) in iter {
            let mut o = json!({
                "group": group, "variation": variation, "index": index,
                "value": value(&x), "flags": flag_names(group, flags(&x)),
            });
            if let (true, Some(t)) = (timed, time(&x)) {
                let ts = match t {
                    Time::Synchronized(ts) | Time::Unsynchronized(ts) => ts,
                };
                o["time"] = json!(format_time(ts.raw_value()));
            }
            o["unsolicited"] = json!(self.unsolicited);
            r.objects.push(o);
        }
    }
}

impl Handler {
    /// Command events (g13, g43): the commanded state or value and the
    /// status, without flags.
    fn push_command_events<T>(
        &mut self,
        info: HeaderInfo,
        iter: &mut dyn Iterator<Item = (T, u16)>,
        value: impl Fn(&T) -> Value,
        status: impl Fn(&T) -> CommandStatus,
        time: impl Fn(&T) -> Option<Time>,
    ) {
        let (group, variation) = group_variation(info.variation);
        let timed = TIMED.contains(&(group, variation));
        let mut r = self.received.lock().unwrap_or_else(|e| e.into_inner());
        for (x, index) in iter {
            let mut o = json!({
                "group": group, "variation": variation, "index": index,
                "value": value(&x), "status": command_status_name(status(&x).as_u8()),
            });
            if let (true, Some(Time::Synchronized(ts) | Time::Unsynchronized(ts))) =
                (timed, time(&x))
            {
                o["time"] = json!(format_time(ts.raw_value()));
            }
            o["unsolicited"] = json!(self.unsolicited);
            r.objects.push(o);
        }
    }
}

impl ReadHandler for Handler {
    fn begin_fragment(&mut self, read_type: ReadType, header: ResponseHeader) -> MaybeAsync<()> {
        self.unsolicited = matches!(read_type, ReadType::Unsolicited);
        if !self.unsolicited {
            let mut r = self.received.lock().unwrap_or_else(|e| e.into_inner());
            r.iin = Some((header.iin.iin1.value, header.iin.iin2.value));
        }
        MaybeAsync::ready(())
    }

    fn handle_binary_input(
        &mut self,
        info: HeaderInfo,
        iter: &mut dyn Iterator<Item = (BinaryInput, u16)>,
    ) {
        self.push(
            info,
            iter,
            |x| json!(x.value),
            |x| x.flags.value,
            |x| x.time,
        );
    }

    fn handle_double_bit_binary_input(
        &mut self,
        info: HeaderInfo,
        iter: &mut dyn Iterator<Item = (DoubleBitBinaryInput, u16)>,
    ) {
        let state = |x: &DoubleBitBinaryInput| {
            json!(match x.value {
                DoubleBit::Intermediate => 0,
                DoubleBit::DeterminedOff => 1,
                DoubleBit::DeterminedOn => 2,
                DoubleBit::Indeterminate => 3,
            })
        };
        self.push(info, iter, state, |x| x.flags.value, |x| x.time);
    }

    fn handle_binary_output_status(
        &mut self,
        info: HeaderInfo,
        iter: &mut dyn Iterator<Item = (BinaryOutputStatus, u16)>,
    ) {
        self.push(
            info,
            iter,
            |x| json!(x.value),
            |x| x.flags.value,
            |x| x.time,
        );
    }

    fn handle_counter(&mut self, info: HeaderInfo, iter: &mut dyn Iterator<Item = (Counter, u16)>) {
        self.push(
            info,
            iter,
            |x| json!(x.value),
            |x| x.flags.value,
            |x| x.time,
        );
    }

    fn handle_frozen_counter(
        &mut self,
        info: HeaderInfo,
        iter: &mut dyn Iterator<Item = (FrozenCounter, u16)>,
    ) {
        self.push(
            info,
            iter,
            |x| json!(x.value),
            |x| x.flags.value,
            |x| x.time,
        );
    }

    fn handle_analog_input(
        &mut self,
        info: HeaderInfo,
        iter: &mut dyn Iterator<Item = (AnalogInput, u16)>,
    ) {
        self.push(
            info,
            iter,
            |x| number(x.value),
            |x| x.flags.value,
            |x| x.time,
        );
    }

    fn handle_frozen_analog_input(
        &mut self,
        info: HeaderInfo,
        iter: &mut dyn Iterator<Item = (FrozenAnalogInput, u16)>,
    ) {
        self.push(
            info,
            iter,
            |x| number(x.value),
            |x| x.flags.value,
            |x| x.time,
        );
    }

    fn handle_binary_output_command_event(
        &mut self,
        info: HeaderInfo,
        iter: &mut dyn Iterator<Item = (BinaryOutputCommandEvent, u16)>,
    ) {
        self.push_command_events(
            info,
            iter,
            |x| json!(x.commanded_state),
            |x| x.status,
            |x| x.time,
        );
    }

    fn handle_analog_output_command_event(
        &mut self,
        info: HeaderInfo,
        iter: &mut dyn Iterator<Item = (AnalogOutputCommandEvent, u16)>,
    ) {
        let value = |x: &AnalogOutputCommandEvent| match x.commanded_value {
            AnalogCommandValue::I16(v) => number(f64::from(v)),
            AnalogCommandValue::I32(v) => number(f64::from(v)),
            AnalogCommandValue::F32(v) => number(f64::from(v)),
            AnalogCommandValue::F64(v) => number(v),
        };
        self.push_command_events(info, iter, value, |x| x.status, |x| x.time);
    }

    fn handle_analog_output_status(
        &mut self,
        info: HeaderInfo,
        iter: &mut dyn Iterator<Item = (AnalogOutputStatus, u16)>,
    ) {
        self.push(
            info,
            iter,
            |x| number(x.value),
            |x| x.flags.value,
            |x| x.time,
        );
    }

    fn handle_octet_string<'a>(
        &mut self,
        info: HeaderInfo,
        iter: &'a mut dyn Iterator<Item = (&'a [u8], u16)>,
    ) {
        let (group, _) = group_variation(info.variation);
        let mut r = self.received.lock().unwrap_or_else(|e| e.into_inner());
        for (x, index) in iter {
            r.objects.push(json!({
                "group": group, "variation": x.len(), "index": index,
                "value": to_hex(x), "unsolicited": self.unsolicited,
            }));
        }
    }
}

struct AssocHandler;
impl AssociationHandler for AssocHandler {}

struct AssocInformation;
impl AssociationInformation for AssocInformation {}

/// The master of `client --listen`: hands the channel of the first outstation
/// that connects to the operation.
struct ListenHandler {
    config: MasterChannelConfig,
    channel: Option<tokio::sync::oneshot::Sender<MasterChannel>>,
}

impl ConnectionHandler for ListenHandler {
    async fn accept(&mut self, _addr: SocketAddr) -> Result<AcceptAction, Reject> {
        if self.channel.is_none() {
            return Err(Reject); // one outstation per operation
        }
        Ok(AcceptAction::Accept(AcceptConfig {
            error_mode: LinkErrorMode::Close,
            config: self.config,
        }))
    }

    async fn start(&mut self, channel: MasterChannel, _addr: SocketAddr) {
        if let Some(tx) = self.channel.take() {
            let _ = tx.send(channel);
        }
    }

    async fn accept_link_id(
        &mut self,
        _addr: SocketAddr,
        _source: u16,
        _destination: u16,
    ) -> Result<AcceptConfig, Reject> {
        Err(Reject)
    }

    async fn start_with_link_id(
        &mut self,
        _channel: MasterChannel,
        _addr: SocketAddr,
        _source: u16,
        _destination: u16,
    ) {
    }
}

/// Whether the channel is connected (TCP, TLS) or open (serial).
struct StateListener {
    tx: watch::Sender<bool>,
}

impl Listener<ClientState> for StateListener {
    fn update(&mut self, value: ClientState) -> MaybeAsync<()> {
        let _ = self.tx.send(matches!(value, ClientState::Connected));
        MaybeAsync::ready(())
    }
}

impl Listener<PortState> for StateListener {
    fn update(&mut self, value: PortState) -> MaybeAsync<()> {
        let _ = self.tx.send(matches!(value, PortState::Open));
        MaybeAsync::ready(())
    }
}

/// A variation of a group that a request can name: the default (variation 0)
/// of the point groups and the classes; specific variations for reads.
fn variation(g: u8, v: u8) -> Option<Variation> {
    use Variation::*;
    Some(match (g, v) {
        (1, 0) => Group1Var0,
        (1, 1) => Group1Var1,
        (1, 2) => Group1Var2,
        (2, 0) => Group2Var0,
        (2, 1) => Group2Var1,
        (2, 2) => Group2Var2,
        (2, 3) => Group2Var3,
        (3, 0) => Group3Var0,
        (3, 1) => Group3Var1,
        (3, 2) => Group3Var2,
        (4, 0) => Group4Var0,
        (4, 1) => Group4Var1,
        (4, 2) => Group4Var2,
        (4, 3) => Group4Var3,
        (10, 0) => Group10Var0,
        (10, 1) => Group10Var1,
        (10, 2) => Group10Var2,
        (11, 0) => Group11Var0,
        (11, 1) => Group11Var1,
        (11, 2) => Group11Var2,
        (20, 0) => Group20Var0,
        (20, 1) => Group20Var1,
        (20, 2) => Group20Var2,
        (20, 5) => Group20Var5,
        (20, 6) => Group20Var6,
        (21, 0) => Group21Var0,
        (21, 1) => Group21Var1,
        (21, 2) => Group21Var2,
        (21, 5) => Group21Var5,
        (21, 6) => Group21Var6,
        (21, 9) => Group21Var9,
        (21, 10) => Group21Var10,
        (22, 0) => Group22Var0,
        (22, 1) => Group22Var1,
        (22, 2) => Group22Var2,
        (22, 5) => Group22Var5,
        (22, 6) => Group22Var6,
        (23, 0) => Group23Var0,
        (23, 1) => Group23Var1,
        (23, 2) => Group23Var2,
        (23, 5) => Group23Var5,
        (23, 6) => Group23Var6,
        (30, 0) => Group30Var0,
        (30, 1) => Group30Var1,
        (30, 2) => Group30Var2,
        (30, 3) => Group30Var3,
        (30, 4) => Group30Var4,
        (30, 5) => Group30Var5,
        (30, 6) => Group30Var6,
        (31, 0) => Group31Var0,
        (31, 1) => Group31Var1,
        (31, 2) => Group31Var2,
        (31, 3) => Group31Var3,
        (31, 4) => Group31Var4,
        (31, 5) => Group31Var5,
        (31, 6) => Group31Var6,
        (31, 7) => Group31Var7,
        (31, 8) => Group31Var8,
        (33, 0) => Group33Var0,
        (33, 1) => Group33Var1,
        (33, 2) => Group33Var2,
        (33, 3) => Group33Var3,
        (33, 4) => Group33Var4,
        (33, 5) => Group33Var5,
        (33, 6) => Group33Var6,
        (33, 7) => Group33Var7,
        (33, 8) => Group33Var8,
        (32, 0) => Group32Var0,
        (32, 1) => Group32Var1,
        (32, 2) => Group32Var2,
        (32, 3) => Group32Var3,
        (32, 4) => Group32Var4,
        (32, 5) => Group32Var5,
        (32, 6) => Group32Var6,
        (32, 7) => Group32Var7,
        (32, 8) => Group32Var8,
        (40, 0) => Group40Var0,
        (40, 1) => Group40Var1,
        (40, 2) => Group40Var2,
        (40, 3) => Group40Var3,
        (40, 4) => Group40Var4,
        (42, 0) => Group42Var0,
        (42, 1) => Group42Var1,
        (42, 2) => Group42Var2,
        (42, 3) => Group42Var3,
        (42, 4) => Group42Var4,
        (42, 5) => Group42Var5,
        (42, 6) => Group42Var6,
        (42, 7) => Group42Var7,
        (42, 8) => Group42Var8,
        (60, 1) => Group60Var1,
        (60, 2) => Group60Var2,
        (60, 3) => Group60Var3,
        (60, 4) => Group60Var4,
        (110, 0) => Group110(0),
        (111, 0) => Group111(0),
        _ => return None,
    })
}

fn class_variation(class: u8) -> Variation {
    match class {
        1 => Variation::Group60Var2,
        2 => Variation::Group60Var3,
        3 => Variation::Group60Var4,
        _ => Variation::Group60Var1,
    }
}

/// The operation, ready to run.
enum Op {
    Read(ReadRequest),
    Operate(CommandMode, CommandHeaders),
    TimeSync(TimeSyncProcedure),
    Restart(bool),
    Empty(FunctionCode, Headers),
    Monitor(Duration),
    File(FileOp),
}

struct Failure {
    code: &'static str,
    message: String,
}

fn fail(code: &'static str, message: impl Into<String>) -> Failure {
    Failure {
        code,
        message: message.into(),
    }
}

fn request_error(iin2: u8) -> bool {
    iin2 & 0x07 != 0
}

fn task_failure(e: &TaskError, iin: &mut Option<(u8, u8)>) -> Failure {
    #[allow(unreachable_patterns)]
    match e {
        TaskError::ResponseTimeout => fail("timeout", "no response from the outstation"),
        TaskError::RejectedByIin2(x) => {
            if iin.is_none() {
                *iin = Some((x.iin1.value, x.iin2.value));
            }
            fail(
                "rejected",
                format!("the outstation refused the request: {e}"),
            )
        }
        TaskError::NoConnection
        | TaskError::Link(_)
        | TaskError::Transport
        | TaskError::Shutdown => fail("connection-lost", format!("{e}")),
        _ => fail("failed", format!("{e}")),
    }
}

fn parse_op(op: &str, args: &Args) -> Result<Op, UsageError> {
    let range_headers = |g: u8, v: u8| -> Result<ReadHeader, UsageError> {
        let Some(var) = variation(g, v) else {
            return usage(format!(
                "g{g}v{v} cannot be named in a request by this adapter"
            ));
        };
        if args.has("start") != args.has("stop") {
            return usage("--start and --stop go together");
        }
        if !args.has("start") {
            return Ok(ReadHeader::all_objects(var));
        }
        let a = args.integer("start", 0, 0, 65535)?;
        let b = args.integer("stop", 0, 0, 65535)?;
        if a > b {
            return usage("--start is larger than --stop");
        }
        Ok(if b <= 255 {
            ReadHeader::one_byte_range(var, a as u8, b as u8)
        } else {
            ReadHeader::two_byte_range(var, a as u16, b as u16)
        })
    };
    Ok(match op {
        "integrity-poll" => Op::Read(ReadRequest::class_scan(Classes::all())),
        "event-poll" => {
            let c = args.classes("classes", "1,2,3", 1)?;
            Op::Read(ReadRequest::class_scan(Classes::new(
                false,
                EventClasses::new(c.contains(&1), c.contains(&2), c.contains(&3)),
            )))
        }
        "read" => {
            let g = args.required_integer("group", 0, 255)? as u8;
            let v = args.required_integer("variation", 0, 255)? as u8;
            Op::Read(ReadRequest::multiple_headers(&[range_headers(g, v)?]))
        }
        "operate" => {
            let kind = args.required("type")?;
            let index = args.required_integer("index", 0, 65535)? as u16;
            let mode = match args.str("mode", "direct").as_str() {
                "direct" => CommandMode::DirectOperate,
                "sbo" => CommandMode::SelectBeforeOperate,
                "direct-no-ack" => return usage(
                    "--mode direct-no-ack: the library's master cannot send DIRECT OPERATE NO ACK",
                ),
                _ => return usage("--mode must be direct, sbo or direct-no-ack"),
            };
            let headers = if kind == "g12v1" {
                if args.has("value") {
                    return usage("--value is for g41");
                }
                let code = match args.required("code")?.as_str() {
                    "LATCH_ON" => ControlCode::from_op_type(OpType::LatchOn),
                    "LATCH_OFF" => ControlCode::from_op_type(OpType::LatchOff),
                    "PULSE_ON" => ControlCode::from_op_type(OpType::PulseOn),
                    "PULSE_OFF" => ControlCode::from_op_type(OpType::PulseOff),
                    "CLOSE" => {
                        ControlCode::from_tcc_and_op_type(TripCloseCode::Close, OpType::PulseOn)
                    }
                    "TRIP" => {
                        ControlCode::from_tcc_and_op_type(TripCloseCode::Trip, OpType::PulseOn)
                    }
                    _ => return usage(
                        "--code must be LATCH_ON, LATCH_OFF, PULSE_ON, PULSE_OFF, CLOSE or TRIP",
                    ),
                };
                let crob = Group12Var1::new(
                    code,
                    args.integer("count", 1, 0, 255)? as u8,
                    args.integer("on-ms", 100, 0, 4_294_967_295)? as u32,
                    args.integer("off-ms", 100, 0, 4_294_967_295)? as u32,
                );
                CommandBuilder::single_header_u16(crob, index)
            } else {
                if ["code", "count", "on-ms", "off-ms"]
                    .iter()
                    .any(|f| args.has(f))
                {
                    return usage("--code, --count, --on-ms and --off-ms are for g12v1");
                }
                let value = args.number("value")?;
                let integral = |min: f64, max: f64| -> Result<(), UsageError> {
                    if value != value.floor() || value < min || value > max {
                        return usage(format!("--value is out of the range of {kind}"));
                    }
                    Ok(())
                };
                match kind.as_str() {
                    "g41v1" => {
                        integral(-2_147_483_648.0, 2_147_483_647.0)?;
                        CommandBuilder::single_header_u16(Group41Var1::new(value as i32), index)
                    }
                    "g41v2" => {
                        integral(-32768.0, 32767.0)?;
                        CommandBuilder::single_header_u16(Group41Var2::new(value as i16), index)
                    }
                    "g41v3" => {
                        if value.abs() > f32::MAX as f64 {
                            return usage("--value is out of the range of g41v3");
                        }
                        CommandBuilder::single_header_u16(Group41Var3::new(value as f32), index)
                    }
                    "g41v4" => CommandBuilder::single_header_u16(Group41Var4::new(value), index),
                    _ => return usage("--type must be g12v1, g41v1, g41v2, g41v3 or g41v4"),
                }
            };
            Op::Operate(mode, headers)
        }
        "time-sync" => match args.str("procedure", "lan").as_str() {
            "lan" => Op::TimeSync(TimeSyncProcedure::Lan),
            "non-lan" => Op::TimeSync(TimeSyncProcedure::NonLan),
            _ => return usage("--procedure must be lan or non-lan"),
        },
        "restart" => match args.str("type", "cold").as_str() {
            "cold" => Op::Restart(true),
            "warm" => Op::Restart(false),
            _ => return usage("--type must be cold or warm"),
        },
        "enable-unsolicited" | "disable-unsolicited" => {
            let mut headers = Headers::new();
            for c in args.classes("classes", "1,2,3", 1)? {
                headers = headers.add_all_objects(class_variation(c));
            }
            let fc = if op == "enable-unsolicited" {
                FunctionCode::EnableUnsolicited
            } else {
                FunctionCode::DisableUnsolicited
            };
            Op::Empty(fc, headers)
        }
        "assign-class" => {
            let class = args.required_integer("class", 0, 3)? as u8;
            let g = args.required_integer("group", 0, 255)? as u8;
            let mut headers = Headers::new().add_all_objects(class_variation(class));
            let Some(var) = variation(g, 0) else {
                return usage(format!(
                    "g{g}v0 cannot be named in a request by this adapter"
                ));
            };
            if args.has("start") != args.has("stop") {
                return usage("--start and --stop go together");
            }
            if args.has("start") {
                let a = args.integer("start", 0, 0, 65535)?;
                let b = args.integer("stop", 0, 0, 65535)?;
                if a > b {
                    return usage("--start is larger than --stop");
                }
                headers = if b <= 255 {
                    headers.add_range_8(var, a as u8, b as u8)
                } else {
                    headers.add_range_16(var, a as u16, b as u16)
                };
            } else {
                headers = headers.add_all_objects(var);
            }
            Op::Empty(FunctionCode::AssignClass, headers)
        }
        "freeze" => {
            if args.has("no-ack") {
                return usage(
                    "--no-ack: the library's master always expects a response to a freeze",
                );
            }
            let fc = if args.has("clear") {
                FunctionCode::FreezeClear
            } else {
                FunctionCode::ImmediateFreeze
            };
            Op::Empty(fc, Headers::new().add_all_objects(Variation::Group20Var0))
        }
        "monitor" => {
            Op::Monitor(Duration::from_millis(
                args.integer("duration-ms", 1000, 0, 3_600_000)? as u64,
            ))
        }
        _ => return usage(format!("unknown operation '{op}'")),
    })
}

pub fn run(argv: &[String]) -> i32 {
    let Some(op) = argv.first().map(String::as_str) else {
        logf!("client: missing operation");
        return EXIT_USAGE;
    };
    if !OPERATIONS.contains(&op) && !files::OPERATIONS.contains(&op) {
        logf!("client: unknown operation '{op}'");
        return EXIT_USAGE;
    }
    let mut flags = vec![
        "host",
        "port",
        "local-addr",
        "remote-addr",
        "connect-timeout-ms",
        "timeout-ms",
        "collect-ms",
    ];
    flags.extend(transport::CLIENT_FLAGS);
    let mut switches: Vec<&str> = transport::CLIENT_SWITCHES.to_vec();
    match op {
        "file-get" | "file-ls" | "file-info" => flags.push("name"),
        "file-put" => flags.extend(["name", "content"]),
        "event-poll" | "enable-unsolicited" | "disable-unsolicited" => flags.push("classes"),
        "read" => flags.extend(["group", "variation", "start", "stop"]),
        "operate" => flags.extend([
            "type", "index", "code", "count", "on-ms", "off-ms", "value", "mode",
        ]),
        "time-sync" => flags.push("procedure"),
        "restart" => flags.push("type"),
        "assign-class" => flags.extend(["class", "group", "start", "stop"]),
        "freeze" => switches.extend(["clear", "no-ack"]),
        "monitor" => flags.push("duration-ms"),
        _ => {}
    }

    // Everything is parsed before anything is sent.
    let parsed = (|| -> Result<_, UsageError> {
        let args = Args::parse(&argv[1..], &flags, &switches)?;
        let transport = transport::parse(&args, false)?;
        let host = if transport.listen {
            args.str("bind-address", "0.0.0.0")
        } else if transport.kind == Kind::Serial {
            String::new()
        } else {
            args.required("host")?
        };
        Ok((
            transport,
            host,
            args.integer("port", 20000, 1, 65535)? as u16,
            args.integer("local-addr", 1, 0, 65519)? as u16,
            args.integer("remote-addr", 1024, 0, 65519)? as u16,
            args.integer("connect-timeout-ms", 5000, 1, 3_600_000)? as u64,
            args.integer("timeout-ms", 5000, 1, 3_600_000)? as u64,
            args.integer("collect-ms", 0, 0, 3_600_000)? as u64,
            if files::OPERATIONS.contains(&op) {
                Op::File(files::parse(op, &args)?)
            } else {
                parse_op(op, &args)?
            },
        ))
    })();
    let (transport, host, port, local, remote, connect_timeout, timeout, collect, operation) =
        match parsed {
            Ok(v) => v,
            Err(UsageError(m)) => {
                logf!("client {op}: {m}");
                return EXIT_USAGE;
            }
        };

    let started = Instant::now();
    let runtime = match tokio::runtime::Runtime::new() {
        Ok(r) => r,
        Err(e) => {
            logf!("client: {e}");
            return EXIT_FAILED;
        }
    };
    let received = Arc::new(Mutex::new(Received::default()));
    let mut file_result = FileResult::default();
    let (connected, failure, command_status, restart_delay) = runtime.block_on(execute(
        transport,
        &mut file_result,
        host,
        port,
        local,
        remote,
        Duration::from_millis(connect_timeout),
        Duration::from_millis(timeout),
        Duration::from_millis(collect),
        operation,
        received.clone(),
    ));

    let r = received.lock().unwrap_or_else(|e| e.into_inner());
    let reads = matches!(op, "integrity-poll" | "event-poll" | "read");
    let mut doc = json!({
        "schemaVersion": SCHEMA_VERSION,
        "adapter": ADAPTER,
        "operation": op,
        "ok": failure.is_none(),
        "error": failure.as_ref().map(|f| json!({"code": f.code, "message": f.message})),
        "connected": connected,
        "iin": if reads { r.iin.map(|(a, b)| iin_names(a, b)) } else { None },
        "objects": r.objects,
    });
    if op == "operate" {
        doc["commandStatus"] = json!(command_status.map(command_status_name));
    }
    if op == "restart" {
        doc["restartDelayMs"] = json!(restart_delay);
    }
    if files::OPERATIONS.contains(&op) {
        if let Some(f) = file_result.files.take() {
            doc["files"] = f;
        } else if let Some(f) = file_result.file.take() {
            doc["file"] = f;
        }
        doc["fileStatus"] = json!(file_result.status);
    }
    doc["elapsedMs"] = json!(started.elapsed().as_millis() as u64);
    emit(&doc);

    match failure {
        None => EXIT_OK,
        Some(f) if f.code == "connect-failed" => EXIT_CONNECT_FAILED,
        Some(_) => EXIT_FAILED,
    }
}

#[allow(clippy::too_many_arguments)]
async fn execute(
    transport: Transport,
    file_result: &mut FileResult,
    host: String,
    port: u16,
    local: u16,
    remote: u16,
    connect_timeout: Duration,
    timeout: Duration,
    collect: Duration,
    operation: Op,
    received: Arc<Mutex<Received>>,
) -> (bool, Option<Failure>, Option<u8>, Option<u64>) {
    let (Ok(master_address), Ok(outstation_address)) = (
        EndpointAddress::try_new(local),
        EndpointAddress::try_new(remote),
    ) else {
        return (
            false,
            Some(fail("failed", "invalid link address")),
            None,
            None,
        );
    };
    let mut config = MasterChannelConfig::new(master_address);
    config.decode_level = crate::decode_level();
    let (tx, mut rx) = watch::channel(false);
    let address = match transport.kind {
        Kind::Serial => transport.device.clone(),
        _ => format!("{host}:{port}"),
    };
    // What has to live as long as the operation: the listening socket.
    let mut _server = None;
    let strategy = ConnectStrategy::new(
        Duration::from_millis(100),
        Duration::from_secs(1),
        Duration::from_millis(100),
    );
    let mut udp_destination = None;
    let mut channel = match (transport.kind, &transport.tls) {
        (Kind::Tcp, _) if transport.listen => {
            // The master waits for the outstation to connect.
            let Some(local) = transport::resolve(&address) else {
                return (
                    false,
                    Some(fail("failed", format!("cannot resolve {address}"))),
                    None,
                    None,
                );
            };
            let (channel_tx, channel_rx) = tokio::sync::oneshot::channel();
            let handler = ListenHandler {
                config,
                channel: Some(channel_tx),
            };
            match spawn_master_tcp_server(local, LinkIdConfig::new(), handler).await {
                Ok(h) => _server = Some(h),
                Err(e) => {
                    return (
                        false,
                        Some(fail("failed", format!("cannot listen on {address}: {e}"))),
                        None,
                        None,
                    )
                }
            }
            match tokio::time::timeout(connect_timeout, channel_rx).await {
                Ok(Ok(channel)) => {
                    let _ = tx.send(true);
                    channel
                }
                _ => {
                    return (
                        false,
                        Some(fail(
                            "connect-failed",
                            format!("no outstation connected to {address}"),
                        )),
                        None,
                        None,
                    )
                }
            }
        }
        (Kind::Serial, _) => spawn_master_serial(
            config,
            &transport.device,
            transport.serial,
            Duration::from_millis(500),
            Box::new(StateListener { tx }),
        ),
        (Kind::Udp, _) => {
            // No connection to wait for: the channel is "connected" once its
            // socket exists. An outstation that cannot be resolved is not
            // there to connect to.
            let Some(destination) = transport::resolve(&address) else {
                return (
                    false,
                    Some(fail("connect-failed", format!("cannot resolve {address}"))),
                    None,
                    None,
                );
            };
            udp_destination = Some(destination);
            let _ = tx.send(true);
            spawn_master_udp(
                SocketAddr::from(([0, 0, 0, 0], transport.udp_local_port)),
                LinkReadMode::Datagram,
                Timeout::from_secs(1).expect("valid timeout"),
                config,
            )
        }
        (Kind::Tcp, Some(tls)) => {
            let name = tls.server_name.clone().unwrap_or_else(|| host.clone());
            let tls_config = match TlsClientConfig::full_pki(
                Some(name),
                Path::new(&tls.ca),
                Path::new(&tls.cert),
                Path::new(&tls.key),
                None,
                MinTlsVersion::V12,
            ) {
                Ok(c) => c,
                Err(e) => return (false, Some(fail("failed", format!("TLS: {e}"))), None, None),
            };
            spawn_master_tls_client_2(
                LinkErrorMode::Close,
                config,
                EndpointList::single(address.clone()),
                strategy,
                ConnectOptions::default(),
                Box::new(StateListener { tx }),
                tls_config,
            )
        }
        (Kind::Tcp, None) => spawn_master_tcp_client_2(
            LinkErrorMode::Close,
            config,
            EndpointList::single(address.clone()),
            strategy,
            ConnectOptions::default(),
            Box::new(StateListener { tx }),
        ),
    };

    let mut assoc_config = AssociationConfig::quiet();
    if let Ok(t) = Timeout::from_duration(timeout) {
        assoc_config.response_timeout = t;
    }
    let handler = Handler {
        received: received.clone(),
        unsolicited: false,
    };
    let added = match udp_destination {
        Some(destination) => {
            channel
                .add_udp_association(
                    outstation_address,
                    destination,
                    assoc_config,
                    Box::new(handler),
                    Box::new(AssocHandler),
                    Box::new(AssocInformation),
                )
                .await
        }
        None => {
            channel
                .add_association(
                    outstation_address,
                    assoc_config,
                    Box::new(handler),
                    Box::new(AssocHandler),
                    Box::new(AssocInformation),
                )
                .await
        }
    };
    let mut association = match added {
        Ok(a) => a,
        Err(e) => return (false, Some(fail("failed", format!("{e}"))), None, None),
    };
    if let Err(e) = channel.enable().await {
        return (false, Some(fail("failed", format!("{e}"))), None, None);
    }

    let connected = tokio::time::timeout(connect_timeout, async {
        loop {
            if *rx.borrow_and_update() {
                return true;
            }
            if rx.changed().await.is_err() {
                return false;
            }
        }
    })
    .await
    .unwrap_or(false);
    if !connected {
        return (
            false,
            Some(fail(
                "connect-failed",
                format!("no connection to {address}"),
            )),
            None,
            None,
        );
    }

    let mut command_status = None;
    let mut restart_delay = None;
    let mut iin_from_error = None;
    // The library bounds every request by the response timeout; this only
    // guards against a task that never reports. A file transfer is many
    // requests.
    let guard = if matches!(operation, Op::File(_)) {
        timeout * 20 + Duration::from_secs(2)
    } else {
        timeout * 3 + Duration::from_secs(2)
    };
    let outcome = tokio::time::timeout(guard, async {
        match operation {
            Op::Read(request) => association
                .read(request)
                .await
                .err()
                .map(|e| task_failure(&e, &mut iin_from_error)),
            Op::Operate(mode, headers) => match association.operate(mode, headers).await {
                Ok(()) => {
                    command_status = Some(0);
                    None
                }
                Err(CommandError::Response(CommandResponseError::BadStatus(s))) => {
                    command_status = Some(s.as_u8());
                    Some(fail(
                        "command-failed",
                        format!("the outstation answered {}", command_status_name(s.as_u8())),
                    ))
                }
                Err(CommandError::Task(e))
                | Err(CommandError::Response(CommandResponseError::Request(e))) => {
                    Some(task_failure(&e, &mut iin_from_error))
                }
                Err(e) => Some(fail(
                    "failed",
                    format!("the outstation's echo did not match the request: {e}"),
                )),
            },
            Op::TimeSync(procedure) => match association.synchronize_time(procedure).await {
                Ok(()) => None,
                Err(TimeSyncError::Task(e)) => Some(task_failure(&e, &mut iin_from_error)),
                Err(TimeSyncError::IinError(_)) => Some(fail(
                    "rejected",
                    "the outstation refused the request (IIN2)",
                )),
                Err(e) => Some(fail("failed", format!("{e}"))),
            },
            Op::Restart(cold) => {
                let result = if cold {
                    association.cold_restart().await
                } else {
                    association.warm_restart().await
                };
                match result {
                    Ok(d) => {
                        restart_delay = Some(d.as_millis() as u64);
                        None
                    }
                    Err(e) => Some(task_failure(&e, &mut iin_from_error)),
                }
            }
            Op::Empty(fc, headers) => match association
                .send_and_expect_empty_response(fc, headers)
                .await
            {
                Ok(()) => None,
                Err(WriteError::Task(e)) => Some(task_failure(&e, &mut iin_from_error)),
                Err(WriteError::IinError(_)) => Some(fail(
                    "rejected",
                    "the outstation refused the request (IIN2)",
                )),
            },
            Op::File(op) => files::run(&mut association, op, file_result)
                .await
                .map(|f| Failure {
                    code: f.code,
                    message: f.message,
                }),
            Op::Monitor(duration) => {
                let lost = tokio::time::timeout(duration, async {
                    loop {
                        if rx.changed().await.is_err() || !*rx.borrow() {
                            return;
                        }
                    }
                })
                .await
                .is_ok();
                lost.then(|| {
                    fail(
                        "connection-lost",
                        "the connection closed during the monitoring",
                    )
                })
            }
        }
    })
    .await;
    let mut failure = match outcome {
        Ok(f) => f,
        Err(_) => Some(fail("timeout", "the operation did not complete")),
    };

    {
        let mut r = received.lock().unwrap_or_else(|e| e.into_inner());
        if r.iin.is_none() {
            r.iin = iin_from_error;
        }
        // A read whose response carries a request error is a refusal, also
        // when the library accepted it.
        if failure.is_none() && r.iin.is_some_and(|(_, iin2)| request_error(iin2)) {
            failure = Some(fail(
                "rejected",
                "the outstation refused the request (IIN2)",
            ));
        }
    }

    if failure.is_none() && !collect.is_zero() {
        tokio::time::sleep(collect).await;
    }
    // The confirm of the last response may still be on its way out; closing at
    // once can lose it, and the outstation then keeps events the master has
    // already reported.
    tokio::time::sleep(Duration::from_millis(200)).await;
    let _ = channel.disable().await;
    drop(association);
    (true, failure, command_status, restart_delay)
}
