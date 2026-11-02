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
import socket
import struct
import sys
import time
from functools import partial
from datetime import UTC, datetime
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
from dnp3.transport_io.channel import ChannelError, TcpConfig
from dnp3.transport_io.tcp_client import TcpClientChannel
from dnp3.transport_io.tcp_server import serve as tcp_serve
from dnp3.datalink.builder import build_request_link_status

ADAPTER = "dnp3py"
SCHEMA_VERSION = "1.0"
ADAPTER_VERSION = os.environ.get("DNP3_INTEROP_ADAPTER_VERSION", "dev")
UPSTREAM_REVISION = os.environ.get("DNP3_INTEROP_UPSTREAM_REVISION", "unknown")

EXIT_OK, EXIT_FAILED, EXIT_USAGE, EXIT_CONNECT_FAILED = 0, 1, 2, 3

OPERATIONS = [
    "integrity-poll", "event-poll", "read", "operate", "time-sync", "restart", "enable-unsolicited",
    "disable-unsolicited", "assign-class", "freeze", "monitor", "operate-multi", "manage", "deadband-write",
    "attribute-read", "attribute-write", "file-get", "file-put", "file-ls", "file-info", "file-delete",
    "file-authenticate",
]
FILE_OPERATIONS = {"file-get", "file-put", "file-ls", "file-info", "file-delete", "file-authenticate"}

USAGE = """usage: dnp3-interop <command>

  server [--fixture PATH] [--bind-address ADDR] [--port N] [--connect HOST:PORT] [--ready-file PATH]
         [--select-timeout-ms N] [--confirm-timeout-ms N] [--transport tcp] [LINK]
         (serves fixtures within dnp3py's limits: fixtures/dnp3py)
         [--control-socket PATH]
  counter-increment --index N [--by D] [--control-socket PATH] [--timeout-ms N]
  client <operation> --host HOST [--port N] [--connect-timeout-ms N] [--timeout-ms N]
         [--collect-ms N] [--transport tcp] [LINK] [--keep-alive-ms N] [--no-confirm] [operation flags]
         (or --listen [--bind-address ADDR] [--port N] instead of --host: the outstation dials)
      integrity-poll
      event-poll           [--classes 1,2,3]
      read                 --group G --variation V [--start N --stop N]
      operate              --type g12v1|g41v1..4 --index N [--mode direct|sbo|direct-no-ack|operate]
                           [--broadcast] (direct-no-ack only) [--operate-delay-ms N] (sbo only)
                           g12v1: --code LATCH_ON|LATCH_OFF|PULSE_ON|PULSE_OFF|CLOSE|TRIP
                                  [--count N] [--on-ms N] [--off-ms N]
                           g41:   --value V
      time-sync            [--procedure lan|non-lan]
      restart              [--type cold|warm]
      enable-unsolicited   [--classes 1,2,3]
      disable-unsolicited  [--classes 1,2,3]
      assign-class         --class C --group G [--start N --stop N]
      freeze               [--clear] [--no-ack] [--group 20|30] [--at immediately|+MS|EPOCH_MS [--interval-ms N]]
      monitor              [--duration-ms N] [--no-unsolicited-confirm]
      operate-multi        --controls g12v1:I:CODE,g41vN:I:VALUE,... [--mode direct|sbo]
      manage               --function initialize-data|initialize-application|start-application|
                                      stop-application|save-configuration|activate-config [--spec NAME]
      deadband-write       --deadbands INDEX:VALUE[,...]
      attribute-read       --set N [--variation V]
      attribute-write      --set N --variation V (--text S | --uint N | --int N | --float X | --octets HEX | --bits HEX | --time T)
      file-get|file-ls|file-info|file-delete --name NAME
      file-put             --name NAME --content HEX [--append]
      file-authenticate    (with FILE)
      FILE (file-get, file-put, file-ls, file-delete): [--file-user U --file-password P]
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

# Size of a command event object (groups 13 and 43), the time included.
COMMAND_EVENT_WIDTH = {(13, 1): 1, (13, 2): 7, (43, 1): 5, (43, 2): 3, (43, 3): 11, (43, 4): 9, (43, 5): 5,
                       (43, 6): 9, (43, 7): 11, (43, 8): 15}
ANALOG_COMMAND_FORMAT = {1: "<i", 2: "<h", 3: "<i", 4: "<h", 5: "<f", 6: "<d", 7: "<f", 8: "<d"}

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


def time_from_ms(ms):
    return format_time(datetime.fromtimestamp(ms / 1000, UTC))


def number(v):
    """A value with no fractional part is printed as an integer, as every adapter does."""
    if isinstance(v, float) and v.is_integer() and abs(v) < 2**53:
        return int(v)
    return v


def iin_names(bits):
    return [IIN_NAMES[i] for i in range(16) if bits & (1 << i)]


def flag_names(group, flags):
    out = [COMMON_FLAGS[b] for b in range(5) if flags & (1 << b)]
    counter, analog = group in (20, 21, 22, 23), group in (30, 31, 32, 33, 40, 42)
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
        # Blocks of groups 0, 70 and 91 as (group, variation, qualifier, data).
        self.raw = []
        self.file_status = None

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
            if g in (0, 70, 91):
                # Device attributes and file transfer: decoded by the operation.
                self.raw.append((g, v, block.header.qualifier, bytes(block.data)))
                continue
            if (g, v) in COMMAND_EVENT_WIDTH:
                self._command_events(block, g, v, unsolicited)
                continue
            if (g, v) == (50, 4):
                self._time_and_interval(block, unsolicited)
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
            elif g in (30, 31, 32, 33, 40, 42):
                values = [(x.index, number(x.value), x.quality, x.timestamp) for x in _decode_analog(block, wire, None)]
            else:
                continue
            for index, value, quality, stamp in values:
                o = {"group": g, "variation": v, "index": index, "value": value, "flags": flag_names(g, quality)}
                if ((g, v) in TIMED or g in (31, 33)) and stamp is not None:
                    o["time"] = format_time(stamp)
                o["unsolicited"] = unsolicited
                self.objects.append(o)

    def _command_events(self, block, g, v, unsolicited):
        """Groups 13 and 43 (contract 1.2): the commanded state or value, and the status of the control."""
        width = COMMAND_EVENT_WIDTH[(g, v)]
        layout = _decode_object_layout(block.header.qualifier, block.data)
        if layout is None:
            return
        for index, offset in _iter_object_slots(layout, block.data, width):
            raw = block.data[offset : offset + width]
            if g == 13:
                value, status, rest = bool(raw[0] & 0x80), raw[0] & 0x7F, raw[1:]
            else:
                fmt = ANALOG_COMMAND_FORMAT[v]
                value, status = number(struct.unpack_from(fmt, raw, 1)[0]), raw[0]
                rest = raw[1 + struct.calcsize(fmt) :]
            o = {"group": g, "variation": v, "index": index, "value": value, "status": status_name(status)}
            if len(rest) >= 6:
                o["time"] = time_from_ms(int.from_bytes(rest[:6], "little"))
            o["unsolicited"] = unsolicited
            self.objects.append(o)

    def time_and_interval_block(self, data, unsolicited):
        """A g50v4 block from its object header on, with a start-stop range."""
        if len(data) < 3:
            return
        qualifier = data[2]
        if qualifier == 0x00 and len(data) >= 5:
            start, stop, offset = data[3], data[4], 5
        elif qualifier == 0x01 and len(data) >= 7:
            (start, stop), offset = struct.unpack_from("<HH", data, 3), 7
        else:
            return
        for index in range(start, stop + 1):
            raw = data[offset : offset + 11]
            if len(raw) < 11:
                return
            offset += 11
            self.objects.append({
                "group": 50, "variation": 4, "index": index, "value": int.from_bytes(raw[6:10], "little"),
                "time": time_from_ms(int.from_bytes(raw[:6], "little")), "units": raw[10],
                "unsolicited": unsolicited,
            })

    def _time_and_interval(self, block, unsolicited):
        """g50v4 (contract 1.2): the interval as the value, the time and the units."""
        layout = _decode_object_layout(block.header.qualifier, block.data)
        if layout is None:
            return
        for index, offset in _iter_object_slots(layout, block.data, 11):
            raw = block.data[offset : offset + 11]
            self.objects.append({
                "group": 50, "variation": 4, "index": index, "value": int.from_bytes(raw[6:10], "little"),
                "time": time_from_ms(int.from_bytes(raw[:6], "little")), "units": raw[10],
                "unsolicited": unsolicited,
            })

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
    _raw = b""

    def process_response(self, data):
        self._raw = data
        return super().process_response(data)

    def _parse_response_objects(self, objects, info):
        if self.recorder is not None:
            self.recorder.record(objects, info)
            t = info.truncation
            if t is not None and (t.group, t.variation) == (50, 4):
                # dnp3py's parser knows no width for g50v4 and stops there;
                # the adapter frames that one block from the fragment.
                self.recorder.time_and_interval_block(self._raw[4 + t.offset :], info.is_unsolicited)
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

    async def request_code(self, function, blocks=()):
        """A request whose function code dnp3py's FunctionCode may not have (ACTIVATE_CONFIG)."""
        if isinstance(function, int) and not isinstance(function, FunctionCode):
            function = RawFunction(function)
        return await self.request(function, blocks)


