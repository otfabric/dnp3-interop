# SPDX-License-Identifier: MIT
"""dnp3-interop adapter for dnp3py. Implements docs/CONTAINER_CONTRACT.md.

Both roles. dnp3py's outstation cannot serve the baseline fixture (it has no
double-bit inputs, no octet strings and no per-point variations): `server`
serves the fixtures within its limits, the `dnp3py` fixture, and refuses
others before it listens (exit 4).

dnp3py's master runs the link, transport and application layers and decodes
responses. Its high-level calls cover polls and unsolicited responses; every
other request is built here with its application-layer builder types
(RequestHeader, ObjectHeader, ObjectBlock) and sent through the same runner.
Values come from its decoders, with the group and variation of the block they
arrived in, which the SOE handler interface does not carry.
"""

import asyncio
import json
import logging
import math
import os
import re
import signal
import struct
import sys
import time
from datetime import UTC
from importlib.metadata import version as package_version

from dnp3.application.fragment import ObjectBlock, RequestFragment
from dnp3.application.header import RequestHeader
from dnp3.application.parser import ParseError as FragmentParseError
from dnp3.application.qualifiers import ObjectHeader, PrefixCode, RangeCode
from dnp3.core.enums import CommandStatus, FunctionCode
from dnp3.core.exceptions import DNP3Error
from dnp3.core.flags import AnalogQuality, BinaryQuality, CounterQuality
from dnp3.database import (
    AnalogInputConfig,
    AnalogOutputConfig,
    BinaryInputConfig,
    BinaryOutputConfig,
    CounterConfig,
    Database,
    DatabaseConfig,
    EventBufferConfig,
    EventClass,
)
from dnp3.master.config import MasterConfig
from dnp3.master.handler import DefaultSOEHandler

# The decoders of dnp3py's master. They are module-private: the pin is exact
# (requirements.txt), and the adapter build fails if they move.
from dnp3.master.master import (
    Master,
    _decode_analog,
    _decode_binary,
    _decode_counter,
    _decode_double_bit,
    _decode_object_layout,
    _iter_object_slots,
)
from dnp3.master.tcp_runner import (
    LinkError,
    LinkResetPolicy,
    MasterRunnerError,
    MasterTcpRunner,
    ResponseTimeoutError,
)
from dnp3.objects.layout import layout_for
from dnp3.outstation import (
    CommandResult,
    Outstation,
    OutstationConfig,
    OutstationTcpRunner,
    UnsolicitedConfig,
)
from dnp3.transport.reassembler import ReassemblyError
from dnp3.transport_io.channel import ChannelError

ADAPTER = "dnp3py"
SCHEMA_VERSION = "1.0"
ADAPTER_VERSION = os.environ.get("DNP3_INTEROP_ADAPTER_VERSION", "dev")
UPSTREAM_REVISION = os.environ.get("DNP3_INTEROP_UPSTREAM_REVISION", "unknown")

EXIT_OK, EXIT_FAILED, EXIT_USAGE, EXIT_CONNECT_FAILED = 0, 1, 2, 3

OPERATIONS = [
    "integrity-poll", "event-poll", "read", "operate", "time-sync", "restart", "enable-unsolicited",
    "disable-unsolicited", "assign-class", "freeze", "monitor", "operate-multi",
]

USAGE = """usage: dnp3-interop <command>

  server [--fixture PATH] [--bind-address ADDR] [--port N] [--ready-file PATH]
         [--select-timeout-ms N] [--confirm-timeout-ms N] [--transport tcp] [LINK]
         (serves fixtures within dnp3py's limits: fixtures/dnp3py)
  client <operation> --host HOST [--port N] [--connect-timeout-ms N] [--timeout-ms N]
         [--collect-ms N] [--transport tcp] [LINK] [operation flags]
      integrity-poll
      event-poll           [--classes 1,2,3]
      read                 --group G --variation V [--start N --stop N]
      operate              --type g12v1|g41v1..4 --index N [--mode direct|sbo|direct-no-ack]
                           g12v1: --code LATCH_ON|LATCH_OFF|PULSE_ON|PULSE_OFF|CLOSE|TRIP
                                  [--count N] [--on-ms N] [--off-ms N]
                           g41:   --value V
      time-sync            [--procedure lan|non-lan]
      restart              [--type cold|warm]
      enable-unsolicited   [--classes 1,2,3]
      disable-unsolicited  [--classes 1,2,3]
      assign-class         --class C --group G [--start N --stop N]
      freeze               [--clear] [--no-ack] [--group 20|30] [--at immediately|+MS|EPOCH_MS [--interval-ms N]]
      monitor              [--duration-ms N]
      operate-multi        --controls g12v1:I:CODE,g41vN:I:VALUE,... [--mode direct|sbo]
  print-capabilities
  print-fixture [NAME]
  version

  LINK: [--local-addr N] [--remote-addr N]

See docs/CONTAINER_CONTRACT.md in
https://github.com/otfabric/dnp3-interop
"""

IIN_NAMES = [
    "BROADCAST", "CLASS_1_EVENTS", "CLASS_2_EVENTS", "CLASS_3_EVENTS", "NEED_TIME", "LOCAL_CONTROL",
    "DEVICE_TROUBLE", "DEVICE_RESTART", "NO_FUNC_CODE_SUPPORT", "OBJECT_UNKNOWN", "PARAMETER_ERROR",
    "EVENT_BUFFER_OVERFLOW", "ALREADY_EXECUTING", "CONFIG_CORRUPT", "RESERVED_2", "RESERVED_1",
]
REQUEST_ERRORS = 0x0700  # NO_FUNC_CODE_SUPPORT, OBJECT_UNKNOWN, PARAMETER_ERROR

COMMAND_STATUS = [
    "SUCCESS", "TIMEOUT", "NO_SELECT", "FORMAT_ERROR", "NOT_SUPPORTED", "ALREADY_ACTIVE", "HARDWARE_ERROR",
    "LOCAL", "TOO_MANY_OPS", "NOT_AUTHORIZED", "AUTOMATION_INHIBIT", "PROCESSING_LIMITED", "OUT_OF_RANGE",
    "DOWNSTREAM_LOCAL", "ALREADY_COMPLETE", "BLOCKED", "CANCELLED", "BLOCKED_OTHER_MASTER", "DOWNSTREAM_FAIL",
]

# Variations with a time stamp; objects of other variations carry no "time".
TIMED = {
    (2, 2), (2, 3), (4, 2), (4, 3), (11, 2), (21, 5), (21, 6), (22, 5), (22, 6), (23, 5), (23, 6),
    (32, 3), (32, 4), (32, 7), (32, 8), (42, 3), (42, 4), (42, 7), (42, 8),
}

# Size of a control object, the status octet included.
CONTROL_WIDTH = {(12, 1): 11, (41, 1): 5, (41, 2): 3, (41, 3): 5, (41, 4): 9}

CODES = {"LATCH_ON": 0x03, "LATCH_OFF": 0x04, "PULSE_ON": 0x01, "PULSE_OFF": 0x02, "CLOSE": 0x41, "TRIP": 0x81}

COMMON_FLAGS = ["ONLINE", "RESTART", "COMM_LOST", "REMOTE_FORCED", "LOCAL_FORCED"]


def log(message):
    print(f"[{ADAPTER}] {message}", file=sys.stderr)


def emit(doc):
    sys.stdout.write(json.dumps(doc, separators=(",", ":"), ensure_ascii=False) + "\n")
    sys.stdout.flush()


def format_time(dt):
    dt = dt.astimezone(UTC)
    return dt.strftime("%Y-%m-%dT%H:%M:%S.") + f"{dt.microsecond // 1000:03d}Z"


