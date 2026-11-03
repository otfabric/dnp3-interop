# SPDX-License-Identifier: MIT
"""dnp3-interop adapter for nfm-dnp3 (import name dnp3py). Implements docs/CONTAINER_CONTRACT.md.

Client role only: nfm-dnp3 is a DNP3 master over TCP and has no outstation,
so `server` and `counter-increment` are usage errors (exit 2).

Every request goes through the library's master: its application layer
builds the APDU (ApplicationLayer.build_request with its ObjectHeader and
AppLayerFunction types, the same path its own polls and controls take), its
transport and data link layers carry it, and its response path receives,
reassembles, confirms and parses the answer (DNP3Master._send_request and
_receive_response). Where the library has a high-level call for a request,
the adapter builds the same request; it does not use those calls because they
return a bool, not the response the result document needs.

Measurements come from the library alone: the object headers its response
parser framed, each decoded by the library's decoder for that kind of object
(BinaryInput, BinaryOutput, Counter, AnalogInput, AnalogOutput) where that
decoder implements the object's layout (IEEE 1815-2012 Annex A). An object
the library cannot frame or has no decoder for (double-bit inputs, octet
strings, objects with a time the decoders do not read) is left out, never
decoded here. Control statuses come from the library's command decoders
(BinaryOutputCommand, AnalogOutputCommand). The one field read here from an
object the library framed is the 16-bit time delay of group 52, which the
library frames but does not decode: the restart delay and the outstation's
turnaround in the non-LAN time synchronization.

nfm-dnp3 does not tell solicited from unsolicited responses: it takes
whatever fragment arrives first as the answer to its request. The outstations
send a null unsolicited response at the start of a connection, which would
then answer the first request. The adapter avoids that: a fragment the library
returns with the UNS bit set (which it has already confirmed) is recorded as
unsolicited, and the library's receive path is asked again for the response.
"""

import json
import logging
import math
import os
import struct
import sys
import time
from datetime import UTC, datetime
from importlib.metadata import version as package_version

from dnp3py import DNP3Config, DNP3Master
from dnp3py.core.config import AppLayerFunction, QualifierCode
from dnp3py.core.exceptions import (
    DNP3CommunicationError,
    DNP3Error,
    DNP3TimeoutError,
)
from dnp3py.layers.application import ObjectHeader
from dnp3py.objects.analog import AnalogInput, AnalogOutput, AnalogOutputCommand
from dnp3py.objects.binary import (
    BinaryInput,
    BinaryOutput,
    BinaryOutputCommand,
    parse_binary_inputs,
    parse_binary_outputs,
)
from dnp3py.objects.counter import Counter
from dnp3py.objects.groups import get_object_size
from dnp3py.utils.logging import setup_logging

ADAPTER = "nfmdnp3"
PACKAGE = "nfm-dnp3"
SCHEMA_VERSION = "1.0"
ADAPTER_VERSION = os.environ.get("DNP3_INTEROP_ADAPTER_VERSION", "dev")
UPSTREAM_REVISION = os.environ.get("DNP3_INTEROP_UPSTREAM_REVISION", "unknown")
DEBUG = os.environ.get("DNP3_INTEROP_DEBUG") == "1"

EXIT_OK, EXIT_FAILED, EXIT_USAGE, EXIT_CONNECT_FAILED = 0, 1, 2, 3

OPERATIONS = [
    "integrity-poll", "event-poll", "read", "operate", "time-sync", "restart", "enable-unsolicited",
    "disable-unsolicited", "assign-class", "freeze", "monitor", "manage", "deadband-write",
]

# Operations of the contract this image does not have, and why.
MISSING_OPERATIONS = {
    "operate-multi": "multipleControlsClient is false: the library's response parser misframes every object "
                     "header after the first one that has an index prefix, so the echo of several controls "
                     "cannot be read",
    "attribute-read": "deviceAttributesClient is false: the library cannot frame group 0",
    "attribute-write": "deviceAttributesClient is false: the library cannot frame group 0",
    "file-get": "fileClient is false: the library cannot encode or frame group 70 (qualifier 0x5B)",
    "file-put": "fileClient is false: the library cannot encode or frame group 70 (qualifier 0x5B)",
    "file-ls": "fileClient is false: the library cannot encode or frame group 70 (qualifier 0x5B)",
    "file-info": "fileClient is false: the library cannot encode or frame group 70 (qualifier 0x5B)",
    "file-delete": "fileDeleteClient is false: the library cannot encode or frame group 70 (qualifier 0x5B)",
    "file-authenticate": "fileAuthClient is false: the library cannot encode or frame group 70 (qualifier 0x5B)",
}