class RawFunction:
    """A function code as RequestHeader serialises one: by its value."""

    def __init__(self, value):
        self.value, self.name = value, f"FC{value}"

    def __eq__(self, other):
        return getattr(other, "value", other) == self.value

    def __hash__(self):
        return hash(self.value)


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
    if op == "manage":
        return parse_manage(a)
    if op == "deadband-write":
        return parse_deadbands(a)
    if op in ("attribute-read", "attribute-write"):
        return parse_attribute_operation(op, a)
    if op in FILE_OPERATIONS:
        return parse_file_operation(op, a)
    raise UsageError(f"unknown operation '{op}'")


# ---- management functions (contract 1.3; activate-config 1.5) ----

MANAGE_FUNCTIONS = {
    "initialize-data": (FunctionCode.INITIALIZE_DATA, False),
    "initialize-application": (FunctionCode.INITIALIZE_APPLICATION, True),
    "start-application": (FunctionCode.START_APPLICATION, True),
    "stop-application": (FunctionCode.STOP_APPLICATION, True),
    "save-configuration": (FunctionCode.SAVE_CONFIGURATION, False),
}
ACTIVATE_CONFIG = 31  # not in dnp3py's FunctionCode


def parse_manage(a):
    function = a.required("function")
    if function == "activate-config":
        # g70v8 file specifications, free format; the outstation answers g91v1.
        spec = a.str("spec", "config.xml")
        if not 1 <= len(spec.encode()) <= 255:
            raise UsageError("--spec must be 1..255 octets")
        headers = [free_format(70, 8, spec.encode())]
        return lambda c: c.request_code(ACTIVATE_CONFIG, headers)
    if a.has("spec"):
        raise UsageError("--spec goes with --function activate-config")
    if function not in MANAGE_FUNCTIONS:
        raise UsageError("--function must be " + ", ".join(MANAGE_FUNCTIONS) + " or activate-config")
    fc, applications = MANAGE_FUNCTIONS[function]
    headers = [all_objects(90, 1)] if applications else []
    return lambda c: c.request(fc, headers)


# ---- analog deadbands (contract 1.3) ----


def parse_deadbands(a):
    items = []
    for item in a.required("deadbands").split(","):
        index, sep, value = item.partition(":")
        try:
            i, v = int(index, 10), float(value)
        except ValueError:
            i, v = -1, -1.0
        if not sep or not 0 <= i <= 65535 or not math.isfinite(v) or v < 0 or v > 3.4028234663852886e38:
            raise UsageError("--deadbands items are INDEX:VALUE, VALUE a non-negative number")
        items.append((i, v))
    # g34v3 (single precision), a one-octet index prefix when every index fits.
    if all(i <= 0xFF for i, _ in items) and len(items) <= 0xFF:
        data = bytes([len(items)]) + b"".join(struct.pack("<Bf", i, v) for i, v in items)
        header = block(34, 3, RangeCode.UINT8_COUNT, data, PrefixCode.UINT8_INDEX)
    else:
        data = struct.pack("<H", len(items)) + b"".join(struct.pack("<Hf", i, v) for i, v in items)
        header = block(34, 3, RangeCode.UINT16_COUNT, data, PrefixCode.UINT16_INDEX)
    return lambda c: c.request(FunctionCode.WRITE, [header])