def number(v):
    """A value with no fractional part is printed as an integer, as every adapter does."""
    if isinstance(v, float) and v.is_integer() and abs(v) < 2**53:
        return int(v)
    return v


def iin_names(bits):
    return [IIN_NAMES[i] for i in range(16) if bits & (1 << i)]


def flag_names(group, flags):
    out = [COMMON_FLAGS[b] for b in range(5) if flags & (1 << b)]
    counter, analog = group in (20, 21, 22, 23), group in (30, 32, 40, 42)
    for bit in (5, 6):
        if not flags & (1 << bit):
            continue
        if counter:
            out.append("ROLLOVER" if bit == 5 else "DISCONTINUITY")
        elif analog:
            out.append("OVER_RANGE" if bit == 5 else "REFERENCE_ERR")
        elif bit == 5 and group in (1, 2, 3, 4):
            out.append("CHATTER_FILTER")
    return out


def status_name(s):
    if s < len(COMMAND_STATUS):
        return COMMAND_STATUS[s]
    return "NON_PARTICIPATING" if s == 126 else "UNDEFINED"


class UsageError(Exception):
    pass


class Args:
    """Command line: "--name value" flags and bare "--name" switches."""

    def __init__(self, argv, flags, switches=()):
        self.values = {}
        i = 0
        while i < len(argv):
            arg = argv[i]
            if not arg.startswith("--"):
                raise UsageError(f"unexpected argument '{arg}'")
            name = arg[2:]
            if name in switches:
                self.values[name] = "true"
            elif name in flags:
                if i + 1 >= len(argv):
                    raise UsageError(f"--{name} needs a value")
                i += 1
                self.values[name] = argv[i]
            else:
                raise UsageError(f"unknown flag --{name}")
            i += 1

    def has(self, name):
        return name in self.values

    def str(self, name, default):
        return self.values.get(name, default)

    def required(self, name):
        if name not in self.values:
            raise UsageError(f"--{name} is required")
        return self.values[name]

    def integer(self, name, default, minimum, maximum):
        if name not in self.values:
            return default
        try:
            v = int(self.values[name], 10)
        except ValueError:
            v = None
        if v is None or not minimum <= v <= maximum:
            raise UsageError(f"--{name} must be an integer in {minimum}..{maximum}")
        return v

    def required_integer(self, name, minimum, maximum):
        self.required(name)
        return self.integer(name, 0, minimum, maximum)

    def number(self, name):
        try:
            v = float(self.required(name))
        except ValueError:
            v = None
        if v is None or not math.isfinite(v):
            raise UsageError(f"--{name} must be a number")
        return v

    def transport(self):
        """--transport: tcp, the one transport dnp3py has; udp and serial need features it lacks."""
        t = self.str("transport", "tcp")
        if t in ("udp", "serial"):
            raise UsageError(f"--transport {t}: this image has TCP only (feature {t} is false)")
        if t != "tcp":
            raise UsageError("--transport must be tcp, udp or serial")
        return t

    def classes(self, name, default, minimum):
        out = []
        for item in self.str(name, default).split(","):
            if len(item) != 1 or not item.isdigit() or not minimum <= int(item) <= 3:
                raise UsageError(f"--{name} is a comma-separated list of classes {minimum}..3")
            out.append(int(item))
        return out


class Failure(Exception):
    def __init__(self, code, message):
        super().__init__(message)
        self.code = code
        self.message = message


class Recorder:
    """Everything the master parsed: objects, the IIN of solicited responses, command echoes, delays."""

    def __init__(self):
        self.objects = []
        self.iin = None
        self.statuses = []
        self.delay_ms = None
        # operate-multi: the statuses of the last request's echo (the OPERATE,
        # or the SELECT when the select failed); None until one is sent.
        self.echo = None

    def record(self, blocks, info):
        unsolicited = info.is_unsolicited
        if not unsolicited:
            self.iin = int(info.iin)
        for block in blocks:
            g, v = block.header.group, block.header.variation
            if g in (12, 41) and (g, v) in CONTROL_WIDTH:
                self._statuses(block, CONTROL_WIDTH[(g, v)])
                continue
            if g == 52 and v in (1, 2) and len(block.data) >= 3:
                layout = _decode_object_layout(block.header.qualifier, block.data)
                if layout is not None:
                    raw = int.from_bytes(block.data[layout.data_offset : layout.data_offset + 2], "little")
                    self.delay_ms = raw * 1000 if v == 1 else raw
                continue
            if g in (110, 111):
                self._octets(block, g, v, unsolicited)
                continue
            wire = layout_for(g, v)
            if wire is None:
                continue
            if g in (1, 2, 10, 11):
                values = [(x.index, x.value, x.quality, x.timestamp) for x in _decode_binary(block, wire, None)]
            elif g in (3, 4):
                values = [(x.index, int(x.state), x.quality, x.timestamp) for x in _decode_double_bit(block, wire, None)]
            elif g in (20, 21, 22, 23):
                values = [(x.index, x.value, x.quality, x.timestamp) for x in _decode_counter(block, wire, None)]
            elif g in (30, 32, 40, 42):
                values = [(x.index, number(x.value), x.quality, x.timestamp) for x in _decode_analog(block, wire, None)]
            else:
                continue
            for index, value, quality, stamp in values:
                o = {"group": g, "variation": v, "index": index, "value": value, "flags": flag_names(g, quality)}
                if (g, v) in TIMED and stamp is not None:
                    o["time"] = format_time(stamp)
                o["unsolicited"] = unsolicited
                self.objects.append(o)

    def _statuses(self, block, width):
        layout = _decode_object_layout(block.header.qualifier, block.data)
        if layout is None:
            return
        for _, offset in _iter_object_slots(layout, block.data, width):
            self.statuses.append(block.data[offset + width - 1])

    def _octets(self, block, g, v, unsolicited):
        layout = _decode_object_layout(block.header.qualifier, block.data)
        if layout is None or v == 0:
            return
        for index, offset in _iter_object_slots(layout, block.data, v):
            self.objects.append({
                "group": g, "variation": v, "index": index,
                "value": block.data[offset : offset + v].hex(), "unsolicited": unsolicited,
            })


class RecordingMaster(Master):
    """dnp3py's master, handing every parsed fragment to a Recorder as well as to its SOE handler."""

    recorder: Recorder | None = None

    def _parse_response_objects(self, objects, info):
        if self.recorder is not None:
            self.recorder.record(objects, info)
        super()._parse_response_objects(objects, info)


# ---- requests ----


def block(group, variation, range_code, data=b"", prefix=PrefixCode.NONE):
    return ObjectBlock(header=ObjectHeader.build(group=group, variation=variation, prefix=prefix, range_code=range_code), data=data)


def all_objects(group, variation):
    return block(group, variation, RangeCode.ALL_OBJECTS)


def start_stop(group, variation, start, stop):
    if stop <= 0xFF:
        return block(group, variation, RangeCode.UINT8_START_STOP, bytes([start, stop]))
    return block(group, variation, RangeCode.UINT16_START_STOP, struct.pack("<HH", start, stop))


def count_one(group, variation, payload):
    """One object with no index: the time objects."""
    return block(group, variation, RangeCode.UINT8_COUNT, b"\x01" + payload)


def control(group, variation, index, payload):
    """One control object with an index prefix."""
    if index <= 0xFF:
        return block(group, variation, RangeCode.UINT8_COUNT, bytes([1, index]) + payload, PrefixCode.UINT8_INDEX)
    return block(group, variation, RangeCode.UINT16_COUNT, struct.pack("<HH", 1, index) + payload, PrefixCode.UINT16_INDEX)