# Flags of the contract that need a feature this image declares false.
UNSUPPORTED_FLAGS = {
    "tls": "tls", "tls-ca": "tls", "tls-cert": "tls", "tls-key": "tls", "tls-server-name": "tls",
    "listen": "dualEndpointClient", "bind-address": "dualEndpointClient",
    "serial-device": "serial", "baud": "serial", "data-bits": "serial", "parity": "serial", "stop-bits": "serial",
    "udp-local-port": "udp",
    "sa-update-key": "secureAuthentication", "sa-user": "secureAuthentication", "sa-mac": "secureAuthentication",
    "no-confirm": "noConfirmClient", "no-unsolicited-confirm": "noUnsolicitedConfirmClient",
    "link-confirms": "linkConfirmsClient", "keep-alive-ms": "keepAliveClient",
    "broadcast": "broadcastClient", "file-user": "fileAuthClient", "file-password": "fileAuthClient",
}
UNSUPPORTED_SWITCHES = {"tls", "listen", "no-confirm", "no-unsolicited-confirm", "link-confirms", "broadcast"}

USAGE = """usage: dnp3-interop <command>

  client <operation> --host HOST [--port N] [--connect-timeout-ms N] [--timeout-ms N]
         [--collect-ms N] [--transport tcp] [LINK] [operation flags]
      integrity-poll
      event-poll           [--classes 1,2,3]
      read                 --group G --variation V [--start N --stop N]
      operate              --type g12v1|g41v1..4 --index N [--mode direct|sbo|direct-no-ack|operate]
                           [--operate-delay-ms N] (sbo only)
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
      manage               --function initialize-data|initialize-application|start-application|
                                      stop-application|save-configuration
      deadband-write       --deadbands INDEX:VALUE[,...]
  print-capabilities
  print-fixture [NAME]
  version

  LINK: [--local-addr N] [--remote-addr N]

nfm-dnp3 is a master only: this image has no server (outstation) role.

See docs/CONTAINER_CONTRACT.md in
https://github.com/otfabric/dnp3-interop
"""

IIN_NAMES = [
    "BROADCAST", "CLASS_1_EVENTS", "CLASS_2_EVENTS", "CLASS_3_EVENTS", "NEED_TIME", "LOCAL_CONTROL",
    "DEVICE_TROUBLE", "DEVICE_RESTART", "NO_FUNC_CODE_SUPPORT", "OBJECT_UNKNOWN", "PARAMETER_ERROR",
    "EVENT_BUFFER_OVERFLOW", "ALREADY_EXECUTING", "CONFIG_CORRUPT", "RESERVED_2", "RESERVED_1",
]
REQUEST_ERRORS = 0x0700  # IIN2.0 NO_FUNC_CODE_SUPPORT, IIN2.1 OBJECT_UNKNOWN, IIN2.2 PARAMETER_ERROR

COMMAND_STATUS = [
    "SUCCESS", "TIMEOUT", "NO_SELECT", "FORMAT_ERROR", "NOT_SUPPORTED", "ALREADY_ACTIVE", "HARDWARE_ERROR",
    "LOCAL", "TOO_MANY_OPS", "NOT_AUTHORIZED", "AUTOMATION_INHIBIT", "PROCESSING_LIMITED", "OUT_OF_RANGE",
    "DOWNSTREAM_LOCAL", "ALREADY_COMPLETE", "BLOCKED", "CANCELLED", "BLOCKED_OTHER_MASTER", "DOWNSTREAM_FAIL",
]

# Control codes of the contract, as the octet of the CROB (IEEE 1815-2012
# A.12.1: trip-close code 01 close, 10 trip, in bits 7..6). nfm-dnp3's
# BinaryOutputCommand takes the octet as it is.
CODES = {"LATCH_ON": 0x03, "LATCH_OFF": 0x04, "PULSE_ON": 0x01, "PULSE_OFF": 0x02, "CLOSE": 0x41, "TRIP": 0x81}

COMMON_FLAGS = ["ONLINE", "RESTART", "COMM_LOST", "REMOTE_FORCED", "LOCAL_FORCED"]