# ---- device attributes (contract 1.3) ----

ATTRIBUTE_TYPES = {1: "visibleString", 2: "unsignedInt", 3: "signedInt", 4: "float", 5: "octetString",
                   6: "bitString", 7: "time", 254: "attributeList", 255: "extendedAttributeList"}


def attribute_value(kind, raw):
    if kind == 1:
        return raw.decode("utf-8", "replace")
    if kind == 2:
        return int.from_bytes(raw, "little")
    if kind == 3:
        return int.from_bytes(raw, "little", signed=True)
    if kind == 4:
        return number(struct.unpack("<f", raw)[0] if len(raw) == 4 else struct.unpack("<d", raw)[0])
    if kind == 7:
        return time_from_ms(int.from_bytes(raw[:6], "little"))
    if kind in (254, 255):
        return None
    return raw.hex()


def decode_attributes(raw_blocks):
    """The attributes of group 0 response blocks: a start-stop range (the set) and type, length, value each."""
    out = []
    for g, v, qualifier, data in raw_blocks:
        if g != 0:
            continue
        range_code = qualifier & 0x0F
        if range_code == RangeCode.UINT8_START_STOP and len(data) >= 2:
            start, stop, offset = data[0], data[1], 2
        elif range_code == RangeCode.UINT16_START_STOP and len(data) >= 4:
            (start, stop), offset = struct.unpack_from("<HH", data), 4
        else:
            raise ValueError(f"g0v{v} with qualifier 0x{qualifier:02x}")
        for set_ in range(start, stop + 1):
            if offset + 2 > len(data):
                raise ValueError(f"g0v{v}: short attribute")
            kind, length = data[offset], data[offset + 1]
            raw = data[offset + 2 : offset + 2 + length]
            offset += 2 + length
            out.append({"set": set_, "variation": v, "type": ATTRIBUTE_TYPES.get(kind, "unknown"),
                        "value": attribute_value(kind, raw)})
    return out


def parse_attribute_operation(op, a):
    set_ = a.required_integer("set", 0, 255)
    if op == "attribute-read":
        variation = a.integer("variation", 254, 1, 254)

        async def read(c):
            before = len(c.recorder.raw)
            await c.request(FunctionCode.READ, [block(0, variation, RangeCode.UINT8_START_STOP, bytes([set_, set_]))])
            c.recorder.attributes = decode_attributes(c.recorder.raw[before:])

        return read
    variation = a.required_integer("variation", 1, 253)
    given = [f for f in ("text", "uint", "int", "float", "octets", "bits", "time") if a.has(f)]
    if len(given) != 1:
        raise UsageError("attribute-write needs exactly one of --text, --uint, --int, --float, --octets, --bits and --time")
    if given[0] == "text":
        kind, raw = 1, a.str("text", "").encode()
        if len(raw) > 255:
            raise UsageError("--text is longer than 255 octets")
    elif given[0] == "uint":
        n = a.integer("uint", 0, 0, 2**64 - 1)
        kind, raw = 2, n.to_bytes(next(w for w in (1, 2, 4, 8) if n < 1 << (8 * w)), "little")
    elif given[0] == "int":
        n = a.integer("int", 0, -(2**63), 2**63 - 1)
        width = next(w for w in (1, 2, 4, 8) if -(1 << (8 * w - 1)) <= n < 1 << (8 * w - 1))
        kind, raw = 3, n.to_bytes(width, "little", signed=True)
    elif given[0] == "float":
        f = a.number("float")
        if abs(f) > 3.4028234663852886e38:
            raise UsageError("--float is out of the range of a 32-bit float")
        kind, raw = 4, struct.pack("<f", f)
    elif given[0] in ("octets", "bits"):
        try:
            raw = bytes.fromhex(a.str(given[0], ""))
        except ValueError:
            raise UsageError(f"--{given[0]} must be hex") from None
        if len(raw) > 255:
            raise UsageError(f"--{given[0]} is longer than 255 octets")
        kind = 5 if given[0] == "octets" else 6
    else:
        try:
            at = datetime.strptime(a.str("time", ""), "%Y-%m-%dT%H:%M:%S.%fZ").replace(tzinfo=UTC)
        except ValueError:
            raise UsageError("--time must be YYYY-MM-DDTHH:MM:SS.mmmZ") from None
        ms = round(at.timestamp() * 1000)
        if not 0 <= ms < 2**48:
            raise UsageError("--time is out of the range of a DNP3 time")
        kind, raw = 7, ms.to_bytes(6, "little")
    header = block(0, variation, RangeCode.UINT8_START_STOP, bytes([set_, set_, kind, len(raw)]) + raw)
    return lambda c: c.request(FunctionCode.WRITE, [header])


# ---- file transfer (contracts 1.1 and 1.3), group 70 ----

FILE_STATUS = {0: "SUCCESS", 1: "PERMISSION_DENIED", 2: "INVALID_MODE", 3: "FILE_NOT_FOUND", 4: "FILE_LOCKED",
               5: "TOO_MANY_OPEN", 6: "INVALID_HANDLE", 7: "WRITE_BLOCK_SIZE", 8: "COMM_LOST", 9: "CANNOT_ABORT",
               16: "NOT_OPENED", 17: "HANDLE_EXPIRED", 18: "BUFFER_OVERRUN", 19: "FATAL", 20: "BLOCK_SEQ"}
FILE_NAME = re.compile(r"^(/[A-Za-z0-9_-][A-Za-z0-9._-]*)+$")
MODE_NULL, MODE_READ, MODE_WRITE, MODE_APPEND = 0, 1, 2, 3
FILE_BLOCK_SIZE = 1024
ZERO_TIME = "0001-01-01T00:00:00.000Z"  # a creation time of 0, as the dscsystems adapter prints it