def time48(ms):
    return int(ms).to_bytes(6, "little")


def class_variation(c):
    return 1 + c  # g60v1 is class 0, g60v2..v4 classes 1..3


class Client:
    def __init__(self, master, runner, recorder, timeout):
        self.master, self.runner, self.recorder, self.timeout = master, runner, recorder, timeout

    def fragment(self, function, blocks=()):
        header = RequestHeader.build(function=function, seq=self.master.next_request_sequence())
        return RequestFragment(header=header, objects=tuple(blocks))

    async def request(self, function, blocks=()):
        return await self.runner.request(self.fragment(function, blocks))

    async def send(self, function, blocks=()):
        await self.runner.send(self.fragment(function, blocks))


def parse_operation(op, a):
    """Returns a coroutine function taking a Client, after checking every flag."""
    if op == "integrity-poll":
        return lambda c: c.runner.integrity_poll()
    if op == "event-poll":
        cl = a.classes("classes", "1,2,3", 1)
        return lambda c: c.runner.class_poll(class_1=1 in cl, class_2=2 in cl, class_3=3 in cl)
    if op == "read":
        g, v = a.required_integer("group", 0, 255), a.required_integer("variation", 0, 255)
        if a.has("start") != a.has("stop"):
            raise UsageError("--start and --stop go together")
        if a.has("start"):
            start, stop = a.integer("start", 0, 0, 65535), a.integer("stop", 0, 0, 65535)
            if start > stop:
                raise UsageError("--start is larger than --stop")
            header = start_stop(g, v, start, stop)
        else:
            header = all_objects(g, v)
        return lambda c: c.request(FunctionCode.READ, [header])
    if op == "operate":
        return parse_operate(a)
    if op == "operate-multi":
        return parse_operate_multi(a)
    if op == "time-sync":
        procedure = a.str("procedure", "lan")
        if procedure not in ("lan", "non-lan"):
            raise UsageError("--procedure must be lan or non-lan")
        return time_sync_lan if procedure == "lan" else time_sync_non_lan
    if op == "restart":
        kind = a.str("type", "cold")
        if kind not in ("cold", "warm"):
            raise UsageError("--type must be cold or warm")
        fc = FunctionCode.COLD_RESTART if kind == "cold" else FunctionCode.WARM_RESTART
        return lambda c: c.request(fc)
    if op in ("enable-unsolicited", "disable-unsolicited"):
        headers = [all_objects(60, class_variation(k)) for k in a.classes("classes", "1,2,3", 1)]
        fc = FunctionCode.ENABLE_UNSOLICITED if op == "enable-unsolicited" else FunctionCode.DISABLE_UNSOLICITED
        return lambda c: c.request(fc, headers)
    if op == "assign-class":
        k, g = a.required_integer("class", 0, 3), a.required_integer("group", 0, 255)
        if a.has("start") != a.has("stop"):
            raise UsageError("--start and --stop go together")
        if a.has("start"):
            start, stop = a.integer("start", 0, 0, 65535), a.integer("stop", 0, 0, 65535)
            if start > stop:
                raise UsageError("--start is larger than --stop")
            points = start_stop(g, 0, start, stop)
        else:
            points = all_objects(g, 0)
        headers = [all_objects(60, class_variation(k)), points]
        return lambda c: c.request(FunctionCode.ASSIGN_CLASS, headers)
    if op == "freeze":
        clear, no_ack = a.has("clear"), a.has("no-ack")
        fc = {
            (False, False): FunctionCode.IMMEDIATE_FREEZE,
            (False, True): FunctionCode.IMMEDIATE_FREEZE_NO_ACK,
            (True, False): FunctionCode.FREEZE_CLEAR,
            (True, True): FunctionCode.FREEZE_CLEAR_NO_ACK,
        }[(clear, no_ack)]
        group = a.integer("group", 20, 20, 30)
        if group not in (20, 30):
            raise UsageError("--group must be 20 or 30")
        headers = [all_objects(group, 0)]
        if a.has("at"):
            # FREEZE AT TIME (contract 1.3): g50v2 (time, interval), then the objects.
            if clear:
                raise UsageError("--clear does not go with --at")
            at = a.str("at", "")
            if at == "immediately":
                ms = 0
            elif at.startswith("+") and at[1:].isdigit():
                ms = int(time.time() * 1000) + int(at[1:])
            elif at.isdigit() and int(at) < 2**48:
                ms = int(at)
            else:
                raise UsageError("--at must be immediately, +MS or milliseconds since the epoch")
            interval = a.integer("interval-ms", 0, 0, 2**32 - 1)
            fc = FunctionCode.FREEZE_AT_TIME_NO_ACK if no_ack else FunctionCode.FREEZE_AT_TIME
            headers = [count_one(50, 2, time48(ms) + struct.pack("<I", interval))] + headers
        elif a.has("interval-ms"):
            raise UsageError("--interval-ms needs --at")
        if no_ack:
            return lambda c: c.send(fc, headers)
        return lambda c: c.request(fc, headers)
    raise UsageError(f"unknown operation '{op}'")


def parse_operate(a):
    kind = a.required("type")
    index = a.required_integer("index", 0, 65535)
    mode = a.str("mode", "direct")
    if mode not in ("direct", "sbo", "direct-no-ack"):
        raise UsageError("--mode must be direct, sbo or direct-no-ack")
    if kind == "g12v1":
        if a.has("value"):
            raise UsageError("--value is for g41")
        code = a.required("code")
        if code not in CODES:
            raise UsageError("--code must be LATCH_ON, LATCH_OFF, PULSE_ON, PULSE_OFF, CLOSE or TRIP")
        payload = struct.pack(
            "<BBIIB", CODES[code], a.integer("count", 1, 0, 255), a.integer("on-ms", 100, 0, 2**32 - 1),
            a.integer("off-ms", 100, 0, 2**32 - 1), 0,
        )
        obj = control(12, 1, index, payload)
    else:
        if any(a.has(f) for f in ("code", "count", "on-ms", "off-ms")):
            raise UsageError("--code, --count, --on-ms and --off-ms are for g12v1")
        value = a.number("value")

        def integral(lo, hi):
            if value != int(value) or not lo <= value <= hi:
                raise UsageError(f"--value is out of the range of {kind}")

        if kind == "g41v1":
            integral(-(2**31), 2**31 - 1)
            obj = control(41, 1, index, struct.pack("<iB", int(value), 0))
        elif kind == "g41v2":
            integral(-32768, 32767)
            obj = control(41, 2, index, struct.pack("<hB", int(value), 0))
        elif kind == "g41v3":
            if abs(value) > 3.4028234663852886e38:
                raise UsageError("--value is out of the range of g41v3")
            obj = control(41, 3, index, struct.pack("<fB", value, 0))
        elif kind == "g41v4":
            obj = control(41, 4, index, struct.pack("<dB", value, 0))
        else:
            raise UsageError("--type must be g12v1, g41v1, g41v2, g41v3 or g41v4")

    async def run(c):
        if mode == "direct-no-ack":
            await c.send(FunctionCode.DIRECT_OPERATE_NO_ACK, [obj])
            return
        if mode == "direct":
            await c.request(FunctionCode.DIRECT_OPERATE, [obj])
            return
        await c.request(FunctionCode.SELECT, [obj])
        # The OPERATE follows only a successful SELECT, with no request between.
        if c.recorder.statuses and c.recorder.statuses[-1] == 0 and not (c.recorder.iin or 0) & REQUEST_ERRORS:
            await c.request(FunctionCode.OPERATE, [obj])

    return run