# The library's decoder for each object, where its layout is the object's
# (IEEE 1815-2012 Annex A) and the library's size table agrees: (decoder, the
# variation the decoder takes, size of one object). The library's own poll
# parser hands every event group to the static decoder of the same variation
# number, which for g2v1, g32v3/v4/v7/v8 and g42v3/v4/v7/v8 is another layout;
# here the decoder is chosen by layout. Objects with a time stamp the decoders
# do not read (counter and analog events with time, frozen values with time)
# are not in the table.
BI, BO, CT, AI, AO = "binary-input", "binary-output", "counter", "analog-input", "analog-output"
DECODERS = {
    (1, 2): (BI, 2), (2, 1): (BI, 2), (2, 2): (BI, 2),
    (10, 2): (BO, 2), (11, 1): (BO, 2),
    # g11v2 is the layout of g2v2 (flags with the state in bit 7, 48-bit time),
    # which BinaryInput decodes with its time; BinaryOutput reads no time.
    (11, 2): (BI, 2),
    **{(20, v): (CT, v) for v in range(1, 9)},
    **{(g, v): (CT, v) for g in (21, 22, 23) for v in range(1, 5)},
    **{(30, v): (AI, v) for v in range(1, 7)},
    (31, 1): (AI, 1), (31, 2): (AI, 2), (31, 5): (AI, 3), (31, 6): (AI, 4), (31, 7): (AI, 5), (31, 8): (AI, 6),
    **{(g, v): (AI, d) for g in (32, 33) for v, d in ((1, 1), (2, 2), (5, 5), (6, 6))},
    **{(40, v): (AO, v) for v in range(1, 5)},
    (42, 1): (AO, 1), (42, 2): (AO, 2), (42, 5): (AO, 3), (42, 6): (AO, 4),
}
# Sizes of one object by IEEE 1815-2012 Annex A, for the objects of DECODERS:
# an object is decoded only where the library's size table says the same.
IEEE_SIZE = {
    (1, 2): 1, (2, 1): 1, (2, 2): 7, (10, 2): 1, (11, 1): 1, (11, 2): 7,
    (20, 1): 5, (20, 2): 3, (20, 3): 5, (20, 4): 3, (20, 5): 4, (20, 6): 2, (20, 7): 4, (20, 8): 2,
    **{(g, v): s for g in (21, 22, 23) for v, s in ((1, 5), (2, 3), (3, 5), (4, 3))},
    (30, 1): 5, (30, 2): 3, (30, 3): 4, (30, 4): 2, (30, 5): 5, (30, 6): 9,
    (31, 1): 5, (31, 2): 3, (31, 5): 4, (31, 6): 2, (31, 7): 5, (31, 8): 9,
    **{(g, v): s for g in (32, 33) for v, s in ((1, 5), (2, 3), (5, 5), (6, 9))},
    (40, 1): 5, (40, 2): 3, (40, 3): 5, (40, 4): 9,
    (42, 1): 5, (42, 2): 3, (42, 5): 5, (42, 6): 9,
}
TIMED = {(2, 2), (11, 2)}
# Index prefixes the library frames correctly. It reads 0x28 with a one-octet
# count and 0x29 with a two-octet one (IEEE 1815-2012 Table 4-5: two and four
# octets), so its count and the start of the objects are wrong there: such a
# header is left out, its objects are not the ones on the wire.
PREFIX_SIZE = {QualifierCode.UINT8_COUNT_UINT8_INDEX: 1}
RANGES = {QualifierCode.UINT8_START_STOP, QualifierCode.UINT16_START_STOP}


def log(message):
    print(f"[{ADAPTER}] {message}", file=sys.stderr)


def emit(doc):
    sys.stdout.write(json.dumps(doc, separators=(",", ":"), ensure_ascii=False) + "\n")
    sys.stdout.flush()


def time_from_ms(ms):
    dt = datetime.fromtimestamp(ms / 1000, UTC)
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


class Failure(Exception):
    def __init__(self, code, message):
        super().__init__(message)
        self.code = code
        self.message = message


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
            if name in UNSUPPORTED_FLAGS and name not in flags and name not in switches:
                raise UsageError(f"--{name} needs the feature {UNSUPPORTED_FLAGS[name]}, which this image declares false")
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

    def classes(self, name, default, minimum):
        out = []
        for item in self.str(name, default).split(","):
            if len(item) != 1 or not item.isdigit() or not minimum <= int(item) <= 3:
                raise UsageError(f"--{name} is a comma-separated list of classes {minimum}..3")
            out.append(int(item))
        return out

    def range(self):
        """--start and --stop together, or neither."""
        if self.has("start") != self.has("stop"):
            raise UsageError("--start and --stop go together")
        if not self.has("start"):
            return None
        start, stop = self.integer("start", 0, 0, 65535), self.integer("stop", 0, 0, 65535)
        if start > stop:
            raise UsageError("--start is larger than --stop")
        return start, stop


# ---- what the library parsed ----