class FileStatusError(Exception):
    def __init__(self, status, message):
        super().__init__(message)
        self.status = status


class Refused(Exception):
    """The outstation answered with an IIN2 request error: the result is "rejected"."""


def free_format(group, variation, obj):
    """One free-format object with a two-octet size (qualifier 0x5B)."""
    return block(group, variation, RangeCode.FREE_FORMAT, b"\x01" + struct.pack("<H", len(obj)) + obj,
                 PrefixCode.UINT16_SIZE)


def free_format_objects(qualifier, data):
    width = {4: 1, 5: 2, 6: 4}.get((qualifier >> 4) & 0x07)
    if width is None or not data:
        raise ValueError(f"qualifier 0x{qualifier:02x} is not free format")
    out, offset = [], 1
    for _ in range(data[0]):
        size = int.from_bytes(data[offset : offset + width], "little")
        offset += width
        out.append(data[offset : offset + size])
        offset += size
    return out


def file_time(raw):
    ms = int.from_bytes(raw, "little")
    return ZERO_TIME if ms == 0 else time_from_ms(ms)


def permission_string(p):
    return "".join("rwx"[2 - i % 3] if p & (1 << i) else "-" for i in range(8, -1, -1))


def file_descriptor(obj):
    """A g70v7 descriptor: the entry of the result, and how many octets it took."""
    if len(obj) < 20:
        raise ValueError(f"g70v7 is {len(obj)} octets, needs 20")
    name_offset, name_size, kind, size = struct.unpack_from("<HHHI", obj)
    name = obj[name_offset : name_offset + name_size].decode("utf-8", "replace")
    entry = {"name": name, "type": "directory" if kind == 0 else "file", "size": size,
             "permissions": permission_string(int.from_bytes(obj[16:18], "little")), "time": file_time(obj[10:16])}
    return entry, max(name_offset + name_size, 20)


class Files:
    """The file transfer exchanges of one operation, each a request and its response."""

    def __init__(self, c, name, credentials):
        self.c, self.name, self.credentials, self.key, self.request_id = c, name, credentials, 0, 1

    async def exchange(self, function, headers):
        before = len(self.c.recorder.raw)
        await self.c.request_code(function, headers)
        if (self.c.recorder.iin or 0) & REQUEST_ERRORS:
            raise Refused()
        return [(v, q, d) for g, v, q, d in self.c.recorder.raw[before:] if g == 70]

    @staticmethod
    def first(objects, variation):
        for v, q, d in objects:
            if v == variation:
                found = free_format_objects(q, d)
                if found:
                    return found[0]
        return None

    def check(self, status, what):
        if status != 0:
            name = FILE_STATUS.get(status, "UNDEFINED")
            raise FileStatusError(name, f"{what} {self.name}: the outstation answered {name}")

    async def authenticate(self):
        user, password = (x.encode() for x in self.credentials)
        obj = struct.pack("<HHHHI", 12, len(user), 12 + len(user), len(password), 0) + user + password
        answer = self.first(await self.exchange(FunctionCode.AUTHENTICATE_FILE, [free_format(70, 2, obj)]), 2)
        if answer is None or len(answer) < 12:
            raise ValueError("the answer to AUTHENTICATE_FILE carried no g70v2")
        self.key = int.from_bytes(answer[8:12], "little")
        if self.key == 0:
            raise FileStatusError("PERMISSION_DENIED", "the outstation answered key 0: the credentials are refused")

    def command(self, mode, size=0):
        name = self.name.encode()
        return free_format(70, 3, struct.pack("<HH", 26, len(name)) + bytes(6)
                           + struct.pack("<HIIHHH", 0, self.key, size, mode, FILE_BLOCK_SIZE, self.request_id) + name)

    async def open(self, mode, size=0):
        if self.credentials:
            await self.authenticate()
        status = self.first(await self.exchange(FunctionCode.OPEN_FILE, [self.command(mode, size)]), 4)
        if status is None or len(status) < 13:
            raise ValueError("the answer to OPEN_FILE carried no g70v4")
        handle, size, block_size = struct.unpack_from("<IIH", status)
        self.check(status[12], "opening")
        if handle == 0:
            raise ValueError("the outstation returned no handle")
        return handle, size, min(FILE_BLOCK_SIZE, block_size) if block_size else FILE_BLOCK_SIZE

    async def close(self, handle):
        obj = struct.pack("<IIHHB", handle, 0, 0, self.request_id, 0)
        status = self.first(await self.exchange(FunctionCode.CLOSE_FILE, [free_format(70, 4, obj)]), 4)
        if status is not None and len(status) >= 13:
            self.check(status[12], "closing")

    async def read(self):
        handle, _, _ = await self.open(MODE_READ)
        data, number_ = bytearray(), 0
        try:
            while True:
                objects = await self.exchange(FunctionCode.READ, [free_format(70, 5, struct.pack("<II", handle, number_))])
                failed = self.first(objects, 6)
                if failed is not None and len(failed) >= 9:
                    self.check(failed[8], f"reading block {number_} of")
                got = self.first(objects, 5)
                if got is None or len(got) < 8:
                    raise ValueError(f"reading block {number_} of {self.name}: the response carried no file data")
                block_word = int.from_bytes(got[4:8], "little")
                if block_word & 0x7FFFFFFF != number_:
                    raise ValueError(f"block {block_word & 0x7FFFFFFF} arrived where {number_} was expected")
                data += got[8:]
                number_ += 1
                if block_word & 0x80000000:
                    break
        except BaseException:
            await self.close_quietly(handle)
            raise
        await self.close(handle)
        return bytes(data)

    async def write(self, content, append):
        handle, _, block_size = await self.open(MODE_APPEND if append else MODE_WRITE, len(content))
        try:
            chunks = [content[i : i + block_size] for i in range(0, len(content), block_size)] or [b""]
            for number_, chunk in enumerate(chunks):
                word = number_ | (0x80000000 if number_ == len(chunks) - 1 else 0)
                objects = await self.exchange(FunctionCode.WRITE, [free_format(70, 5, struct.pack("<II", handle, word) + chunk)])
                failed = self.first(objects, 6)
                if failed is not None and len(failed) >= 9:
                    self.check(failed[8], f"writing block {number_} of")
        except BaseException:
            await self.close_quietly(handle)
            raise
        await self.close(handle)

    async def close_quietly(self, handle):
        try:
            await self.close(handle)
        except Exception:  # noqa: BLE001 - the first error is the one reported
            pass

    async def delete(self):
        if self.credentials:
            await self.authenticate()
        status = self.first(await self.exchange(FunctionCode.DELETE_FILE, [self.command(MODE_NULL)]), 4)
        if status is None or len(status) < 13:
            raise ValueError("the answer to DELETE_FILE carried no g70v4")
        self.check(status[12], "deleting")

    async def info(self):
        name = self.name.encode()
        obj = struct.pack("<HHHI", 20, len(name), 0, 0) + bytes(6) + struct.pack("<HH", 0, self.request_id) + name
        objects = await self.exchange(FunctionCode.GET_FILE_INFO, [free_format(70, 7, obj)])
        descriptor = self.first(objects, 7)
        if descriptor is None:
            status = self.first(objects, 4)
            if status is not None and len(status) >= 13:
                self.check(status[12], "describing")
            raise ValueError("the answer to GET_FILE_INFO carried no descriptor")
        return file_descriptor(descriptor)[0]