def parse_operate_multi(a):
    """--controls: "g12v1:INDEX:CODE" and "g41vN:INDEX:VALUE", one header each."""
    mode = a.str("mode", "direct")
    if mode not in ("direct", "sbo"):
        raise UsageError("--mode must be direct or sbo")
    objs = []
    for item in a.required("controls").split(","):
        parts = item.split(":")
        if len(parts) != 3:
            raise UsageError("--controls items are TYPE:INDEX:CODE or TYPE:INDEX:VALUE")
        kind, index, arg = parts
        if not index.isdigit() or int(index) > 65535:
            raise UsageError(f"--controls: '{index}' is not a point index")
        index = int(index)
        if kind == "g12v1":
            if arg not in CODES:
                raise UsageError(f"--controls: '{arg}' is not a control code")
            objs.append(control(12, 1, index, struct.pack("<BBIIB", CODES[arg], 1, 100, 100, 0)))
            continue
        try:
            value = float(arg)
        except ValueError:
            raise UsageError(f"--controls: '{arg}' is not a number") from None
        integral = value == int(value) if abs(value) != float("inf") else False
        if kind == "g41v1" and integral and -(2**31) <= value <= 2**31 - 1:
            objs.append(control(41, 1, index, struct.pack("<iB", int(value), 0)))
        elif kind == "g41v2" and integral and -32768 <= value <= 32767:
            objs.append(control(41, 2, index, struct.pack("<hB", int(value), 0)))
        elif kind == "g41v3" and abs(value) <= 3.4028234663852886e38:
            objs.append(control(41, 3, index, struct.pack("<fB", value, 0)))
        elif kind == "g41v4":
            objs.append(control(41, 4, index, struct.pack("<dB", value, 0)))
        else:
            raise UsageError(f"--controls: '{item}' is not a control type with a value in its range")

    async def echo(c, function):
        """Sends the request and keeps the statuses of its echo, and only those, in recorder.echo."""
        c.recorder.echo = []
        before = len(c.recorder.statuses)
        await c.request(function, objs)
        c.recorder.echo = c.recorder.statuses[before:]
        return c.recorder.echo

    async def run(c):
        if mode == "direct":
            await echo(c, FunctionCode.DIRECT_OPERATE)
            return
        selected = await echo(c, FunctionCode.SELECT)
        if len(selected) == len(objs) and not any(selected) and not (c.recorder.iin or 0) & REQUEST_ERRORS:
            await echo(c, FunctionCode.OPERATE)

    run.count = len(objs)
    return run


async def time_sync_lan(c):
    recorded = time.time()
    await c.request(FunctionCode.RECORD_CURRENT_TIME)
    if (c.recorder.iin or 0) & REQUEST_ERRORS:
        return
    await c.request(FunctionCode.WRITE, [count_one(50, 3, time48(recorded * 1000))])


async def time_sync_non_lan(c):
    sent = time.monotonic()
    await c.request(FunctionCode.DELAY_MEASURE)
    received = time.monotonic()
    if (c.recorder.iin or 0) & REQUEST_ERRORS:
        return
    outstation_delay = (c.recorder.delay_ms or 0) / 1000
    one_way = max((received - sent - outstation_delay) / 2, 0)
    await c.request(FunctionCode.WRITE, [count_one(50, 1, time48((time.time() + one_way) * 1000))])


async def monitor(c, seconds):
    end = time.monotonic() + seconds
    while (left := end - time.monotonic()) > 0:
        await c.runner.listen_unsolicited(timeout=left)


async def run_client(op, host, port, local, remote, connect_timeout, timeout, collect, duration, request):
    recorder = Recorder()
    master = RecordingMaster(
        config=MasterConfig(
            address=local, outstation_address=remote, response_timeout=timeout, enable_unsolicited=True,
            startup_integrity_poll=False, disable_unsolicited_on_startup=False, enable_unsolicited_on_startup=False,
            time_sync_on_startup=False,
        ),
        handler=DefaultSOEHandler(),
    )
    master.recorder = recorder
    # Connected means the TCP connection, as for every adapter: no link reset
    # that an outstation with another address would leave unanswered.
    runner = MasterTcpRunner(master=master, host=host, port=port, response_timeout=timeout, link_reset=LinkResetPolicy.NEVER)
    client = Client(master, runner, recorder, timeout)
    connected = False
    failure = None
    try:
        await asyncio.wait_for(runner.open(), connect_timeout)
        connected = True
    # What opening can raise: the channel's ChannelConnectionError and
    # ChannelTimeoutError (both ChannelError), the runner's MasterRunnerError,
    # an OSError the channel does not wrap, and wait_for's TimeoutError.
    except (ChannelError, MasterRunnerError, OSError, TimeoutError) as exc:
        failure = Failure("connect-failed", f"no connection to {host}:{port}: {exc}")

    if connected:
        try:
            if op == "monitor":
                await monitor(client, duration)
            else:
                await asyncio.wait_for(request(client), 3 * timeout + 2)
        except ResponseTimeoutError as exc:
            failure = Failure("timeout", str(exc) or "no response from the outstation")
        except TimeoutError:
            failure = Failure("timeout", "the operation did not complete")
        except LinkError as exc:
            failure = Failure("connection-lost", str(exc))
        except MasterRunnerError as exc:
            failure = Failure("failed", str(exc))
        # The contract wants a result document in every case: the rest of
        # what dnp3py raises (its DNP3Error hierarchy, the fragment parser's
        # ParseError, the channel's and the reassembler's errors when the
        # runner does not wrap them), an OSError, and what decoding a
        # malformed response raises in the decoders (ValueError, struct.error,
        # an index or key out of range, an out-of-range timestamp).
        except (
            DNP3Error,
            FragmentParseError,
            ChannelError,
            ReassemblyError,
            OSError,
            ValueError,
            struct.error,
            LookupError,
            ArithmeticError,
        ) as exc:
            failure = Failure("failed", f"{type(exc).__name__}: {exc}")

    command_status = recorder.statuses[-1] if recorder.statuses else None
    command_statuses = None
    if op == "operate-multi":
        # One status per control echoed in the last response: the OPERATE, or
        # the SELECT that failed. The first refusal decides, not the last
        # status received.
        command_status = None
        if recorder.echo:
            command_statuses = recorder.echo
            command_status = next((s for s in command_statuses if s != 0), 0)
    # One status per control or null: a partial echo has no entry for the
    # controls without one, so its commandStatuses is null as no echo is.
    partial = op == "operate-multi" and command_statuses is not None and len(command_statuses) < request.count
    recorder.command_statuses = None if partial else command_statuses
    if failure is None and connected and op != "monitor":
        if (recorder.iin or 0) & REQUEST_ERRORS and not getattr(request, "no_response", False):
            message = "the outstation refused the request (IIN2)"
            if command_status is not None:
                message += f"; it echoed {status_name(command_status)}"
            failure = Failure("rejected", message)
            command_status = None
            recorder.command_statuses = None
        elif op in ("operate", "operate-multi") and command_status is not None and command_status != 0:
            failure = Failure("command-failed", f"the outstation answered {status_name(command_status)}")
        elif partial:
            failure = Failure("failed", f"the outstation echoed {len(command_statuses)} of {request.count} controls")
        elif op in ("operate", "operate-multi") and command_status is None and not getattr(request, "no_response", False):
            failure = Failure("failed", "the outstation's echo did not carry a status")
        elif op == "restart" and recorder.delay_ms is None:
            failure = Failure("failed", "the outstation's answer did not carry a delay")

    if connected and failure is None and collect > 0:
        try:
            await monitor(client, collect)
        except (LinkError, MasterRunnerError):
            pass
    if connected:
        # Let a confirm of the last response leave before the connection closes.
        await asyncio.sleep(0.2)
        try:
            await runner.close()
        except (OSError, LinkError, MasterRunnerError):
            pass
    return connected, failure, command_status, recorder