class Recorder:
    """Objects, the IIN of the last solicited response, control statuses and delays, as the library parsed them."""

    def __init__(self):
        self.objects = []
        self.iin = None
        self.statuses = []
        self.delay_ms = None
        self.headers = []  # (group, variation) of every header of the last solicited response

    def record(self, response, unsolicited):
        raw = response.raw_data
        if not unsolicited:
            self.iin = response.iin1 | (response.iin2 << 8)
            self.statuses = []
            self.delay_ms = None
            self.headers = [(h.group, h.variation) for h in response.objects]
        for header in response.objects:
            g, v = header.group, header.variation
            if g == 12 and v == 1:
                self._statuses(header, raw, 11, lambda d, i: BinaryOutputCommand.from_bytes(d, i).status)
            elif g == 41 and v in (1, 2, 3, 4):
                size = get_object_size(g, v)
                self._statuses(header, raw, size, lambda d, i, v=v: AnalogOutputCommand.from_bytes(d, i, v).status)
            elif g == 52 and v in (1, 2) and not unsolicited:
                # The library frames g52 but has no decoder: the 16-bit delay.
                if header.count >= 1 and header.data_offset + 2 <= len(raw):
                    delay = int.from_bytes(raw[header.data_offset : header.data_offset + 2], "little")
                    self.delay_ms = delay * 1000 if v == 1 else delay
            else:
                self._measurements(header, raw, unsolicited)

    def _statuses(self, header, raw, size, status_of):
        if header.qualifier in PREFIX_SIZE:
            prefix = PREFIX_SIZE[header.qualifier]
        elif header.qualifier in RANGES:
            prefix = 0
        else:
            return
        for i in range(header.count):
            at = header.data_offset + i * (prefix + size)
            if at + prefix + size > len(raw):
                break
            index = int.from_bytes(raw[at : at + prefix], "little") if prefix else header.range_start + i
            self.statuses.append(status_of(raw[at + prefix : at + prefix + size], index))

    def _measurements(self, header, raw, unsolicited):
        g, v = header.group, header.variation
        if header.count == 0:
            return
        if (g, v) in ((1, 1), (10, 1)) and header.qualifier in RANGES:
            # Packed: the library's parser of the static values (1 bit a point).
            data = raw[header.data_offset : header.data_offset + (header.count + 7) // 8]
            parse = parse_binary_inputs if g == 1 else parse_binary_outputs
            for p in parse(data, header.range_start, header.count, 1):
                self._add(g, v, p.index, p.value, p.flags, None, unsolicited)
            return
        if (g, v) not in DECODERS or get_object_size(g, v) != IEEE_SIZE[(g, v)]:
            return
        kind, variation = DECODERS[(g, v)]
        size = IEEE_SIZE[(g, v)]
        if header.qualifier in RANGES:
            prefix = 0
        elif header.qualifier in PREFIX_SIZE:
            prefix = PREFIX_SIZE[header.qualifier]
        else:
            return
        for i in range(header.count):
            at = header.data_offset + i * (prefix + size)
            if at + prefix + size > len(raw):
                break
            index = int.from_bytes(raw[at : at + prefix], "little") if prefix else header.range_start + i
            data = raw[at + prefix : at + prefix + size]
            if kind == BI:
                p = BinaryInput.from_bytes(data, index, variation)
                self._add(g, v, index, p.value, p.flags, p.timestamp, unsolicited)
            elif kind == BO:
                p = BinaryOutput.from_bytes(data, index, variation)
                self._add(g, v, index, p.value, p.flags, None, unsolicited)
            elif kind == CT:
                p = Counter.from_bytes(data, index, variation)
                self._add(g, v, index, p.value, p.flags, None, unsolicited)
            elif kind == AI:
                p = AnalogInput.from_bytes(data, index, variation)
                self._add(g, v, index, number(p.value), p.flags, None, unsolicited)
            else:
                p = AnalogOutput.from_bytes(data, index, variation)
                self._add(g, v, index, number(p.value), p.flags, None, unsolicited)

    def _add(self, g, v, index, value, flags, stamp, unsolicited):
        o = {"group": g, "variation": v, "index": index, "value": value, "flags": flag_names(g, int(flags))}
        if (g, v) in TIMED and stamp is not None:
            o["time"] = time_from_ms(stamp)
        o["unsolicited"] = unsolicited
        self.objects.append(o)


# ---- the master ----


class Session:
    """One connection of the library's master, and what it received."""

    def __init__(self, master, timeout):
        self.master = master
        self.timeout = timeout
        self.recorder = Recorder()

    def _receive(self, deadline):
        left = deadline - time.monotonic()
        if left <= 0.001:
            raise DNP3TimeoutError("Response timeout", timeout_seconds=self.timeout)
        return self.master._receive_response(left)

    def request(self, function, headers=(), expect_response=True):
        """Sends one request; returns the solicited response, after any unsolicited ones that came first."""
        apdu = self.master._application.build_request(function, list(headers))
        deadline = time.monotonic() + self.timeout
        response = self.master._send_request(apdu, expect_response=expect_response, timeout=self.timeout)
        if not expect_response:
            return None
        while response.unsolicited:
            # The library took an unsolicited fragment (and confirmed it) as
            # the answer; the response to the request is still to come.
            self.recorder.record(response, unsolicited=True)
            response = self._receive(deadline)
        self.recorder.record(response, unsolicited=False)
        return response

    def refused(self):
        return bool((self.recorder.iin or 0) & REQUEST_ERRORS)

    def monitor(self, seconds):
        """Receives unsolicited responses (or whatever arrives) for this long; the library confirms them."""
        end = time.monotonic() + seconds
        while (left := end - time.monotonic()) > 0.001:
            try:
                response = self.master._receive_response(left)
            except DNP3TimeoutError:
                continue
            self.recorder.record(response, unsolicited=response.unsolicited)


def all_objects(group, variation):
    return ObjectHeader(group=group, variation=variation, qualifier=QualifierCode.ALL_OBJECTS)


def start_stop(group, variation, start, stop):
    return ObjectHeader(group=group, variation=variation, qualifier=QualifierCode.UINT16_START_STOP,
                        range_start=start, range_stop=stop)


def count_one(group, variation, payload):
    """One object without an index: the time objects."""
    return ObjectHeader(group=group, variation=variation, qualifier=QualifierCode.UINT8_COUNT, count=1, data=payload)


def prefixed(group, variation, items):
    """Objects with an index prefix: qualifier 0x17 (one-octet count and index) where every index fits.

    The library names 0x28 "8-bit count, 16-bit index" and writes a one-octet
    count with it, but 0x28 is a two-octet count (IEEE 1815-2012 Table 4-5,
    range specifier code 8), so an outstation reads the count and every index
    after it wrongly. Its own controls send that. 0x17 the library writes
    correctly; for an index above 255 it has no correct qualifier, and the
    adapter sends what the library sends (0x28).
    """
    if all(i <= 0xFF for i, _ in items):
        qualifier, octets = QualifierCode.UINT8_COUNT_UINT8_INDEX, 1
    else:
        qualifier, octets = QualifierCode.UINT8_COUNT_UINT16_INDEX, 2
    data = b"".join(i.to_bytes(octets, "little") + payload for i, payload in items)
    return ObjectHeader(group=group, variation=variation, qualifier=qualifier, count=len(items), data=data)


def control(group, variation, index, payload):
    """One control object with an index prefix."""
    return prefixed(group, variation, [(index, payload)])


def time48(ms):
    return int(ms).to_bytes(6, "little")


def class_headers(classes):
    return [all_objects(60, 1 + c) for c in classes]  # g60v2..v4: classes 1..3


# ---- operations ----


def parse_operation(op, a):
    """Returns a function taking a Session, after checking every flag."""
    F = AppLayerFunction
    if op == "integrity-poll":
        # Classes 1, 2, 3, then 0, as the contract has it (the library's own
        # integrity poll asks for class 0 first).
        headers = class_headers([1, 2, 3]) + [all_objects(60, 1)]
        return lambda s: s.request(F.READ, headers)
    if op == "event-poll":
        headers = class_headers(a.classes("classes", "1,2,3", 1))
        return lambda s: s.request(F.READ, headers)
    if op == "read":
        g, v = a.required_integer("group", 0, 255), a.required_integer("variation", 0, 255)
        r = a.range()
        header = start_stop(g, v, *r) if r else all_objects(g, v)
        return lambda s: s.request(F.READ, [header])
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
        fc = F.COLD_RESTART if kind == "cold" else F.WARM_RESTART
        return lambda s: s.request(fc)
    if op in ("enable-unsolicited", "disable-unsolicited"):
        # The headers of the library's enable_unsolicited and disable_unsolicited.
        headers = class_headers(a.classes("classes", "1,2,3", 1))
        fc = F.ENABLE_UNSOLICITED if op == "enable-unsolicited" else F.DISABLE_UNSOLICITED
        return lambda s: s.request(fc, headers)
    if op == "assign-class":
        k, g = a.required_integer("class", 0, 3), a.required_integer("group", 0, 255)
        r = a.range()
        headers = [all_objects(60, 1 + k), start_stop(g, 0, *r) if r else all_objects(g, 0)]
        return lambda s: s.request(F.ASSIGN_CLASS, headers)
    if op == "freeze":
        return parse_freeze(a)
    if op == "manage":
        return parse_manage(a)
    if op == "deadband-write":
        return parse_deadbands(a)
    raise UsageError(f"unknown operation '{op}'")


def parse_operate(a):
    F = AppLayerFunction
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
        command = BinaryOutputCommand(
            index=index, control_code=CODES[code], count=a.integer("count", 1, 0, 255),
            on_time=a.integer("on-ms", 100, 0, 2**32 - 1), off_time=a.integer("off-ms", 100, 0, 2**32 - 1),
        )
        obj = control(12, 1, index, command.to_bytes())
    elif kind in ("g41v1", "g41v2", "g41v3", "g41v4"):
        if any(a.has(f) for f in ("code", "count", "on-ms", "off-ms")):
            raise UsageError("--code, --count, --on-ms and --off-ms are for g12v1")
        value = a.number("value")
        variation = int(kind[-1])
        integral = value == int(value)
        if variation == 1 and not (integral and -(2**31) <= value <= 2**31 - 1):
            raise UsageError("--value is out of the range of g41v1")
        if variation == 2 and not (integral and -32768 <= value <= 32767):
            raise UsageError("--value is out of the range of g41v2")
        if variation == 3 and abs(value) > 3.4028234663852886e38:
            raise UsageError("--value is out of the range of g41v3")
        if variation in (1, 2):
            value = int(value)
        obj = control(41, variation, index, AnalogOutputCommand.create(index, value).to_bytes(variation))
    else:
        raise UsageError("--type must be g12v1, g41v1, g41v2, g41v3 or g41v4")
    if a.has("operate-delay-ms") and mode != "sbo":
        raise UsageError("--operate-delay-ms needs --mode sbo")
    delay = a.integer("operate-delay-ms", 0, 0, 3600000)

    def run(s):
        if mode == "direct-no-ack":
            s.request(F.DIRECT_OPERATE_NO_ACK, [obj], expect_response=False)
        elif mode == "direct":
            s.request(F.DIRECT_OPERATE, [obj])
        elif mode == "operate":
            # An OPERATE without a SELECT (contract 1.5): the outstation answers NO_SELECT.
            s.request(F.OPERATE, [obj])
        else:
            s.request(F.SELECT, [obj])
            # The OPERATE follows only a successful SELECT, with no request between.
            if s.recorder.statuses and s.recorder.statuses[-1] == 0 and not s.refused():
                if delay:
                    time.sleep(delay / 1000)  # --operate-delay-ms (contract 1.5)
                s.request(F.OPERATE, [obj])

    run.no_response = mode == "direct-no-ack"
    return run


def parse_freeze(a):
    F = AppLayerFunction
    clear, no_ack = a.has("clear"), a.has("no-ack")
    fc = {
        (False, False): F.IMMEDIATE_FREEZE, (False, True): F.IMMEDIATE_FREEZE_NO_ACK,
        (True, False): F.FREEZE_CLEAR, (True, True): F.FREEZE_CLEAR_NO_ACK,
    }[(clear, no_ack)]
    group = a.integer("group", 20, 20, 30)
    if group not in (20, 30):
        raise UsageError("--group must be 20 or 30")
    headers = [all_objects(group, 0)]
    if a.has("at"):
        # FREEZE AT TIME (contract 1.3): g50v2 (time and interval), then the objects.
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
        fc = F.FREEZE_AT_TIME_NO_ACK if no_ack else F.FREEZE_AT_TIME
        headers = [count_one(50, 2, time48(ms) + struct.pack("<I", interval))] + headers
    elif a.has("interval-ms"):
        raise UsageError("--interval-ms needs --at")

    def run(s):
        s.request(fc, headers, expect_response=not no_ack)

    run.no_response = no_ack
    return run


MANAGE_FUNCTIONS = {
    "initialize-data": (AppLayerFunction.INITIALIZE_DATA, False),
    "initialize-application": (AppLayerFunction.INITIALIZE_APPLICATION, True),
    "start-application": (AppLayerFunction.START_APPLICATION, True),
    "stop-application": (AppLayerFunction.STOP_APPLICATION, True),
    "save-configuration": (AppLayerFunction.SAVE_CONFIGURATION, False),
}


def parse_manage(a):
    function = a.required("function")
    if function == "activate-config":
        raise UsageError("--function activate-config needs the feature activateConfigClient, which this image "
                         "declares false: the library cannot encode its g70v8 (qualifier 0x5B)")
    if a.has("spec"):
        raise UsageError("--spec goes with --function activate-config")
    if function not in MANAGE_FUNCTIONS:
        raise UsageError("--function must be " + ", ".join(MANAGE_FUNCTIONS))
    fc, applications = MANAGE_FUNCTIONS[function]
    headers = [all_objects(90, 1)] if applications else []
    return lambda s: s.request(fc, headers)


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
    if len(items) > 255:
        raise UsageError("--deadbands takes at most 255 items")
    # g34v3 (single precision) with an index prefix.
    header = prefixed(34, 3, [(i, struct.pack("<f", v)) for i, v in items])
    return lambda s: s.request(AppLayerFunction.WRITE, [header])


def time_sync_lan(s):
    recorded = time.time()
    s.request(AppLayerFunction.RECORD_CURRENT_TIME)
    if s.refused():
        return
    s.request(AppLayerFunction.WRITE, [count_one(50, 3, time48(recorded * 1000))])


def time_sync_non_lan(s):
    sent = time.monotonic()
    s.request(AppLayerFunction.DELAY_MEASURE)
    received = time.monotonic()
    if s.refused():
        return
    if s.recorder.delay_ms is None:
        raise Failure("failed", "the answer to DELAY MEASURE did not carry a time delay (g52)")
    turnaround = s.recorder.delay_ms / 1000
    one_way = max((received - sent - turnaround) / 2, 0)
    s.request(AppLayerFunction.WRITE, [count_one(50, 1, time48((time.time() + one_way) * 1000))])


# ---- client ----


class Options:
    pass


def run_client(op, o, request):
    """Opens the connection, runs the operation, closes; returns (connected, failure, recorder)."""
    config = DNP3Config(
        host=o.host, port=o.port, master_address=o.local, outstation_address=o.remote,
        response_timeout=o.timeout, connection_timeout=o.connect_timeout,
        # User data unconfirmed: the library sends CONFIRMED_USER_DATA without
        # a RESET_LINK_STATES and cannot read the link layer's answer to it.
        confirm_required=False, log_level="DEBUG" if DEBUG else "WARNING", log_raw_frames=DEBUG,
    )
    master = DNP3Master(config)
    session = Session(master, o.timeout)
    try:
        master.open()
    except DNP3Error as exc:
        return False, Failure("connect-failed", f"no connection to {o.host}:{o.port}: {exc}"), session.recorder
    failure = None
    try:
        if op == "monitor":
            session.monitor(o.duration)
        else:
            request(session)
    except Failure as exc:
        failure = exc
    except DNP3TimeoutError as exc:
        failure = Failure("timeout", str(exc) or "no response from the outstation")
    except DNP3CommunicationError as exc:
        failure = Failure("connection-lost", str(exc))
    # The rest of what the library raises (its DNP3Error hierarchy for frames,
    # objects and protocol errors) and what its decoders raise on a malformed
    # object: a result document in every case.
    except (DNP3Error, OSError, ValueError, TypeError, struct.error, LookupError, ArithmeticError) as exc:
        failure = Failure("failed", f"{type(exc).__name__}: {exc}")

    r = session.recorder
    no_response = getattr(request, "no_response", False)
    if failure is None and op != "monitor" and not no_response:
        command_status = r.statuses[-1] if r.statuses else None
        if session.refused():
            message = "the outstation refused the request (IIN2)"
            if command_status is not None:
                message += f"; it echoed {status_name(command_status)}"
            failure = Failure("rejected", message)
        elif op == "operate" and command_status is None:
            failure = Failure("failed", "the outstation's echo did not carry a status")
        elif op == "operate" and command_status != 0:
            failure = Failure("command-failed", f"the outstation answered {status_name(command_status)}")
        elif op == "restart" and not any(g == 52 and v in (1, 2) for g, v in r.headers):
            failure = Failure("failed", "the outstation's answer did not carry a delay (g52v1 or g52v2)")
    r.command_status = None
    if op == "operate" and r.statuses and not no_response and (failure is None or failure.code == "command-failed"):
        r.command_status = r.statuses[-1]

    if failure is None and o.collect > 0:
        try:
            session.monitor(o.collect)
        except DNP3Error:
            pass
    master.close()
    return True, failure, r


def client_main(argv):
    if not argv:
        log("client: missing operation")
        return EXIT_USAGE
    op = argv[0]
    if op in MISSING_OPERATIONS:
        log(f"client {op}: {MISSING_OPERATIONS[op]}")
        return EXIT_USAGE
    if op not in OPERATIONS:
        log(f"client: unknown operation '{op}'")
        return EXIT_USAGE
    flags = {"host", "port", "local-addr", "remote-addr", "connect-timeout-ms", "timeout-ms", "collect-ms", "transport"}
    flags |= {
        "event-poll": {"classes"}, "enable-unsolicited": {"classes"}, "disable-unsolicited": {"classes"},
        "read": {"group", "variation", "start", "stop"},
        "operate": {"type", "index", "code", "count", "on-ms", "off-ms", "value", "mode", "operate-delay-ms"},
        "time-sync": {"procedure"}, "restart": {"type"}, "assign-class": {"class", "group", "start", "stop"},
        "monitor": {"duration-ms"}, "freeze": {"group", "at", "interval-ms"},
        "manage": {"function", "spec"}, "deadband-write": {"deadbands"},
    }.get(op, set())
    switches = {"freeze": {"clear", "no-ack"}}.get(op, set())
    o = Options()
    try:
        a = Args(argv[1:], flags, switches)
        transport = a.str("transport", "tcp")
        if transport in ("udp", "serial"):
            raise UsageError(f"--transport {transport}: this image has TCP only (feature {transport} is false)")
        if transport != "tcp":
            raise UsageError("--transport must be tcp, udp or serial")
        o.host = a.required("host")
        o.port = a.integer("port", 20000, 1, 65535)
        o.local = a.integer("local-addr", 1, 0, 65519)
        o.remote = a.integer("remote-addr", 1024, 0, 65519)
        o.connect_timeout = a.integer("connect-timeout-ms", 5000, 1, 3600000) / 1000
        o.timeout = a.integer("timeout-ms", 5000, 1, 3600000) / 1000
        o.collect = a.integer("collect-ms", 0, 0, 3600000) / 1000
        o.duration = a.integer("duration-ms", 1000, 0, 3600000) / 1000
        request = None if op == "monitor" else parse_operation(op, a)
    except UsageError as exc:
        log(f"client {op}: {exc}")
        return EXIT_USAGE

    started = time.monotonic()
    connected, failure, r = run_client(op, o, request)
    reads = op in ("integrity-poll", "event-poll", "read")
    doc = {
        "schemaVersion": SCHEMA_VERSION, "adapter": ADAPTER, "operation": op, "ok": failure is None,
        "error": None if failure is None else {"code": failure.code, "message": failure.message},
        "connected": connected,
        "iin": iin_names(r.iin) if reads and r.iin is not None else None,
        "objects": r.objects,
    }
    if op == "operate":
        cs = getattr(r, "command_status", None)
        doc["commandStatus"] = None if cs is None else status_name(cs)
    if op == "restart":
        doc["restartDelayMs"] = r.delay_ms if failure is None else None
    doc["elapsedMs"] = int((time.monotonic() - started) * 1000)
    emit(doc)
    if failure is None:
        return EXIT_OK
    return EXIT_CONNECT_FAILED if failure.code == "connect-failed" else EXIT_FAILED


# ---- the rest of the commands ----


def print_capabilities():
    emit({
        "schemaVersion": SCHEMA_VERSION,
        "adapter": ADAPTER,
        "adapterVersion": ADAPTER_VERSION,
        "protocol": "dnp3",
        "upstream": {
            "name": PACKAGE, "version": package_version(PACKAGE), "revision": UPSTREAM_REVISION,
            "license": "MIT", "language": "Python", "independentEngine": True,
        },
        # The library is a master only.
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
            "octetStrings": False,
            "tcp": True,
            "udp": False,
            "tls": False,
            "serial": False,
            "fileTransfer": False,
            "fileServer": False,
            "fileClient": False,
            "fileDeleteClient": False,
            "fileAppendServer": False,
            "fileAppendClient": False,
            "fileAuthServer": False,
            "fileAuthClient": False,
            "secureAuthentication": False,
            "commandEventsServer": False,
            "commandEventsClient": False,
            "frozenAnalogInputsClient": True,
            "timeAndIntervalClient": False,
            "dualEndpointServer": False,
            "dualEndpointClient": False,
            "dualEndpointClientTls": False,
            "securityStatisticsServer": False,
            "securityStatisticsClient": False,
            "deviceAttributesServer": False,
            "deviceAttributesClient": False,
            "multipleControlsClient": False,
            "maxControlsServer": False,
            "eventBufferServer": False,
            "linkConfirmsServer": False,
            "linkConfirmsClient": False,
            "keepAliveClient": False,
            "linkStatusEventServer": False,
            "broadcastServer": False,
            "broadcastEventServer": False,
            "broadcastClient": False,
            "manageServer": False,
            "manageClient": True,
            "multipleOutstationsServer": False,
            "freezeAtTimeServer": False,
            "freezeAtTimeClient": True,
            "analogFreezeServer": False,
            "analogFreezeClient": True,
            "counterIncrementServer": False,
            "frozenCounterEventsServer": False,
            "frozenCounterEventTimeClient": False,
            "deadbandWriteServer": False,
            "deadbandWriteClient": True,
            "deadbandReadServer": False,
            "deadbandReadClient": False,
            "activateConfigServer": False,
            "activateConfigClient": False,
            "operateDelayClient": True,
            "operateWithoutSelectClient": True,
            "noConfirmClient": False,
            "noUnsolicitedConfirmClient": False,
        },
        "frozenEventPolicies": [],
        "unsupportedReasons": {
            "octetStrings": "the library's size table has no width for g110 and g111 variations above 0, so its "
                            "response parser stops at an octet string and drops it and every object after it",
            "udp": "the library's master has TCP only",
            "tls": "the library's master has TCP only",
            "serial": "the library's master has TCP only",
            "fileClient": "the library cannot encode or frame free-format objects (qualifier 0x5B): no group 70",
            "fileTransfer": "no outstation, and the master cannot encode or frame group 70 (qualifier 0x5B)",
            "secureAuthentication": "not in the library",
            "commandEventsClient": "the library's size table gives g13 the size of a CROB (11 and 17 octets, IEEE "
                                   "1815-2012 A.13: 1 and 7), and it has no decoder for g13 or g43",
            "timeAndIntervalClient": "the library's size table gives g50v4 6 octets (IEEE 1815-2012 A.27.4: 11)",
            "dualEndpointClient": "the library's master only dials",
            "securityStatisticsClient": "not in the library",
            "deviceAttributesClient": "the library cannot frame group 0",
            "multipleControlsClient": "the library's response parser leaves the index prefix out of the size of "
                                      "an object header (qualifiers 0x17, 0x28, 0x29), so every header after the "
                                      "first such one is misframed and the echo of several controls cannot be read",
            "linkConfirmsClient": "the library sends CONFIRMED_USER_DATA without RESET_LINK_STATES and cannot read "
                                  "the link layer's ACK (a frame without user data)",
            "keepAliveClient": "the library builds REQUEST_LINK_STATUS but never sends it, and cannot read the "
                               "LINK_STATUS answer (a frame without user data)",
            "broadcastClient": "the library refuses 0xFFFF as a destination address",
            "frozenCounterEventTimeClient": "the library's Counter decoder reads no time: g23v5 and g23v6 are left "
                                            "out",
            "deadbandReadClient": "the library has no decoder for group 34",
            "activateConfigClient": "ACTIVATE_CONFIG is not among the library's function codes, and it cannot "
                                    "encode the g70v8 file specification (qualifier 0x5B)",
            "noConfirmClient": "the library confirms every response that asks for it",
            "noUnsolicitedConfirmClient": "the library confirms every unsolicited response that asks for it",
            "counterIncrementServer": "no outstation in the library",
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


def setup_library_logging():
    """The library's logger writes to stdout; the contract keeps stdout for JSON, so it goes to stderr."""
    logger = setup_logging(level="DEBUG" if DEBUG else "WARNING")
    handler = logging.StreamHandler(sys.stderr)
    handler.setFormatter(logging.Formatter(f"[{ADAPTER}] %(levelname)s %(name)s %(message)s"))
    logger.handlers = [handler]


def main(argv):
    setup_library_logging()
    command, rest = (argv[0], argv[1:]) if argv else ("server", [])
    if command == "server":
        log("server: nfm-dnp3 has no outstation; this image has the client role only (roles.server is false)")
        return EXIT_USAGE
    if command == "counter-increment":
        log("counter-increment: this image has no server (counterIncrementServer is false)")
        return EXIT_USAGE
    if command == "client":
        return client_main(rest)
    if command == "print-capabilities" and not rest:
        return print_capabilities()
    if command == "print-fixture" and len(rest) <= 1:
        return print_fixture(rest[0] if rest else "baseline")
    if command == "version" and not rest:
        print(f"dnp3-interop {ADAPTER} {ADAPTER_VERSION} ({PACKAGE} {package_version(PACKAGE)}, {UPSTREAM_REVISION})")
        return EXIT_OK
    if command in ("help", "--help", "-h"):
        sys.stdout.write(USAGE)
        return EXIT_OK
    sys.stderr.write(USAGE)
    return EXIT_USAGE


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