def parse_file_operation(op, a):
    has_user, has_password = a.has("file-user"), a.has("file-password")
    if has_user != has_password:
        raise UsageError("--file-user and --file-password go together")
    credentials = (a.str("file-user", ""), a.str("file-password", "")) if has_user else None
    if op == "file-authenticate":
        if credentials is None:
            raise UsageError("file-authenticate needs --file-user and --file-password")

        async def authenticate(c):
            await Files(c, "", credentials).authenticate()

        return authenticate
    if op == "file-info" and credentials is not None:
        raise UsageError("file-info takes no --file-user or --file-password: GET_FILE_INFO carries no key")
    name = a.required("name")
    if name != "/" and (not FILE_NAME.match(name) or len(name) > 200):
        raise UsageError("--name must be an absolute file name of at most 200 characters, path elements of "
                         "A-Za-z0-9._- not starting with '.'")
    if op == "file-put":
        try:
            content = bytes.fromhex(a.required("content"))
        except ValueError:
            raise UsageError("--content must be hex") from None
        append = a.has("append")

        async def put(c):
            await Files(c, name, credentials).write(content, append)

        return put
    if op == "file-get":
        async def get(c):
            data = await Files(c, name, credentials).read()
            c.recorder.file = {"name": name, "size": len(data), "content": data.hex()}

        return get
    if op == "file-ls":
        async def ls(c):
            data = await Files(c, name, credentials).read()
            entries, offset = [], 0
            while offset < len(data):
                entry, used = file_descriptor(data[offset:])
                entries.append(entry)
                offset += used
            c.recorder.files = entries

        return ls
    if op == "file-info":
        async def info(c):
            c.recorder.file = await Files(c, name, None).info()

        return info

    async def delete(c):
        await Files(c, name, credentials).delete()

    return delete


def parse_operate(a):
    kind = a.required("type")
    index = a.required_integer("index", 0, 65535)
    mode = a.str("mode", "direct")
    if mode not in ("direct", "sbo", "direct-no-ack", "operate"):
        raise UsageError("--mode must be direct, sbo, direct-no-ack or operate")
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
        if mode == "operate":
            # An OPERATE without a SELECT (contract 1.5): the outstation answers NO_SELECT.
            await c.request(FunctionCode.OPERATE, [obj])
            return
        await c.request(FunctionCode.SELECT, [obj])
        # The OPERATE follows only a successful SELECT, with no request between.
        if c.recorder.statuses and c.recorder.statuses[-1] == 0 and not (c.recorder.iin or 0) & REQUEST_ERRORS:
            if delay:
                # --operate-delay-ms (contract 1.5): let the selection age first.
                await asyncio.sleep(delay / 1000)
            await c.request(FunctionCode.OPERATE, [obj])

    if a.has("broadcast") and mode != "direct-no-ack":
        raise UsageError("--broadcast needs --mode direct-no-ack")
    if a.has("operate-delay-ms") and mode != "sbo":
        raise UsageError("--operate-delay-ms needs --mode sbo")
    delay = a.integer("operate-delay-ms", 0, 0, 3600000)
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
    c.recorder.unsolicited_fragments = 0
    while (left := end - time.monotonic()) > 0:
        if await c.runner.listen_unsolicited(timeout=left) is not None:
            c.recorder.unsolicited_fragments += 1
        if not c.runner.is_open:
            # The runner can return on an end of stream without raising.
            raise LinkError("the connection closed during the monitoring")


class NoConfirmRunner(MasterTcpRunner):
    """--no-confirm (contract 1.5): the master confirms no solicited response, so the outstation keeps its events.

    Unsolicited responses are still confirmed: an outstation that waits for
    the confirm of its null unsolicited response at start (DSC go-dnp3)
    answers no request until it has it.
    """

    async def _send_confirm(self, sequence, *, uns, deadline):
        if uns:
            await super()._send_confirm(sequence, uns=uns, deadline=deadline)


class NoUnsolicitedConfirmRunner(MasterTcpRunner):
    """monitor --no-unsolicited-confirm (contract 1.5): no unsolicited response is confirmed.

    The outstation sends it again after its confirm timeout; the monitor
    counts every unsolicited fragment it receives.
    """

    async def _send_confirm(self, sequence, *, uns, deadline):
        if not uns:
            await super()._send_confirm(sequence, uns=uns, deadline=deadline)


async def keep_alive(runner, master, every):
    """--keep-alive-ms (contract 1.3): REQUEST_LINK_STATUS when nothing was written for that long.

    dnp3py's runner has the frame builder but never sends it; the adapter
    writes it on the runner's channel, and the runner skips the LINK_STATUS
    answer (a frame without user data).
    """
    while True:
        await asyncio.sleep(every)
        frame = build_request_link_status(destination=master.config.outstation_address, source=master.config.address,
                                          dir_from_master=True)
        await runner._write_frame(frame, runner._deadline(None))