def client_main(argv):
    if not argv:
        log("client: missing operation")
        return EXIT_USAGE
    op = argv[0]
    if op not in OPERATIONS:
        log(f"client: unknown operation '{op}'")
        return EXIT_USAGE
    flags = {"host", "port", "local-addr", "remote-addr", "connect-timeout-ms", "timeout-ms", "collect-ms", "transport"}
    switches = set()
    flags |= {
        "event-poll": {"classes"}, "enable-unsolicited": {"classes"}, "disable-unsolicited": {"classes"},
        "read": {"group", "variation", "start", "stop"},
        "operate": {"type", "index", "code", "count", "on-ms", "off-ms", "value", "mode"},
        "operate-multi": {"controls", "mode"},
        "time-sync": {"procedure"}, "restart": {"type"}, "assign-class": {"class", "group", "start", "stop"},
        "monitor": {"duration-ms"}, "freeze": {"group", "at", "interval-ms"},
    }.get(op, set())
    if op == "freeze":
        switches = {"clear", "no-ack"}
    try:
        a = Args(argv[1:], flags, switches)
        a.transport()
        host = a.required("host")
        port = a.integer("port", 20000, 1, 65535)
        local = a.integer("local-addr", 1, 0, 65519)
        remote = a.integer("remote-addr", 1024, 0, 65519)
        connect_timeout = a.integer("connect-timeout-ms", 5000, 1, 3600000) / 1000
        timeout = a.integer("timeout-ms", 5000, 1, 3600000) / 1000
        collect = a.integer("collect-ms", 0, 0, 3600000) / 1000
        duration = a.integer("duration-ms", 1000, 0, 3600000) / 1000
        request = None if op == "monitor" else parse_operation(op, a)
        no_response = (op == "freeze" and a.has("no-ack")) or (op == "operate" and a.str("mode", "") == "direct-no-ack")
    except UsageError as exc:
        log(f"client {op}: {exc}")
        return EXIT_USAGE
    if request is not None and no_response:
        inner = request

        # A request without a response: the same request, marked as such.
        async def request(c):
            await inner(c)

        request.no_response = True

    started = time.monotonic()
    connected, failure, command_status, recorder = asyncio.run(
        run_client(op, host, port, local, remote, connect_timeout, timeout, collect, duration, request)
    )
    reads = op in ("integrity-poll", "event-poll", "read")
    doc = {
        "schemaVersion": SCHEMA_VERSION, "adapter": ADAPTER, "operation": op, "ok": failure is None,
        "error": None if failure is None else {"code": failure.code, "message": failure.message},
        "connected": connected,
        "iin": iin_names(recorder.iin) if reads and recorder.iin is not None else None,
        "objects": recorder.objects,
    }
    if op in ("operate", "operate-multi"):
        doc["commandStatus"] = None if command_status is None else status_name(command_status)
    if op == "operate-multi":
        statuses = recorder.command_statuses
        doc["commandStatuses"] = None if statuses is None else [status_name(s) for s in statuses]
    if op == "restart":
        doc["restartDelayMs"] = recorder.delay_ms
    doc["elapsedMs"] = int((time.monotonic() - started) * 1000)
    emit(doc)
    if failure is None:
        return EXIT_OK
    return EXIT_CONNECT_FAILED if failure.code == "connect-failed" else EXIT_FAILED


# ---- server (outstation) ---------------------------------------------------

DEFAULT_FIXTURE = "/fixtures/baseline/fixture.json"
DEFAULT_READY_FILE = "/run/dnp3-interop/ready"
EXIT_FIXTURE_INVALID = 4

# What dnp3py's outstation serves: one static and one event variation per
# type, each type's events in one class (it answers an event read of g2 with
# every class 1 event, of g32 with class 2, of g22 with class 3), no events of
# outputs and frozen counters (it encodes them as g2 and g22), integer analog
# values (g30v1 and g40v1 carry integers).
SERVED = {
    # type: (static variations, default, event classes, event variations, default)
    "binaryInputs": ({2}, 2, {0, 1}, {1}, 2),
    "binaryOutputs": ({2}, 2, {0}, set(), 2),
    "counters": ({1}, 1, {0, 3}, {5}, 1),
    "frozenCounters": ({1}, 1, {0}, set(), 1),
    "analogInputs": ({1}, 1, {0, 2}, {1}, 1),
    "analogOutputs": ({1, 2}, 1, {0}, set(), 1),
}
POINT_FIELDS = {"index", "value", "class", "flags", "staticVariation", "eventVariation"}
FLAG_BITS = {
    "binary": {**{n: 1 << i for i, n in enumerate(COMMON_FLAGS)}, "CHATTER_FILTER": 0x20},
    "output": {n: 1 << i for i, n in enumerate(COMMON_FLAGS)},
    "counter": {**{n: 1 << i for i, n in enumerate(COMMON_FLAGS)}, "DISCONTINUITY": 0x40},
    "analog": {**{n: 1 << i for i, n in enumerate(COMMON_FLAGS)}, "OVER_RANGE": 0x20, "REFERENCE_ERR": 0x40},
}
FAMILY = {"binaryInputs": "binary", "binaryOutputs": "output", "counters": "counter", "frozenCounters": "counter",
          "analogInputs": "analog", "analogOutputs": "analog"}
QUALITY = {"binary": BinaryQuality, "output": BinaryQuality, "counter": CounterQuality, "analog": AnalogQuality}


class FixtureError(Exception):
    pass


def is_int(v):
    return isinstance(v, int) and not isinstance(v, bool)


