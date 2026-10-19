# Container contract

Version: **1.0** (`contract_version` in [versions.yaml](../versions.yaml))

Every adapter image exposes the same commands, flags, JSON documents and exit
codes. Consumers depend on this document, not on any adapter's source. A
change that breaks it bumps the major version; an addition bumps the minor
one. Documents carry `schemaVersion` `"1.0"`: it names the compatible line
and changes only with the major version.

| Version | Change |
|---------|--------|
| 1.0 | First release |

Not every adapter has every operation or event. What an image lacks it says
in [`print-capabilities`](#print-capabilities), and an operation it does not
list in `clientOperations` is a usage error (exit 2), as is a flag value that
needs a feature the image declares false (`--mode direct-no-ack` without
`directOperateNoAckClient`, ...).

What an *outstation* does with a request is defined by its fixture: see
[FIXTURES.md](FIXTURES.md).

Roles: `server` is a DNP3 **outstation**, `client` a DNP3 **master**.

## Contents

- [Image layout](#image-layout)
- [Commands](#commands)
- [`server`](#server)
- [`client`](#client)
- [Object documents](#object-documents)
- [`print-capabilities`](#print-capabilities)
- [`print-fixture`](#print-fixture)
- [Exit codes](#exit-codes)
- [Output rules](#output-rules)

## Image layout

| Item | Value |
|------|-------|
| Image | `ghcr.io/otfabric/dnp3-interop-<adapter>` |
| Entrypoint | `dnp3-interop` (`/usr/local/bin/dnp3-interop`) |
| Default command | `server` |
| Port | 20000/tcp |
| User | non-root, uid 10001 |
| Fixtures | `/fixtures/<name>/fixture.json` (baked in; mount over `/fixtures` to replace) |
| Readiness file | `/run/dnp3-interop/ready` |
| Health check | built in: `test -f /run/dnp3-interop/ready` |
| Licences | `/licenses/` |

Contract 1.0 covers DNP3 over TCP. UDP, TLS and serial links, file transfer
(group 70) and secure authentication (group 120) are not in it yet; the
capability document has a feature for each, all false. See
[PLAN.md](../PLAN.md).

## Commands

```text
dnp3-interop server [flags]
dnp3-interop client <operation> --host HOST [flags]
dnp3-interop print-capabilities
dnp3-interop print-fixture [NAME]
dnp3-interop version
```

Flags are `--name value`; switches are bare `--name`. An unknown flag is a
usage error (exit 2), never ignored.

### Link flags

Accepted by `server` and by every `client` operation.

| Flag | `server` default | `client` default | Meaning |
|------|------------------|------------------|---------|
| `--local-addr N` | the fixture's `outstation.address` | 1 | Own DNP3 link address, 0..65519 |
| `--remote-addr N` | the fixture's `outstation.masterAddress` | 1024 | Link address of the peer, 0..65519 |

The client defaults match the baseline fixture. A server only answers frames
addressed to its own address and coming from its master's address; a
request from any other master address is not answered.

## `server`

Runs an outstation that serves one fixture until it receives SIGTERM or
SIGINT.

| Flag | Default | Meaning |
|------|---------|---------|
| `--fixture PATH` | `/fixtures/baseline/fixture.json` | Fixture to serve |
| `--bind-address ADDR` | `0.0.0.0` | Listen address |
| `--port N` | 20000 | Listen port |
| `--ready-file PATH` | `/run/dnp3-interop/ready` | Readiness file |
| `--select-timeout-ms N` | 5000 | How long a select stays valid |
| `--confirm-timeout-ms N` | 5000 | How long the outstation waits for an application confirm |
| Link flags | | |

Lifecycle:

1. The fixture is loaded and validated. An invalid fixture, or one that uses
   a point type or variation the adapter does not serve, ends the process
   with exit 4 before anything listens.
2. The listener is opened. Failure ends the process with a non-zero exit.
3. The readiness file is written and the `ready` event is printed. From here
   on the outstation accepts DNP3 connections. **Readiness is the file (or
   the event), not the open port and not a delay.**
4. On SIGTERM/SIGINT the readiness file is removed, the connection is
   closed, the `stopped` event is printed and the process exits 0.

The outstation has **one master connection at a time**, as DNP3 over TCP
has: a new connection replaces the current one. The outstation's state
(point values, events not yet confirmed, selections, which classes report
unsolicited, the IIN bits) belongs to the process and survives connections:
restart the container to return to the fixture.

### Server events

One JSON object per line on stdout.

| `event` | Other fields | When |
|---------|--------------|------|
| `ready` | `adapter`, `address` (`ip:port`), `fixture`, `linkAddress`, `masterAddress` | Once, when the outstation accepts connections |
| `connection-opened` | | A master connected |
| `connection-closed` | | The connection ended |
| `command` | `function`, `type`, `index`, `code` or `value`, `status` | A select or operate reached the outstation's control handler |
| `time-sync` | `time` | The master wrote the outstation's time (either procedure) |
| `restart` | `type` (`cold` or `warm`) | A restart request was accepted |
| `assign-class` | `pointType`, `class`, `start`, `stop` | A class assignment was accepted. Only where the library tells the application (opendnp3) |
| `stopped` | | Once, after a clean stop |

`command`:

| Field | Meaning |
|-------|---------|
| `function` | `select`, `operate` (the operate of a select-before-operate), `direct-operate` or `direct-operate-no-ack` |
| `type` | `g12v1`, `g41v1`, `g41v2`, `g41v3` or `g41v4` |
| `index` | Point index of the control |
| `code` | `g12v1`: the control code octet as a number (`3` latch on, `4` latch off, `1` pulse on, `2` pulse off, `65` close, `129` trip), plus `count`, `onMs` and `offMs` |
| `value` | `g41`: the value |
| `status` | The command status the outstation answered, by name (see [command status](#command-status)) |

The libraries check that an operate matches its select before the control
handler sees it: an operate the library refuses itself (no matching select,
select timed out) produces no event.

The peer address of a connection is not reported: none of the libraries
tells it to the application.

Requests that need no application decision (reads, confirms, enabling
unsolicited responses, freezes, the clear of the restart bit) produce no
event.

## `client`

Runs one bounded operation as a master and prints one result document.

```text
dnp3-interop client <operation> --host HOST [--port N] [common flags] [operation flags]
```

Every operation: open the TCP connection, run the operation, close the
connection. The master does nothing on its own: no integrity poll at
startup, no enabling or disabling of unsolicited responses, no time
synchronization, no event poll on IIN bits. The one automatic action the
libraries take, and that cannot be turned off, is to clear the outstation's
restart bit (write g80v1 index 7) when a response has it set; a library that
does not do it leaves the bit set.

### Common flags

| Flag | Default | Meaning |
|------|---------|---------|
| `--host HOST` | required | Outstation to connect to |
| `--port N` | 20000 | |
| `--connect-timeout-ms N` | 5000 | Time to establish the TCP connection |
| `--timeout-ms N` | 5000 | Response timeout of each request of the operation |
| `--collect-ms N` | 0 | After a successful operation, keep receiving (unsolicited responses) for this long before closing |
| Link flags | | |

### Operations

| Operation | Flags | Sends | Succeeds when |
|-----------|-------|-------|---------------|
| `integrity-poll` | | READ g60v2, g60v3, g60v4, g60v1 (classes 1, 2, 3, 0) | The response arrived without a [request error](#request-errors) |
| `event-poll` | `--classes L` (`1,2,3`) | READ of the listed event classes (g60v2..v4) | As `integrity-poll` |
| `read` | `--group G --variation V` (required), `--start N --stop N` | READ of one header: start-stop range when `--start` and `--stop` are given, all objects (qualifier 0x06) otherwise. Variation 0 asks for the outstation's default | As `integrity-poll`. All objects of a group other than 60 only with the feature `readAllObjectsClient` |
| `operate` | see below | A control, see below | The outstation echoed the control with status SUCCESS |
| `time-sync` | `--procedure P` (`lan`) | `lan`: RECORD CURRENT TIME, then WRITE g50v3. `non-lan`: DELAY MEASURE, then WRITE g50v1 | Both requests were answered without a request error |
| `restart` | `--type T` (`cold`) | COLD RESTART or WARM RESTART | The outstation answered with the delay (g52v1 or g52v2). `restartDelayMs` only with the feature `restartDelayClient`, null otherwise |
| `enable-unsolicited` | `--classes L` (`1,2,3`) | ENABLE UNSOLICITED with g60v2..v4 | Null response without a request error |
| `disable-unsolicited` | `--classes L` (`1,2,3`) | DISABLE UNSOLICITED with g60v2..v4 | As `enable-unsolicited` |
| `assign-class` | `--class C` (required, 0..3), `--group G` (required), `--start N --stop N` | ASSIGN CLASS: g60v(C+1), then group G variation 0, range or all objects | As `enable-unsolicited`. Only with the feature `assignClassClient` |
| `freeze` | `--clear`, `--no-ack` | IMMEDIATE FREEZE (or FREEZE AND CLEAR with `--clear`) with g20v0, all objects; `--no-ack` sends the `_NR` function codes | Null response without a request error; with `--no-ack` when the request was sent. `--no-ack` only with the feature `freezeNoAckClient` |
| `monitor` | `--duration-ms N` (1000) | Nothing | The duration passed with the connection still up. What arrived unsolicited is in `objects` |

`--classes` is a comma-separated list of class numbers; `event-poll` takes
1..3. A list with a number outside that range is a usage error.

`time-sync`: a master library that synchronizes only when the outstation
asks for it, by the IIN bit NEED_TIME, first reads the IIN (a READ the
outstation answers without objects); if NEED_TIME is clear the operation
fails with `failed`. Such an adapter declares the feature
`timeSyncOnDemand` false. The baseline outstation sets NEED_TIME until its
time is written, so both kinds of master can run the operation against it.

`operate` flags:

| Flag | Default | Meaning |
|------|---------|---------|
| `--type T` | required | `g12v1` (control relay output block) or `g41v1`, `g41v2`, `g41v3`, `g41v4` (analog output: int32, int16, float32, float64) |
| `--index N` | required | Point index, 0..65535 |
| `--code C` | required for `g12v1` | `LATCH_ON`, `LATCH_OFF`, `PULSE_ON`, `PULSE_OFF`, `CLOSE` (pulse on with trip-close code close), `TRIP` (pulse on with trip-close code trip) |
| `--count N` | 1 | `g12v1`: count, 0..255 |
| `--on-ms N` | 100 | `g12v1`: on time |
| `--off-ms N` | 100 | `g12v1`: off time |
| `--value V` | required for `g41` | The value, in the range of the variation |
| `--mode M` | `direct` | `direct` (DIRECT OPERATE), `sbo` (SELECT, then OPERATE after a successful select), `direct-no-ack` (DIRECT OPERATE NO ACK) |

`--mode direct-no-ack` only with the feature `directOperateNoAckClient`; it
succeeds when the request was sent, and `commandStatus` is null. A
`select` that fails ends the operation with the status of the select; the
operate is not sent.

### Result document

Exactly one JSON object on one line of stdout, for every invocation that got
past argument parsing, including failures.

| Field | Type | Meaning |
|-------|------|---------|
| `schemaVersion` | string | `"1.0"` |
| `adapter` | string | Adapter that ran the operation |
| `operation` | string | As given on the command line |
| `ok` | boolean | The operation succeeded as defined above |
| `error` | object or null | `{ "code", "message" }` when `ok` is false |
| `connected` | boolean | The TCP connection was established |
| `iin` | array or null | Read operations (`integrity-poll`, `event-poll`, `read`): the internal indications of the last solicited response, by name. Null for the other operations and when no response arrived |
| `objects` | array | Every measurement received, solicited and unsolicited, in order: see [Object documents](#object-documents) |
| `commandStatus` | string or null | `operate` only: the status the outstation echoed, by name; null when no echo arrived, none was expected, or the response carried a [request error](#request-errors) |
| `restartDelayMs` | number or null | `restart` only: the delay the outstation announced, in milliseconds |
| `elapsedMs` | number | Wall-clock duration |

`iin` names, in this order: `BROADCAST`, `CLASS_1_EVENTS`,
`CLASS_2_EVENTS`, `CLASS_3_EVENTS`, `NEED_TIME`, `LOCAL_CONTROL`,
`DEVICE_TROUBLE`, `DEVICE_RESTART` (IIN1.0..1.7), `NO_FUNC_CODE_SUPPORT`,
`OBJECT_UNKNOWN`, `PARAMETER_ERROR`, `EVENT_BUFFER_OVERFLOW`,
`ALREADY_EXECUTING`, `CONFIG_CORRUPT`, `RESERVED_2`, `RESERVED_1`
(IIN2.0..2.7). Only read operations report it because not every library
hands the IIN of other responses to the application.

The restart bit is set by an outstation until a master clears it, so whether
`DEVICE_RESTART` shows depends on what ran against the outstation before.

#### Request errors

A response whose IIN2 has `NO_FUNC_CODE_SUPPORT`, `OBJECT_UNKNOWN` or
`PARAMETER_ERROR` set is a refusal of the request: the error is `rejected`.
This holds for `operate` too: when the response to a control carries a
request error, the error is `rejected` and `commandStatus` is null, whatever
status the outstation echoed. Both P0 outstations set PARAMETER_ERROR along
with a NOT_SUPPORTED echo, and not every master library hands the echo of
such a response to the application. `command-failed` is a refusal by the
echoed status alone (OUT_OF_RANGE, NO_SELECT, ...).

#### Command status

`commandStatus` and the `status` of the `command` event use these names
(IEEE 1815 table 11-6): `SUCCESS` (0), `TIMEOUT`, `NO_SELECT`,
`FORMAT_ERROR`, `NOT_SUPPORTED`, `ALREADY_ACTIVE`, `HARDWARE_ERROR`, `LOCAL`,
`TOO_MANY_OPS`, `NOT_AUTHORIZED`, `AUTOMATION_INHIBIT`,
`PROCESSING_LIMITED`, `OUT_OF_RANGE`, `DOWNSTREAM_LOCAL`,
`ALREADY_COMPLETE`, `BLOCKED`, `CANCELLED`, `BLOCKED_OTHER_MASTER`,
`DOWNSTREAM_FAIL` (18), `NON_PARTICIPATING` (126), and `UNDEFINED` for any
other value.

#### Error codes

| Code | Meaning | Exit |
|------|---------|------|
| `connect-failed` | No TCP connection within `--connect-timeout-ms` | 3 |
| `timeout` | The outstation did not answer in time | 1 |
| `rejected` | A [request error](#request-errors) in the response | 1 |
| `command-failed` | `operate`: the echoed status is not SUCCESS and there is no request error (`commandStatus` says which) | 1 |
| `connection-lost` | The connection closed during the operation | 1 |
| `failed` | Anything else; see `message` | 1 |

`message` is for people and not part of the contract.

## Object documents

```json
{ "group": 32, "variation": 7, "index": 0, "value": 49.5, "flags": ["ONLINE"],
  "time": "2026-10-19T15:35:12.345Z", "unsolicited": false }
```

| Field | Meaning |
|-------|---------|
| `group`, `variation` | Of the header the object arrived in. Octet strings have the length as variation |
| `index` | Point index |
| `value` | See below |
| `flags` | The flag names set in the object's flags octet, see below. For a variation without flags, what the library reports. Absent for octet strings, which have no flags |
| `time` | Variations with an absolute time stamp (and relative ones, resolved by the library), UTC `YYYY-MM-DDTHH:MM:SS.mmmZ`. Absent otherwise |
| `unsolicited` | The object came in an unsolicited response |

| Groups | `value` |
|--------|---------|
| 1, 2, 10, 11 | boolean |
| 3, 4 | double-bit state 0..3 (0 intermediate, 1 determined off, 2 determined on, 3 indeterminate) |
| 20, 21, 22, 23 | integer |
| 30, 32, 40, 42 | number |
| 110, 111 | the octets, lower-case hex |

`flags` names by bit, in bit order. The state bits of the binary types are
in `value`, not in `flags`.

| Bit | Groups 1, 2, 3, 4, 10, 11 | Groups 20..23 | Groups 30, 32, 40, 42 |
|----:|---------------------------|---------------|-----------------------|
| 0 | `ONLINE` | `ONLINE` | `ONLINE` |
| 1 | `RESTART` | `RESTART` | `RESTART` |
| 2 | `COMM_LOST` | `COMM_LOST` | `COMM_LOST` |
| 3 | `REMOTE_FORCED` | `REMOTE_FORCED` | `REMOTE_FORCED` |
| 4 | `LOCAL_FORCED` | `LOCAL_FORCED` | `LOCAL_FORCED` |
| 5 | `CHATTER_FILTER` (1..4 only) | `ROLLOVER` | `OVER_RANGE` |
| 6 | | `DISCONTINUITY` | `REFERENCE_ERR` |

An adapter reports what its library hands it, and in the order it arrives.
The order of headers in a response is the outstation's choice: compare
objects by group, variation and index, not by position.

Numbers are JSON numbers; a single-precision value is printed with the digits
of its double-precision conversion, so compare numerically.

## `print-capabilities`

Prints one JSON object on one line and exits 0. See
[CAPABILITIES.md](CAPABILITIES.md) for the fields and the values of each
adapter. Needs no network and no fixture.

## `print-fixture`

`print-fixture [NAME]` (default `baseline`) writes the fixture baked into the
image to stdout, byte for byte, and exits 0. An unknown name exits 1.

A consumer reads the expected values from the image it tests against, so it
needs no checkout of this repository and cannot drift from the image.

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Success. `server`: clean stop after SIGTERM/SIGINT |
| 1 | The operation failed (see `error.code`); `server`: could not listen |
| 2 | Usage error: unknown command, operation or flag, missing or invalid value. Nothing was sent |
| 3 | `client`: the connection could not be established |
| 4 | `server`: the fixture is missing, invalid, or needs what the adapter does not serve |

## Output rules

- **stdout carries JSON only**: server events as JSON Lines, one client
  result, one capability document. `print-fixture` and `version` are the
  exceptions and print their own format.
- **stderr carries diagnostics** for people. Nothing on stderr is part of the
  contract. Library logging goes there too.
- Field names and exit codes are stable within a major contract version.
  Fields may be added in a minor version; consumers ignore fields they do not
  know.
- A number with no fractional part is printed without one (`42`, not
  `42.0`), so that every adapter prints the same text for the same value.
- Every wait is bounded: by `--timeout-ms`, `--connect-timeout-ms` or the
  duration flags. No client operation runs indefinitely.