async def accept_outstation(o):
    """--listen (contract 1.2): wait for the outstation to dial; the server stops listening once it has."""
    server = await tcp_serve(o.bind, o.port)
    try:
        return await asyncio.wait_for(server.accept(), o.connect_timeout), server
    except BaseException:
        await server.stop()
        raise


async def run_client(op, o, request):
    recorder = Recorder()
    master = RecordingMaster(
        config=MasterConfig(
            address=o.local, outstation_address=o.remote, response_timeout=o.timeout, enable_unsolicited=True,
            startup_integrity_poll=False, disable_unsolicited_on_startup=False, enable_unsolicited_on_startup=False,
            time_sync_on_startup=False,
        ),
        handler=DefaultSOEHandler(),
    )
    master.recorder = recorder
    # Connected means the TCP connection, as for every adapter: no link reset
    # that an outstation with another address would leave unanswered.
    runner_type = NoConfirmRunner if o.no_confirm else MasterTcpRunner
    if o.no_unsolicited_confirm:
        runner_type = NoUnsolicitedConfirmRunner
    connected = False
    failure = None
    listener = None
    runner = None
    where = f"{o.bind}:{o.port}" if o.listen else f"{o.host}:{o.port}"
    try:
        channel = None
        if o.listen:
            channel, listener = await accept_outstation(o)
        runner = runner_type(master=master, host=o.host or o.bind, port=o.port, response_timeout=o.timeout,
                             link_reset=LinkResetPolicy.NEVER, channel=channel)
        await asyncio.wait_for(runner.open(), o.connect_timeout)
        connected = True
    # What opening can raise: the channel's ChannelConnectionError and
    # ChannelTimeoutError (both ChannelError), the runner's MasterRunnerError,
    # an OSError the channel does not wrap, and wait_for's TimeoutError.
    except (ChannelError, MasterRunnerError, OSError, TimeoutError) as exc:
        what = "no outstation dialled" if o.listen else "no connection to"
        failure = Failure("connect-failed", f"{what} {where}: {exc}")
    client = Client(master, runner, recorder, o.timeout)

    alive = None
    if connected and o.keep_alive > 0:
        alive = asyncio.create_task(keep_alive(runner, master, o.keep_alive))
    if connected:
        try:
            if op == "monitor":
                await monitor(client, o.duration)
            else:
                # A file transfer is many exchanges, each bounded by the response timeout.
                bound = 20 * o.timeout + 2 if op in FILE_OPERATIONS else 3 * o.timeout + 2
                await asyncio.wait_for(request(client), bound)
        except Refused:
            pass  # the IIN2 error makes it "rejected" below
        except FileStatusError as exc:
            recorder.file_status = exc.status
            failure = Failure("file-error", str(exc))
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
    if alive is not None and alive.done() and not alive.cancelled() and alive.exception() is not None:
        if failure is None:
            failure = Failure("connection-lost", f"keep-alive: {alive.exception()}")

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
        elif op in FILE_OPERATIONS:
            recorder.file_status = "SUCCESS"

    if connected and failure is None and o.collect > 0:
        try:
            await monitor(client, o.collect)
        except (LinkError, MasterRunnerError):
            pass
    if alive is not None:
        alive.cancel()
    if connected:
        # Let a confirm of the last response leave before the connection closes.
        await asyncio.sleep(0.2)
        try:
            await runner.close()
            if o.listen:
                await runner.channel.close()
        except (OSError, LinkError, MasterRunnerError, ChannelError):
            pass
    if listener is not None:
        await listener.stop()
    return connected, failure, command_status, recorder


class Options:
    pass


def client_main(argv):
    if not argv:
        log("client: missing operation")
        return EXIT_USAGE
    op = argv[0]
    if op not in OPERATIONS:
        log(f"client: unknown operation '{op}'")
        return EXIT_USAGE
    flags = {"host", "port", "local-addr", "remote-addr", "connect-timeout-ms", "timeout-ms", "collect-ms", "transport",
             "keep-alive-ms", "bind-address"}
    switches = {"listen", "no-confirm", "no-unsolicited-confirm"}
    flags |= {
        "event-poll": {"classes"}, "enable-unsolicited": {"classes"}, "disable-unsolicited": {"classes"},
        "read": {"group", "variation", "start", "stop"},
        "operate": {"type", "index", "code", "count", "on-ms", "off-ms", "value", "mode", "operate-delay-ms"},
        "operate-multi": {"controls", "mode"},
        "time-sync": {"procedure"}, "restart": {"type"}, "assign-class": {"class", "group", "start", "stop"},
        "monitor": {"duration-ms"}, "freeze": {"group", "at", "interval-ms"},
        "manage": {"function", "spec"}, "deadband-write": {"deadbands"},
        "attribute-read": {"set", "variation"}, "attribute-write": {"set", "variation", "text", "uint", "int", "float", "octets", "bits", "time"},
        "file-get": {"name", "file-user", "file-password"}, "file-ls": {"name", "file-user", "file-password"},
        "file-info": {"name", "file-user", "file-password"}, "file-delete": {"name", "file-user", "file-password"},
        "file-put": {"name", "content", "file-user", "file-password"},
        "file-authenticate": {"file-user", "file-password"},
    }.get(op, set())
    switches |= {"freeze": {"clear", "no-ack"}, "operate": {"broadcast"}, "file-put": {"append"}}.get(op, set())
    o = Options()
    try:
        a = Args(argv[1:], flags, switches)
        a.transport()
        o.listen = a.has("listen")
        if o.listen:
            if a.has("host"):
                raise UsageError("--host does not go with --listen")
            o.host, o.bind = None, a.str("bind-address", "0.0.0.0")
        else:
            if a.has("bind-address"):
                raise UsageError("--bind-address goes with --listen")
            o.host, o.bind = a.required("host"), None
        o.port = a.integer("port", 20000, 1, 65535)
        o.local = a.integer("local-addr", 1, 0, 65519)
        o.remote = a.integer("remote-addr", 1024, 0, 65519)
        o.connect_timeout = a.integer("connect-timeout-ms", 5000, 1, 3600000) / 1000
        o.timeout = a.integer("timeout-ms", 5000, 1, 3600000) / 1000
        o.collect = a.integer("collect-ms", 0, 0, 3600000) / 1000
        o.duration = a.integer("duration-ms", 1000, 0, 3600000) / 1000
        o.keep_alive = a.integer("keep-alive-ms", 0, 0, 3600000) / 1000
        o.no_confirm = a.has("no-confirm")
        o.no_unsolicited_confirm = a.has("no-unsolicited-confirm")
        if o.no_unsolicited_confirm and (op != "monitor" or o.no_confirm):
            raise UsageError("--no-unsolicited-confirm is for monitor, without --no-confirm")
        request = None if op == "monitor" else parse_operation(op, a)
        if a.has("broadcast"):
            # Broadcast (contract 1.3): the request goes to 0xFFFF instead of --remote-addr.
            o.remote = 0xFFFF
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
    connected, failure, command_status, recorder = asyncio.run(run_client(op, o, request))
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
    if op == "monitor" and o.no_unsolicited_confirm:
        doc["unsolicitedFragments"] = getattr(recorder, "unsolicited_fragments", 0)
    if op == "attribute-read":
        doc["attributes"] = getattr(recorder, "attributes", None) or []
    if op in FILE_OPERATIONS:
        if getattr(recorder, "files", None) is not None:
            doc["files"] = recorder.files
        elif getattr(recorder, "file", None) is not None:
            doc["file"] = recorder.file
        doc["fileStatus"] = recorder.file_status
    doc["elapsedMs"] = int((time.monotonic() - started) * 1000)
    emit(doc)
    if failure is None:
        return EXIT_OK
    return EXIT_CONNECT_FAILED if failure.code == "connect-failed" else EXIT_FAILED