def load_fixture(text):
    """The fixture, checked against docs/FIXTURES.md and what dnp3py serves."""
    try:
        fx = json.loads(text)
    except ValueError as e:
        raise FixtureError(f"not JSON: {e}") from None
    errors = []
    if not isinstance(fx, dict):
        raise FixtureError("not a JSON object")
    for k in fx:
        if k not in ("schemaVersion", "name", "description", "outstation", *SERVED):
            errors.append(f"{k}: dnp3py's outstation does not serve it" if k in (
                "doubleBitInputs", "octetStrings", "frozenAnalogInputs", "timeAndIntervals", "files", "directories")
                else f"unknown field '{k}'")
    if fx.get("schemaVersion") != "1.0":
        errors.append('schemaVersion must be "1.0"')
    if not isinstance(fx.get("name"), str) or not re.fullmatch(r"[a-z0-9][a-z0-9-]*", fx["name"]):
        errors.append("name is missing or invalid")
    o = fx.get("outstation")
    if not isinstance(o, dict):
        errors.append("outstation is required")
        o = {}
    for k in o:
        if k not in ("address", "masterAddress", "unsolicited", "needTime", "coldRestartDelayMs", "warmRestartDelayMs",
                     "maxControlsPerRequest", "eventBufferSize"):
            errors.append(f"outstation: unknown field '{k}'")
    for k in ("address", "masterAddress"):
        if not is_int(o.get(k)) or not 0 <= o[k] <= 65519:
            errors.append(f"outstation: {k} must be 0..65519")
    if o.get("address") == o.get("masterAddress"):
        errors.append("outstation: address and masterAddress are equal")
    for k in ("unsolicited", "needTime"):
        if k in o and not isinstance(o[k], bool):
            errors.append(f"outstation: {k} must be a boolean")
    if o.get("unsolicited"):
        errors.append("outstation.unsolicited: dnp3py's outstation sends no unsolicited responses")
    if "maxControlsPerRequest" in o and (not is_int(o["maxControlsPerRequest"]) or not 1 <= o["maxControlsPerRequest"] <= 255):
        errors.append("outstation: maxControlsPerRequest must be 1..255")
    if "eventBufferSize" in o and (not is_int(o["eventBufferSize"]) or not 1 <= o["eventBufferSize"] <= 65535):
        errors.append("outstation: eventBufferSize must be 1..65535")
    for k in ("coldRestartDelayMs", "warmRestartDelayMs"):
        if k in o and (not is_int(o[k]) or not 0 <= o[k] <= 65535):
            errors.append(f"outstation: {k} must be 0..65535")

    points = {}
    for kind, (svars, sdef, classes, evars, edef) in SERVED.items():
        points[kind] = {}
        items = fx.get(kind, [])
        if not isinstance(items, list):
            errors.append(f"{kind} must be an array")
            continue
        for p in items:
            where = f"{kind}[{p.get('index') if isinstance(p, dict) else '?'}]"
            if not isinstance(p, dict):
                errors.append(f"{where}: not an object")
                continue
            allowed = set(POINT_FIELDS)
            if kind in ("binaryOutputs", "analogOutputs"):
                allowed |= {"target", "selectRequired"}
            if kind == "analogOutputs":
                allowed |= {"min", "max"}
            for k in p:
                if k not in allowed:
                    errors.append(f"{where}: unknown field '{k}'")
            i = p.get("index")
            if not is_int(i) or not 0 <= i <= 65535:
                errors.append(f"{where}: index must be 0..65535")
                continue
            if i in points[kind]:
                errors.append(f"{where}: duplicate index")
            c = p.get("class", 0)
            if not is_int(c) or c not in classes:
                errors.append(f"{where}: class {c}: dnp3py's outstation puts the events of {kind} in class "
                              f"{max(classes)} only" if max(classes) else f"{where}: dnp3py's outstation raises no events of {kind}")
            sv = p.get("staticVariation", sdef)
            if sv not in svars:
                errors.append(f"{where}: dnp3py's outstation serves static variation {sorted(svars)} only")
            ev = p.get("eventVariation", edef)
            if c and c in classes and ev not in evars:
                errors.append(f"{where}: dnp3py's outstation serves event variation {sorted(evars)} only")
            family = FAMILY[kind]
            flags = 0
            for f in p.get("flags", ["ONLINE"]):
                bit = FLAG_BITS[family].get(f)
                if bit is None or flags & bit:
                    errors.append(f"{where}: flag {f} is not allowed or repeated")
                else:
                    flags |= bit
            v = p.get("value")
            if kind in ("binaryInputs", "binaryOutputs"):
                if not isinstance(v, bool):
                    errors.append(f"{where}: value must be a boolean")
            elif kind in ("counters", "frozenCounters"):
                if not is_int(v) or not 0 <= v <= 4294967295:
                    errors.append(f"{where}: value must be 0..4294967295")
            else:
                limit = 32767 if (kind == "analogOutputs" and sv == 2) else 2147483647
                if not is_int(v) or not -limit - 1 <= v <= limit:
                    errors.append(f"{where}: dnp3py serves integer analog values only, in the range of the variation")
            for bound in ("min", "max"):
                if bound in p and not isinstance(p[bound], (int, float)):
                    errors.append(f"{where}: {bound} must be a number")
            if "selectRequired" in p and not isinstance(p["selectRequired"], bool):
                errors.append(f"{where}: selectRequired must be a boolean")
            points[kind][i] = dict(p, flags=flags, staticVariation=sv)
    aovars = {p["staticVariation"] for p in points["analogOutputs"].values()}
    if len(aovars) > 1:
        errors.append("analogOutputs: dnp3py's outstation serves one static variation for all of them")
    for kind, target_kind in (("binaryOutputs", "binaryInputs"), ("analogOutputs", "analogInputs")):
        seen = {}
        for i, p in points[kind].items():
            t = p.get("target")
            if t is None:
                continue
            if t not in points[target_kind]:
                errors.append(f"{kind}[{i}]: target {t} is not one of the {target_kind}")
            elif t in seen:
                errors.append(f"{kind}[{i}]: target {t} is also the target of output {seen[t]}")
            seen[t] = i
    for i in points["frozenCounters"]:
        if i not in points["counters"]:
            errors.append(f"frozenCounters[{i}]: there is no counter with this index to freeze")
    if errors:
        raise FixtureError("; ".join(errors))
    return fx, points


class Controls:
    """The control rules of docs/FIXTURES.md, for dnp3py's CommandHandler.

    dnp3py tells the handler neither the variation of an analog control nor
    whether a direct operate expects a response: InteropOutstation notes both
    from the request before it is processed.
    """

    def __init__(self, fx, points, outstation_ref):
        self.points = points
        self.db = None
        self.outstation = outstation_ref
        self.fx = fx

    def _check(self, kind, index, direct, code=None, value=None):
        p = self.points[kind].get(index)
        if p is None:
            return CommandStatus.NOT_SUPPORTED
        if code is not None and int(code) not in (CODES["LATCH_ON"], CODES["LATCH_OFF"], CODES["CLOSE"], CODES["TRIP"]):
            return CommandStatus.NOT_SUPPORTED
        if value is not None and (("min" in p and value < p["min"]) or ("max" in p and value > p["max"])):
            return CommandStatus.OUT_OF_RANGE
        if direct and p.get("selectRequired"):
            return CommandStatus.NOT_SUPPORTED
        return CommandStatus.SUCCESS

    def _event(self, function, kind, index, status, code=None, value=None, count=0, on=0, off=0):
        o = {"event": "command", "function": function}
        if kind == "binaryOutputs":
            o.update({"type": "g12v1", "index": index, "code": int(code), "count": count, "onMs": on, "offMs": off})
        else:
            o.update({"type": f"g41v{self.outstation[0].control_variation}", "index": index, "value": number(value)})
        o["status"] = status_name(int(status))
        emit(o)

    def _direct_name(self):
        return "direct-operate-no-ack" if self.outstation[0].no_ack else "direct-operate"

    # dnp3py's update_* reset the flags to ONLINE unless given: keep them.
    def _set_binary(self, kind, index, value):
        p = (self.db.get_binary_output if kind == "binaryOutputs" else self.db.get_binary_input)(index)
        if p is not None and p.value != value:
            update = self.db.update_binary_output if kind == "binaryOutputs" else self.db.update_binary_input
            update(index, value, quality=p.quality)

    def _set_analog(self, kind, index, value):
        p = (self.db.get_analog_output if kind == "analogOutputs" else self.db.get_analog_input)(index)
        if p is not None and p.value != value:
            update = self.db.update_analog_output if kind == "analogOutputs" else self.db.update_analog_input
            update(index, value, quality=p.quality)

    def _apply_binary(self, index, code):
        value = int(code) in (CODES["LATCH_ON"], CODES["CLOSE"])
        self._set_binary("binaryOutputs", index, value)
        t = self.points["binaryOutputs"][index].get("target")
        if t is not None:
            self._set_binary("binaryInputs", t, value)

    def _apply_analog(self, index, value):
        self._set_analog("analogOutputs", index, value)
        t = self.points["analogOutputs"][index].get("target")
        if t is not None:
            self._set_analog("analogInputs", t, value)

    def select_binary_output(self, index, code, count, on_time, off_time):
        status = self._check("binaryOutputs", index, False, code=code)
        self._event("select", "binaryOutputs", index, status, code, count=count, on=on_time, off=off_time)
        return CommandResult(status)

    def operate_binary_output(self, index, code, count, on_time, off_time, select_sequence):
        status = self._check("binaryOutputs", index, False, code=code)
        if status == CommandStatus.SUCCESS:
            self._apply_binary(index, code)
        self._event("operate", "binaryOutputs", index, status, code, count=count, on=on_time, off=off_time)
        return CommandResult(status)

    def direct_operate_binary_output(self, index, code, count, on_time, off_time):
        status = self._check("binaryOutputs", index, True, code=code)
        if status == CommandStatus.SUCCESS:
            self._apply_binary(index, code)
        self._event(self._direct_name(), "binaryOutputs", index, status, code, count=count, on=on_time, off=off_time)
        return CommandResult(status)

    def select_analog_output(self, index, value):
        status = self._check("analogOutputs", index, False, value=value)
        self._event("select", "analogOutputs", index, status, value=value)
        return CommandResult(status)

    def operate_analog_output(self, index, value, select_sequence):
        status = self._check("analogOutputs", index, False, value=value)
        if status == CommandStatus.SUCCESS:
            self._apply_analog(index, value)
        self._event("operate", "analogOutputs", index, status, value=value)
        return CommandResult(status)

    def direct_operate_analog_output(self, index, value):
        status = self._check("analogOutputs", index, True, value=value)
        if status == CommandStatus.SUCCESS:
            self._apply_analog(index, value)
        self._event(self._direct_name(), "analogOutputs", index, status, value=value)
        return CommandResult(status)

    def cold_restart(self):
        return self._restart("cold", "coldRestartDelayMs")

    def warm_restart(self):
        return self._restart("warm", "warmRestartDelayMs")

    def _restart(self, kind, key):
        delay = self.fx["outstation"].get(key)
        if delay is not None:
            emit({"event": "restart", "type": kind})
        return delay

    def freeze_counters(self, start, stop, clear):
        # dnp3py passes 0..65535 whatever the request's range.
        for c in self.db.get_all_counters():
            if not start <= c.index <= stop:
                continue
            if self.db.get_frozen_counter(c.index) is not None:
                self.db.freeze_counter(c.index)
            if clear and c.value != 0:
                self.db.update_counter(c.index, 0, quality=c.quality)
        return CommandResult(CommandStatus.SUCCESS)


