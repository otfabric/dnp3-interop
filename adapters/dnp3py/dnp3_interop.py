# SPDX-License-Identifier: MIT
"""dnp3-interop adapter for dnp3py. Implements docs/CONTAINER_CONTRACT.md.

The client (master) role only: dnp3py's outstation cannot serve the baseline
fixture (it has no double-bit inputs, no octet strings and no per-point
variations), so `server` is refused as a role the adapter does not have.

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
import os
import struct
import sys
import time
from datetime import UTC, datetime
from importlib.metadata import version as package_version

from dnp3.application.fragment import ObjectBlock, RequestFragment
from dnp3.application.header import RequestHeader
from dnp3.application.qualifiers import ObjectHeader, PrefixCode, RangeCode
from dnp3.core.enums import FunctionCode
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
from dnp3.master.tcp_runner import LinkError, LinkResetPolicy, MasterRunnerError, MasterTcpRunner, ResponseTimeoutError
from dnp3.objects.layout import layout_for

ADAPTER = "dnp3py"
SCHEMA_VERSION = "1.0"
ADAPTER_VERSION = os.environ.get("DNP3_INTEROP_ADAPTER_VERSION", "dev")
UPSTREAM_REVISION = os.environ.get("DNP3_INTEROP_UPSTREAM_REVISION", "unknown")

EXIT_OK, EXIT_FAILED, EXIT_USAGE, EXIT_CONNECT_FAILED = 0, 1, 2, 3

OPERATIONS = [
    "integrity-poll", "event-poll", "read", "operate", "time-sync", "restart", "enable-unsolicited",
    "disable-unsolicited", "assign-class", "freeze", "monitor",
]

USAGE = """usage: dnp3-interop <command>

  client <operation> --host HOST [--port N] [--connect-timeout-ms N] [--timeout-ms N]
         [--collect-ms N] [LINK] [operation flags]
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
      freeze               [--clear] [--no-ack]
      monitor              [--duration-ms N]
  print-capabilities
  print-fixture [NAME]
  version

  LINK: [--local-addr N] [--remote-addr N]

The adapter has no server role. See docs/CONTAINER_CONTRACT.md in
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
        if v is None or v != v or v in (float("inf"), float("-inf")):
            raise UsageError(f"--{name} must be a number")
        return v

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
        headers = [all_objects(20, 0)]
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
    except Exception as exc:  # noqa: BLE001 - dnp3py's ChannelConnectionError, OSError, a timeout: no connection
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
        except Exception as exc:  # noqa: BLE001 - the contract wants a result document in every case
            failure = Failure("failed", f"{type(exc).__name__}: {exc}")

    command_status = recorder.statuses[-1] if recorder.statuses else None
    if failure is None and connected and op != "monitor":
        if (recorder.iin or 0) & REQUEST_ERRORS and not getattr(request, "no_response", False):
            message = "the outstation refused the request (IIN2)"
            if command_status is not None:
                message += f"; it echoed {status_name(command_status)}"
            failure = Failure("rejected", message)
            command_status = None
        elif op == "operate" and command_status is not None and command_status != 0:
            failure = Failure("command-failed", f"the outstation answered {status_name(command_status)}")
        elif op == "operate" and command_status is None and not getattr(request, "no_response", False):
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
    flags = {"host", "port", "local-addr", "remote-addr", "connect-timeout-ms", "timeout-ms", "collect-ms"}
    switches = set()
    flags |= {
        "event-poll": {"classes"}, "enable-unsolicited": {"classes"}, "disable-unsolicited": {"classes"},
        "read": {"group", "variation", "start", "stop"},
        "operate": {"type", "index", "code", "count", "on-ms", "off-ms", "value", "mode"},
        "time-sync": {"procedure"}, "restart": {"type"}, "assign-class": {"class", "group", "start", "stop"},
        "monitor": {"duration-ms"},
    }.get(op, set())
    if op == "freeze":
        switches = {"clear", "no-ack"}
    try:
        a = Args(argv[1:], flags, switches)
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

        async def request(c):  # noqa: F811 - marks the request as one without a response
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
    if op == "operate":
        doc["commandStatus"] = None if command_status is None else status_name(command_status)
    if op == "restart":
        doc["restartDelayMs"] = recorder.delay_ms
    doc["elapsedMs"] = int((time.monotonic() - started) * 1000)
    emit(doc)
    if failure is None:
        return EXIT_OK
    return EXIT_CONNECT_FAILED if failure.code == "connect-failed" else EXIT_FAILED


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
        "roles": {"server": False, "client": True},
        "features": {
            "integrityPoll": True,
            "eventPoll": True,
            "read": True,
            "readAllObjectsClient": True,
            "directOperate": True,
            "selectBeforeOperate": True,
            "parameterErrorWithNotSupported": False,
            "directOperateNoAckServer": False,
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
            "freezeNoAckServer": False,
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
            "secureAuthentication": False,
        },
        "pointTypes": [],
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
        log("server: this adapter has no server role (see print-capabilities)")
        return EXIT_USAGE
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