# ---- server (outstation) ---------------------------------------------------

DEFAULT_FIXTURE = "/fixtures/baseline/fixture.json"
DEFAULT_READY_FILE = "/run/dnp3-interop/ready"
DEFAULT_CONTROL_SOCKET = "/run/dnp3-interop/control.sock"
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
            if kind == "binaryOutputs":
                allowed |= {"pulse"}
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
            if "pulse" in p and not isinstance(p["pulse"], bool):
                errors.append(f"{where}: pulse must be a boolean")
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
        latch = (CODES["LATCH_ON"], CODES["LATCH_OFF"], CODES["CLOSE"], CODES["TRIP"])
        pulse = (CODES["PULSE_ON"], CODES["PULSE_OFF"])
        if code is not None and int(code) not in latch and not (int(code) in pulse and p.get("pulse")):
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
        if int(code) in (CODES["PULSE_ON"], CODES["PULSE_OFF"]):
            return  # a pulse ends where it began (docs/FIXTURES.md)
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
                        "local-addr", "remote-addr", "transport", "control-socket", "frozen-event-policy", "connect"])
        a.transport()
        dial = None
        if a.has("connect"):
            # Dual end point (contract 1.2): the outstation dials the master.
            if a.has("bind-address") or a.has("port"):
                raise UsageError("--bind-address and --port do not go with --connect")
            host, sep, dial_port = a.str("connect", "").rpartition(":")
            if not sep or not host or not dial_port.isdigit() or not 1 <= int(dial_port) <= 65535:
                raise UsageError("--connect must be HOST:PORT")
            dial = (host, int(dial_port))
        if a.has("frozen-event-policy"):
            raise UsageError("--frozen-event-policy: this outstation raises no frozen counter events")
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
    control_socket = a.str("control-socket", DEFAULT_CONTROL_SOCKET)
    return asyncio.run(serve(outstation, fx, bind, port, ready_file, control_socket, local, remote, dial))


def write_ready_file(path):
    """Creates the readiness file, empty."""
    with open(path, "w", encoding="utf-8"):
        pass


def increment(db, index, by):
    """Adds to a counter, modulo 2^32, through the library's update."""
    counter = db.get_counter(index)
    if counter is None:
        raise KeyError(f"no counter at index {index}")
    value = (counter.value + by) % (1 << 32)
    db.update_counter(index, value, quality=counter.quality)
    return value


async def serve_control(db, reader, writer):
    """One exchange on the control socket (contract 1.4): a line of JSON each way."""
    try:
        request = json.loads(await reader.readline())
        if request.get("operation") != "counter-increment":
            raise KeyError("unknown control operation")
        reply = {"ok": True, "value": increment(db, request["index"], request["by"])}
    except (KeyError, ValueError) as e:
        reply = {"ok": False, "message": str(e)}
    writer.write((json.dumps(reply) + "\n").encode())
    await writer.drain()
    writer.close()


def counter_increment_main(argv):
    """The counter-increment command (contract 1.4), on the running server's control socket."""
    start = time.monotonic()
    try:
        a = Args(argv, ["index", "by", "control-socket", "timeout-ms"])
        index = a.required_integer("index", 0, 65535)
        by = a.integer("by", 1, 1, 4294967295)
        timeout_ms = a.integer("timeout-ms", 5000, 1, 3600000)
    except UsageError as e:
        log(f"counter-increment: {e}")
        return EXIT_USAGE

    def finish(connected, value, error, code):
        emit({"schemaVersion": SCHEMA_VERSION, "adapter": ADAPTER, "operation": "counter-increment",
              "ok": error is None, "error": error, "connected": connected, "iin": None, "objects": [],
              "index": index, "value": value, "elapsedMs": int((time.monotonic() - start) * 1000)})
        return code

    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(timeout_ms / 1000)
        try:
            s.connect(a.str("control-socket", DEFAULT_CONTROL_SOCKET))
        except OSError as e:
            return finish(False, None, {"code": "connect-failed", "message": str(e)}, EXIT_CONNECT_FAILED)
        try:
            s.sendall((json.dumps({"operation": "counter-increment", "index": index, "by": by}) + "\n").encode())
            with s.makefile("rb") as f:
                reply = json.loads(f.readline())
            if not reply.get("ok"):
                raise ValueError(reply.get("message", "no answer"))
        except (OSError, ValueError) as e:
            return finish(True, None, {"code": "failed", "message": str(e)}, EXIT_FAILED)
    return finish(True, reply["value"], None, EXIT_OK)