class InteropOutstation(Outstation):
    """dnp3py's Outstation, noting what the handler is not told, and without
    ENABLE and DISABLE UNSOLICITED (the fixture has no unsolicited responses).
    """

    control_variation = 1
    no_ack = False

    def process_request(self, data, *, peer=None):
        if len(data) >= 4:
            self.no_ack = data[1] == FunctionCode.DIRECT_OPERATE_NO_ACK
            if data[2] == 41:
                self.control_variation = data[3]
        return super().process_request(data, peer=peer)

    def _executors(self, peer):
        executors = super()._executors(peer)
        executors.pop(FunctionCode.ENABLE_UNSOLICITED, None)
        executors.pop(FunctionCode.DISABLE_UNSOLICITED, None)
        return executors


class EventRunner(OutstationTcpRunner):
    """dnp3py's TCP runner, reporting connections as server events."""

    async def _handle_connection(self, channel):
        emit({"event": "connection-opened"})
        try:
            await super()._handle_connection(channel)
        finally:
            emit({"event": "connection-closed"})


def build_outstation(fx, points, local, remote, select_ms, confirm_ms):
    o = fx["outstation"]
    holder = [None]
    controls = Controls(fx, points, holder)
    aovars = {p["staticVariation"] for p in points["analogOutputs"].values()} or {1}
    config = OutstationConfig(
        address=local, master_address=remote, select_timeout=select_ms / 1000, confirm_timeout=confirm_ms / 1000,
        time_sync_required=bool(o.get("needTime", False)), analog_output_static_variation=aovars.pop(),
        unsolicited=UnsolicitedConfig(enabled=False),
        max_controls_per_request=o.get("maxControlsPerRequest", 16))
    # eventBufferSize (contract 1.3): dnp3py sizes its buffer per type and
    # class; each class holds the sum of the three types.
    size = o.get("eventBufferSize", 100)
    db = Database(DatabaseConfig(event_buffer_config=EventBufferConfig(
        max_binary_events=size, max_analog_events=size, max_counter_events=size)))
    for i, p in sorted(points["binaryInputs"].items()):
        db.add_binary_input(i, BinaryInputConfig(event_class=EventClass(p.get("class", 0))), p["value"],
                            BinaryQuality(p["flags"]))
    for i, p in sorted(points["binaryOutputs"].items()):
        db.add_binary_output(i, BinaryOutputConfig(event_class=EventClass.NONE), p["value"], BinaryQuality(p["flags"]))
    for i, p in sorted(points["counters"].items()):
        db.add_counter(i, CounterConfig(event_class=EventClass(p.get("class", 0))), p["value"], CounterQuality(p["flags"]))
    for i, p in sorted(points["frozenCounters"].items()):
        db.add_frozen_counter(i, None, p["value"], CounterQuality(p["flags"]))
    for i, p in sorted(points["analogInputs"].items()):
        db.add_analog_input(i, AnalogInputConfig(event_class=EventClass(p.get("class", 0))), p["value"],
                            AnalogQuality(p["flags"]))
    for i, p in sorted(points["analogOutputs"].items()):
        # The adapter sets the value itself, keeping the flags; dnp3py's
        # tracking would reset them to ONLINE.
        db.add_analog_output(i, AnalogOutputConfig(event_class=EventClass.NONE, track_commands=False), p["value"],
                             AnalogQuality(p["flags"]))
    controls.db = db

    def time_written(ts):
        emit({"event": "time-sync", "time": format_time(ts.to_datetime())})

    outstation = InteropOutstation(config=config, database=db, handler=controls, time_handler=time_written)
    holder[0] = outstation
    return outstation


def server_main(argv):
    try:
        a = Args(argv, ["fixture", "bind-address", "port", "ready-file", "select-timeout-ms", "confirm-timeout-ms",
                        "local-addr", "remote-addr", "transport"])
        a.transport()
        path = a.str("fixture", DEFAULT_FIXTURE)
        bind = a.str("bind-address", "0.0.0.0")
        port = a.integer("port", 20000, 1, 65535)
        ready_file = a.str("ready-file", DEFAULT_READY_FILE)
        select_ms = a.integer("select-timeout-ms", 5000, 1, 3600000)
        confirm_ms = a.integer("confirm-timeout-ms", 5000, 1, 3600000)
        local = a.integer("local-addr", -1, 0, 65519)
        remote = a.integer("remote-addr", -1, 0, 65519)
    except UsageError as e:
        log(f"server: {e}")
        return EXIT_USAGE
    try:
        with open(path, encoding="utf-8") as f:
            fx, points = load_fixture(f.read())
    except (OSError, FixtureError) as e:
        log(f"server: fixture {path}: {e}")
        return EXIT_FIXTURE_INVALID
    local = fx["outstation"]["address"] if local < 0 else local
    remote = fx["outstation"]["masterAddress"] if remote < 0 else remote
    outstation = build_outstation(fx, points, local, remote, select_ms, confirm_ms)
    return asyncio.run(serve(outstation, fx, bind, port, ready_file, local, remote))


def write_ready_file(path):
    """Creates the readiness file, empty."""
    with open(path, "w", encoding="utf-8"):
        pass