async def dial_master(runner, host, port):
    """The outstation's side of a dual end point: dial the master, serve the connection, dial again."""
    while True:
        channel = TcpClientChannel(config=TcpConfig(host=host, port=port))
        try:
            await asyncio.wait_for(channel.open(), 5)
        except (ChannelError, OSError, TimeoutError):
            await asyncio.sleep(1)
            continue
        try:
            await runner._handle_connection(channel)
        finally:
            try:
                await channel.close()
            except (ChannelError, OSError):
                pass
        await asyncio.sleep(0.5)


async def serve(outstation, fx, bind, port, ready_file, control_socket, local, remote, dial=None):
    runner = EventRunner(outstation=outstation, host=bind, port=port)
    if dial is not None:
        task = asyncio.create_task(dial_master(runner, *dial))
        address = f"{dial[0]}:{dial[1]}"
    else:
        task = asyncio.create_task(runner.run())
        address = f"{bind}:{port}"
    deadline = time.monotonic() + 5
    while dial is None and runner.local_address is None:
        if task.done():
            log(f"server: cannot listen on {bind}:{port}: {task.exception()}")
            return EXIT_FAILED
        if time.monotonic() > deadline:
            log(f"server: not listening on {bind}:{port} after 5 s")
            return EXIT_FAILED
        await asyncio.sleep(0.01)
    try:
        if os.path.exists(control_socket):
            os.remove(control_socket)
        control = await asyncio.start_unix_server(partial(serve_control, outstation.database), control_socket)
    except OSError as e:
        log(f"server: cannot open the control socket {control_socket}: {e}")
        return EXIT_FAILED
    try:
        await asyncio.to_thread(write_ready_file, ready_file)
    except OSError as e:
        log(f"server: cannot write the readiness file {ready_file}: {e}")
        return EXIT_FAILED
    emit({"event": "ready", "adapter": ADAPTER, "address": address, "fixture": fx["name"],
          "linkAddress": local, "masterAddress": remote, "transport": "tcp", "dial": dial is not None})
    stop = asyncio.Event()
    loop = asyncio.get_running_loop()
    for sig in (signal.SIGTERM, signal.SIGINT):
        loop.add_signal_handler(sig, stop.set)
    done, _ = await asyncio.wait([task, asyncio.create_task(stop.wait())], return_when=asyncio.FIRST_COMPLETED)
    signalled = stop.is_set()
    if task in done and not signalled:
        log(f"server: the runner ended without a signal: {task.exception() if not task.cancelled() else 'cancelled'}")
    control.close()
    for path in (ready_file, control_socket):
        try:
            os.remove(path)
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
            # The library has the function codes and frames free-format
            # blocks; the master's group 70 objects are the adapter's.
            # The outstation has no file transfer.
            "fileTransfer": True,
            "fileServer": False,
            "fileClient": True,
            "fileDeleteClient": True,
            "fileAppendServer": False,
            "fileAppendClient": True,
            "fileAuthServer": False,
            "fileAuthClient": True,
            "secureAuthentication": False,
            # Contract 1.2: g31 and g33 through the library's analog decoder;
            # g13 and g43 framed by the library, decoded by the adapter; g50v4,
            # which the library cannot frame, framed and decoded by the adapter.
            "commandEventsServer": False,
            "commandEventsClient": True,
            "frozenAnalogInputsClient": True,
            "timeAndIntervalClient": True,
            # Dual end point over TCP, through the runners' channel injection
            # (the master's channel parameter, the outstation's connection
            # handler); no TLS in the library.
            "dualEndpointServer": True,
            "dualEndpointClient": True,
            "dualEndpointClientTls": False,
            "securityStatisticsServer": False,
            "securityStatisticsClient": False,
            "deviceAttributesServer": False,
            # The library frames group 0; the adapter decodes and encodes it.
            "deviceAttributesClient": True,
            "multipleControlsClient": True,
            # dnp3py's outstation carries out every control of a request,
            # whatever max_controls_per_request says.
            "maxControlsServer": False,
            # Contract 1.3: the fixture's eventBufferSize, and IIN2.3 when it overflows.
            "eventBufferServer": True,
            # Contract 1.3: none of the link layer is configurable; the
            # adapter writes the library's REQUEST_LINK_STATUS frame itself.
            "linkConfirmsServer": False,
            "linkConfirmsClient": False,
            "keepAliveClient": True,
            "linkStatusEventServer": False,
            # Contract 1.3: the outstation ignores broadcast addresses; the
            # master sends to 0xFFFF when it is the outstation address.
            "broadcastServer": False,
            "broadcastEventServer": False,
            "broadcastClient": True,
            # Contract 1.3: the master sends the management functions, the
            # outstation has none.
            "manageServer": False,
            "manageClient": True,
            "multipleOutstationsServer": False,
            # The master sends FREEZE AT TIME and freezes of any group; the
            # outstation has neither (NO_FUNC_CODE_SUPPORT, counters only).
            "freezeAtTimeServer": False,
            "freezeAtTimeClient": True,
            "analogFreezeServer": False,
            "analogFreezeClient": True,
            # Contract 1.4.
            "counterIncrementServer": True,
            "frozenCounterEventsServer": False,
            "frozenCounterEventTimeClient": True,
            # Contract 1.3: the master writes g34v3; the outstation's
            # deadbands are configuration only.
            "deadbandWriteServer": False,
            "deadbandWriteClient": True,
            "deadbandReadServer": False,
            "deadbandReadClient": False,
            # Contract 1.5.
            "activateConfigServer": False,
            "activateConfigClient": True,
            "operateDelayClient": True,
            "operateWithoutSelectClient": True,
            "noConfirmClient": True,
            "noUnsolicitedConfirmClient": True,
        },
        # Contract 1.4: it raises no frozen counter events, so it has no policy.
        "frozenEventPolicies": [],
        "unsupportedReasons": {
            "frozenCounterEventsServer": "dnp3py's outstation has no frozen counter event: its encoder would report "
                                         "one as a counter event of group 22, and the fixture rules refuse a class "
                                         "on a frozen counter",
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
    if command == "counter-increment":
        return counter_increment_main(rest)
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