async def serve(outstation, fx, bind, port, ready_file, local, remote):
    runner = EventRunner(outstation=outstation, host=bind, port=port)
    task = asyncio.create_task(runner.run())
    deadline = time.monotonic() + 5
    while runner.local_address is None:
        if task.done():
            log(f"server: cannot listen on {bind}:{port}: {task.exception()}")
            return EXIT_FAILED
        if time.monotonic() > deadline:
            log(f"server: not listening on {bind}:{port} after 5 s")
            return EXIT_FAILED
        await asyncio.sleep(0.01)
    try:
        await asyncio.to_thread(write_ready_file, ready_file)
    except OSError as e:
        log(f"server: cannot write the readiness file {ready_file}: {e}")
        return EXIT_FAILED
    emit({"event": "ready", "adapter": ADAPTER, "address": f"{bind}:{port}", "fixture": fx["name"],
          "linkAddress": local, "masterAddress": remote, "transport": "tcp", "dial": False})
    stop = asyncio.Event()
    loop = asyncio.get_running_loop()
    for sig in (signal.SIGTERM, signal.SIGINT):
        loop.add_signal_handler(sig, stop.set)
    done, _ = await asyncio.wait([task, asyncio.create_task(stop.wait())], return_when=asyncio.FIRST_COMPLETED)
    signalled = stop.is_set()
    if task in done and not signalled:
        log(f"server: the runner ended without a signal: {task.exception() if not task.cancelled() else 'cancelled'}")
    try:
        os.remove(ready_file)
    except OSError:
        pass
    await runner.stop()
    task.cancel()
    # Wait for the cancelled runner, at most 2 s; how it ended was logged
    # above when it ended on its own.
    finished, _ = await asyncio.wait({task}, timeout=2)
    if signalled and task in finished and not task.cancelled() and task.exception() is not None:
        log(f"server: the runner ended: {task.exception()}")
    # A clean stop is one asked for by SIGTERM or SIGINT; a runner that ends
    # on its own is a failure, without the stopped event.
    if not signalled:
        return EXIT_FAILED
    emit({"event": "stopped"})
    return EXIT_OK


def print_capabilities():
    emit({
        "schemaVersion": SCHEMA_VERSION,
        "adapter": ADAPTER,
        "adapterVersion": ADAPTER_VERSION,
        "protocol": "dnp3",
        "upstream": {
            "name": "dnp3py", "version": package_version("dnp3py"), "revision": UPSTREAM_REVISION,
            "license": "MIT", "language": "Python", "independentEngine": True,
        },
        # The outstation cannot serve the baseline fixture: no double-bit
        # inputs, octet strings or per-point variations.
        "roles": {"server": True, "client": True},
        "features": {
            "integrityPoll": True,
            "eventPoll": True,
            "read": True,
            "readAllObjectsClient": True,
            "directOperate": True,
            "selectBeforeOperate": True,
            "parameterErrorWithNotSupported": False,
            "directOperateNoAckServer": True,
            "directOperateNoAckClient": True,
            "timeSyncLan": True,
            "timeSyncNonLan": True,
            "timeSyncOnDemand": True,
            "restart": True,
            "restartDelayClient": True,
            "unsolicited": True,
            "assignClassServer": False,
            "assignClassClient": True,
            "freeze": True,
            "freezeNoAckServer": True,
            "freezeNoAckClient": True,
            "octetStrings": True,
            "tcp": True,
            "udp": False,
            "tls": False,
            "serial": False,
            # Not in the library.
            "fileTransfer": False,
            "fileServer": False,
            "fileClient": False,
            "fileDeleteClient": False,
            "fileAppendServer": False,
            "fileAppendClient": False,
            "fileAuthServer": False,
            "fileAuthClient": False,
            "secureAuthentication": False,
            # Contract 1.2: the decoders know none of g13, g31, g33, g43, g50v4.
            "commandEventsServer": False,
            "commandEventsClient": False,
            "frozenAnalogInputsClient": False,
            "timeAndIntervalClient": False,
            # Dual end point: TCP client only.
            "dualEndpointServer": False,
            "dualEndpointClient": False,
            "dualEndpointClientTls": False,
            "securityStatisticsServer": False,
            "securityStatisticsClient": False,
            "deviceAttributesServer": False,
            "deviceAttributesClient": False,
            "multipleControlsClient": True,
            # dnp3py's outstation carries out every control of a request,
            # whatever max_controls_per_request says.
            "maxControlsServer": False,
            # Contract 1.3: the fixture's eventBufferSize, and IIN2.3 when it overflows.
            "eventBufferServer": True,
            # Contract 1.3: none of the link layer is configurable.
            "linkConfirmsServer": False,
            "linkConfirmsClient": False,
            "keepAliveClient": False,
            "linkStatusEventServer": False,
            # Contract 1.3: the outstation ignores broadcast addresses and the
            # master refuses them.
            "broadcastServer": False,
            "broadcastEventServer": False,
            "broadcastClient": False,
            # Contract 1.3: no management functions.
            "manageServer": False,
            "manageClient": False,
            "multipleOutstationsServer": False,
            # The master sends FREEZE AT TIME and freezes of any group; the
            # outstation has neither (NO_FUNC_CODE_SUPPORT, counters only).
            "freezeAtTimeServer": False,
            "freezeAtTimeClient": True,
            "analogFreezeServer": False,
            "analogFreezeClient": True,
            # Contract 1.3: neither role reads or writes group 34 (the
            # outstation's deadbands are configuration only).
            "deadbandWriteServer": False,
            "deadbandWriteClient": False,
            "deadbandReadServer": False,
            "deadbandReadClient": False,
        },
        "pointTypes": list(SERVED),
        # Contract 1.2: what the outstation does differently from the master.
        "serverFeatures": {
            # Ranges and specific variations of a read are ignored: every point
            # of the type comes back in its one variation (tests/known-bugs.tsv).
            "read": False,
            "unsolicited": False,
            "octetStrings": False,
        },
        "commandTypes": ["g12v1", "g41v1", "g41v2", "g41v3", "g41v4"],
        "clientOperations": OPERATIONS,
    })
    return EXIT_OK


def print_fixture(name):
    if not name or "/" in name or "\\" in name or ".." in name:
        log(f"print-fixture: '{name}' is not a fixture name")
        return EXIT_USAGE
    try:
        with open(f"/fixtures/{name}/fixture.json", "rb") as f:
            sys.stdout.buffer.write(f.read())
    except OSError:
        log(f"print-fixture: no fixture '{name}'")
        return EXIT_FAILED
    return EXIT_OK


def main(argv):
    logging.basicConfig(
        stream=sys.stderr,
        level=logging.DEBUG if os.environ.get("DNP3_INTEROP_DEBUG") == "1" else logging.WARNING,
        format="[dnp3py] %(levelname)s %(name)s %(message)s",
    )
    command, rest = (argv[0], argv[1:]) if argv else ("server", [])
    if command == "server":
        return server_main(rest)
    if command == "client":
        return client_main(rest)
    if command == "print-capabilities" and not rest:
        return print_capabilities()
    if command == "print-fixture" and len(rest) <= 1:
        return print_fixture(rest[0] if rest else "baseline")
    if command == "version" and not rest:
        print(f"dnp3-interop {ADAPTER} {ADAPTER_VERSION} (dnp3py {package_version('dnp3py')}, {UPSTREAM_REVISION})")
        return EXIT_OK
    if command in ("help", "--help", "-h"):
        sys.stdout.write(USAGE)
        return EXIT_OK
    sys.stderr.write(USAGE)
    return EXIT_USAGE


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
