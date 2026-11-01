# Container contract

Version: **1.4** (`contract_version` in [versions.yaml](../versions.yaml))

Every adapter image exposes the same commands, flags, JSON documents and exit
codes. Consumers depend on this document, not on any adapter's source. A
change that breaks it bumps the major version; an addition bumps the minor
one. Documents carry `schemaVersion` `"1.0"`: it names the compatible line
and changes only with the major version.

| Version | Change |
|---------|--------|
| 1.0 | First release |
| 1.1 | [Transports](#transports) (UDP, TLS, serial), [file transfer](#file-transfer) (group 70), [secure authentication](#secure-authentication) (SAv5, group 120): flags, operations, events, result fields, error codes and features, all additions |
| 1.2 | [More point types](#more-point-types): frozen analog inputs (groups 31, 33), command events (groups 13, 43), time and interval (group 50 variation 4) in object documents, the `extended` fixture and their features; [dual end point](#dual-end-point): `server --connect`, `client --listen`; [security statistics](#security-statistics) (groups 121, 122); `serverFeatures` in the capability document; additions |
| 1.3 | [Device attributes](#device-attributes) (group 0): the `attributes` fixture, `attribute-read`, `attribute-write`, the `attribute-write` event; [several controls in one request](#several-controls-in-one-request): `operate-multi`, `commandStatuses`, the fixture's `maxControlsPerRequest`; [freeze at time and analog freeze](#freeze-at-time-and-analog-freeze): `freeze --at`, `--interval-ms`, `--group`; [analog deadbands](#analog-deadbands) (group 34): `deadband-write`, group 34 in object documents; [file append and authentication](#file-append-and-authentication): `file-put --append`, `file-authenticate`, `--file-user`, `--file-password`; [event buffer overflow](#event-buffer-overflow): the fixture's `eventBufferSize`, the `events` fixture; [link layer](#link-layer): `--link-confirms`, `--keep-alive-ms`, the `link-status` event; [broadcast](#broadcast): `operate --broadcast`, the `broadcast` event; [management functions](#management-functions): `manage`, the `manage` event; [several outstations](#several-outstations): `server --outstations`; additions |
| 1.4 | [Counters that change](#counters-that-change): the `counter-increment` command, `server --control-socket`, `server --frozen-event-policy`, the `counters` fixture, the capability fields `frozenEventPolicies`, `frozenEventRetention` and `unsupportedReasons`, the features `counterIncrementServer`, `frozenCounterEventsServer`, `frozenCounterEventTimeClient`; additions |

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
- [Transports](#transports): [TLS](#tls), [UDP](#udp), [dual end point](#dual-end-point) (1.2), [serial](#serial)
- [File transfer](#file-transfer): [file append and authentication](#file-append-and-authentication) (1.3)
- [Secure authentication](#secure-authentication): [security statistics](#security-statistics) (1.2)
- [Several controls in one request](#several-controls-in-one-request) (1.3)
- [Freeze at time and analog freeze](#freeze-at-time-and-analog-freeze) (1.3)
- [Link layer](#link-layer) (1.3)
- [Several outstations](#several-outstations) (1.3)
- [Management functions](#management-functions) (1.3)
- [Broadcast](#broadcast) (1.3)
- [Event buffer overflow](#event-buffer-overflow) (1.3)
- [Analog deadbands](#analog-deadbands) (1.3)
- [Device attributes](#device-attributes) (1.3)
- [Counters that change](#counters-that-change) (1.4)
- [More point types](#more-point-types) (1.2)
- [Object documents](#object-documents)
- [`print-capabilities`](#print-capabilities)
- [`print-fixture`](#print-fixture)
- [Exit codes](#exit-codes)
- [Output rules](#output-rules)

## Image layout

| Item | Value |
|------|-------|
| Image | `ghcr.io/otfabric/dnp3-interop-<adapter>`; the stepfunc image is not published and exists only where it was built (`make image-stepfunc`) |
| Entrypoint | `dnp3-interop` (`/usr/local/bin/dnp3-interop`) |
| Default command | `server` |
| Port | 20000/tcp (20000/udp with `--transport udp`) |
| User | non-root, uid 10001 |
| Fixtures | `/fixtures/<name>/fixture.json` (baked in; mount over `/fixtures` to replace) |
| Readiness file | `/run/dnp3-interop/ready` |
| Health check | built in: `test -f /run/dnp3-interop/ready` |
| Licences | `/licenses/` |
| Tools | `/bin/sh`; `socat` in images with the feature `serial`, for bridging a serial device (see [Serial](#serial)) |

Contract 1.0 covers DNP3 over TCP. Contract 1.1 adds UDP, TLS and serial
links, file transfer (group 70) and secure authentication (group 120); 1.2
to 1.4 add the features in the table above. An image says which of them it
has in its capability document; what it lacks is a usage error (exit 2).

## Commands

```text
dnp3-interop server [flags]
dnp3-interop client <operation> --host HOST [flags]
dnp3-interop counter-increment --index N [flags]
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
| `--control-socket PATH` | `/run/dnp3-interop/control.sock` | 1.4: see [counters that change](#counters-that-change) |
| `--frozen-event-policy P` | the first of `frozenEventPolicies` | 1.4: see [counters that change](#counters-that-change) |
| Link flags | | |

Lifecycle:

1. The fixture is loaded and validated. An invalid fixture, or one that uses
   a point type or variation the adapter does not serve, ends the process
   with exit 4 before the readiness file is written.
2. The listener is opened. Failure ends the process with a non-zero exit.
3. The readiness file is written and the `ready` event is printed. From here
   on the outstation accepts DNP3 connections. **Readiness is the file (or
   the event), not the open port and not a delay.**
4. On SIGTERM/SIGINT the readiness file is removed, the connection is
   closed, the `stopped` event is printed and the process exits 0. A server
   that ends any other way (its library's session ends with an error, for
   one) exits 1 without `stopped`.

The outstation has **one master connection at a time**, as DNP3 over TCP
has: a new connection replaces the current one. The outstation's state
(point values, events not yet confirmed, selections, which classes report
unsolicited, the IIN bits) belongs to the process and survives connections:
restart the container to return to the fixture.

### Server events

One JSON object per line on stdout.

| `event` | Other fields | When |
|---------|--------------|------|
| `ready` | `adapter`, `address` (`ip:port`; the device with `--transport serial`; the master's with `--connect`), `fixture`, `linkAddress`, `masterAddress`, `transport` (`tcp`, `tls`, `udp` or `serial`; 1.1), `dial` (true with `--connect`; 1.2) | Once, when the outstation accepts connections (or, with `--connect`, dials) |
| `connection-opened` | | A master connected (TCP and TLS only) |
| `connection-closed` | | The connection ended (TCP and TLS only) |
| `command` | `function`, `type`, `index`, `code` or `value`, `status` | A select or operate reached the outstation's control handler |
| `time-sync` | `time` | The master wrote the outstation's time (either procedure) |
| `restart` | `type` (`cold` or `warm`) | A restart request was accepted |
| `assign-class` | `pointType`, `class`, `start`, `stop` | A class assignment was accepted. Only where the library tells the application (opendnp3, stepfunc since 1.8.0) |
| `file` | `action`, `name`, `status` (1.1) | See [file transfer](#file-transfer) |
| `auth` | `user`, `function` (1.1) | See [secure authentication](#secure-authentication) |
| `auth-error` | `user`, `code` (1.1) | See [secure authentication](#secure-authentication) |
| `attribute-write` | `set`, `variation`, `type`, `value` (1.3) | See [device attributes](#device-attributes) |
| `link-status` | (1.3) | See [link layer](#link-layer) |
| `broadcast` | `function` (1.3) | See [broadcast](#broadcast) |
| `manage` | `function` (1.3) | See [management functions](#management-functions) |
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

Every operation: open the connection (TCP by default; see
[transports](#transports)), run the operation, close the connection. The master does nothing on its own: no integrity poll at
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
| `file-get`, `file-put`, `file-ls`, `file-info`, `file-delete` | see [file transfer](#file-transfer) | 1.1 | |

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
past argument parsing, including failures: a listening master that cannot
bind, TLS files that cannot be loaded or a runtime that cannot be created
fail with `failed` and a complete document, the operation's own fields
included.

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
| `commandStatus` | string or null | `operate` and `operate-multi` only: the status the outstation echoed, by name; null when no echo arrived, none was expected, or the response carried a [request error](#request-errors) |
| `commandStatuses` | array or null | `operate-multi` only (1.3): see [several controls in one request](#several-controls-in-one-request) |
| `restartDelayMs` | number or null | `restart` only: the delay the outstation announced, in milliseconds |
| `file`, `files`, `fileStatus` | | File operations only (1.1): see [file transfer](#file-transfer) |
| `attributes` | array | `attribute-read` only (1.3): see [device attributes](#device-attributes) |
| `elapsedMs` | number | Wall-clock duration |

The fields of one operation are absent from the documents of the others.

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
| `connect-failed` | No connection within `--connect-timeout-ms`: no TCP connection, a failed TLS handshake, a serial device that cannot be opened | 3 |
| `timeout` | The outstation did not answer in time | 1 |
| `rejected` | A [request error](#request-errors) in the response | 1 |
| `command-failed` | `operate`: the echoed status is not SUCCESS and there is no request error (`commandStatus` says which) | 1 |
| `connection-lost` | The connection closed during the operation | 1 |
| `file-error` | A file operation: the outstation answered with a file status other than SUCCESS (`fileStatus` says which). 1.1 | 1 |
| `auth-failed` | Secure authentication failed: the outstation refused the master's authentication, or the session key change failed. 1.1 | 1 |
| `failed` | Anything else; see `message` | 1 |

`message` is for people and not part of the contract.

## Transports

*Contract 1.1.* Accepted by `server` and by every `client` operation. An
image that lacks the feature a flag needs (`udp`, `tls`, `serial`) treats
the flag as a usage error (exit 2).

| Flag | Default | Meaning |
|------|---------|---------|
| `--transport T` | `tcp` | `tcp`, `udp` or `serial`. TLS is `tcp` with the TLS flags |

An image with TCP only (dnp3py) accepts `--transport tcp`; `udp` and
`serial` are usage errors there, as the rule above says.

### TLS

Feature `tls`. DNP3 over TLS (IEEE 1815-2012 annex, IEC 62351-3) on a TCP
port; TLS 1.2 or 1.3. Every library the images use requires **mutual**
authentication: the outstation demands a client certificate, the master
verifies the outstation's. None can skip verification, so the contract has
no "insecure" switch.

| `server` flag | Meaning |
|---------------|---------|
| `--tls-cert PATH` | The outstation's certificate (PEM). Turns TLS on |
| `--tls-key PATH` | Its private key (PEM, PKCS #8, unencrypted). Required with `--tls-cert` |
| `--tls-ca PATH` | The CA (PEM) a master's certificate must be signed by. Required with `--tls-cert` |

| `client` flag | Meaning |
|---------------|---------|
| `--tls` | Connect with TLS |
| `--tls-ca PATH` | The CA (PEM) the outstation's certificate must be signed by. Required with `--tls` |
| `--tls-cert PATH`, `--tls-key PATH` | The master's certificate and key (PEM; PKCS #8 key). Required with `--tls` |
| `--tls-server-name NAME` | The name the outstation's certificate must carry (subject alternative name). Default: `--host`. The opendnp3 master does not check the name |

A handshake that fails (an untrusted certificate, no client certificate, a
name that does not match) is `connect-failed` for the master when it fails
before the connection is established. With TLS 1.3 a master learns that
the outstation refused *its* certificate only after its side of the
handshake is done: its operation fails, with an error code that depends on
the library (`connection-lost`, `timeout` or `failed`). Certificate files
that cannot be read or parsed: the client fails with `failed` and its result
document, whether it dials or listens; the server exits 1 before `ready`,
whether it listens or dials.

**Providing certificates.** The images contain no certificates. Mount a
directory with PEM files and pass the paths, for example
`-v "$PWD/pki:/pki:ro" ... server --tls-cert /pki/server.pem --tls-key
/pki/server.key --tls-ca /pki/ca.pem`. The files must be readable by uid
10001. Certificates need the extensions TLS libraries insist on: the CA
`basicConstraints=critical,CA:TRUE` and `keyUsage=keyCertSign`; the outstation's
`extendedKeyUsage=serverAuth` and a `subjectAltName` with the name masters
use; the master's `extendedKeyUsage=clientAuth`. ECDSA P-256 and RSA 2048 keys
work with every image. `scripts/test-pki.sh` makes such a set for tests.

### UDP

Feature `udp`. DNP3 over UDP (IEEE 1815 annex): one link-layer stream over
datagrams, without connections.

| Flag | Role | Default | Meaning |
|------|------|---------|---------|
| `--port N` | both | 20000 | `server`: the UDP port to listen on. `client`: the outstation's UDP port |
| `--udp-peer HOST:PORT` | `server` | none | The master's endpoint: where responses and unsolicited responses go. Required by the opendnp3 outstation, whose socket is connected to one peer (exit 2 without it); without it, other outstations answer the endpoint a request came from |
| `--udp-local-port N` | `client` | 20000 | The master's own UDP port; 0 picks a free one |

There is no connection: a client is `connected` once its socket is open, and
an outstation that does not answer is `timeout`. A socket that cannot be
opened, or a host that cannot be resolved, is `connect-failed`;
`--connect-timeout-ms` bounds resolving the host and opening the socket. The DSC
go-dnp3 outstation (dscsystems) keeps answering the first endpoint it heard from for its whole life, so give
masters a fixed endpoint (`--udp-local-port`, a fixed IP) or restart the
outstation per master.

### Dual end point

*Contract 1.2.* The connection the other way round: the outstation dials,
the master listens. The DNP3 roles do not change.

| Flag | Role | Meaning |
|------|------|---------|
| `--connect HOST:PORT` | `server` | The outstation dials the master at HOST:PORT instead of listening, and dials again after a connection ends. `--bind-address` and `--port` are usage errors with it; `--transport tcp` only. Feature `dualEndpointServer` |
| `--tls-server-name NAME` | `server`, with `--connect` | The name the master's certificate must carry (default HOST). opendnp3 does not check it |
| `--listen` | `client` | The master listens on `--bind-address` (default `0.0.0.0`) and `--port` (20000) and waits up to `--connect-timeout-ms` for an outstation; no outstation is `connect-failed`. `--host` is a usage error with it; `--transport tcp` only. Feature `dualEndpointClient` |
| `--bind-address ADDR` | `client`, with `--listen` | Where the master listens |

With TLS the TLS flags keep their meaning for the end that holds them, and
the certificates swap sides: the dialling outstation is the TLS client
(`server --connect ... --tls-cert --tls-key --tls-ca`), the listening master
the TLS server (`client --listen --tls --tls-cert --tls-key --tls-ca`). A
listening master over TLS needs the feature `dualEndpointClientTls`.

A dialling outstation writes its readiness file once it is set up and
dialling, before any master is there; its ready event has `dial: true` and
`address` the master's. `connection-opened` and `connection-closed` mark
each connection; the first `connection-opened` may come before `ready`.

### Serial

Feature `serial`. DNP3 over a serial line.

| Flag | Default | Meaning |
|------|---------|---------|
| `--serial-device PATH` | required | The device: a UART or a pseudo-terminal |
| `--baud N` | 9600 | |
| `--data-bits N` | 8 | 7 or 8 |
| `--parity P` | `none` | `none`, `even` or `odd` |
| `--stop-bits N` | 1 | 1 or 2 |

No flow control. A `client` needs no `--host` with `--transport serial`;
`--host`, `--port`, `--bind-address` and the TLS flags are usage errors with
it. A device that cannot be opened: the server exits 1, the client fails with
`connect-failed` (exit 3) after `--connect-timeout-ms`. The client is
`connected` once the device is open.

**Bridging.** The images carry `socat`, so a pseudo-terminal in a container
can stand for a serial port and be bridged to TCP. An outstation:

```sh
docker run --entrypoint /bin/sh IMAGE -c '
  socat pty,link=/tmp/ttyDNP3,raw,echo=0 pty,link=/tmp/ttyLINE,raw,echo=0 &
  while [ ! -e /tmp/ttyDNP3 ] || [ ! -e /tmp/ttyLINE ]; do sleep 0.05; done
  socat -t 0.05 tcp-listen:20001,reuseaddr,fork file:/tmp/ttyLINE,raw,echo=0 &
  exec dnp3-interop server --transport serial --serial-device /tmp/ttyDNP3'
```

The outstation holds one end of a pseudo-terminal pair for its whole life;
each TCP connection is attached to the other end, so masters can come and
go. A master in another container (or a library on the host) reaches that
line with `socat pty,link=/tmp/ttyDNP3,raw,echo=0 tcp:OUTSTATION:20001` and
opens its own end of the pseudo-terminal. What the outstation sends while no
master is attached waits in the line, as on a real one. The bytes on the line are exactly what a
serial port would carry; timing (inter-character gaps, baud rate) is not
emulated.

## File transfer

*Contract 1.1.* DNP3 file transfer (group 70, IEEE 1815 clause 4.6 and
annex A.37) with the function codes OPEN_FILE, CLOSE_FILE, DELETE_FILE,
GET_FILE_INFO, READ and WRITE. Features:

| Feature | Meaning |
|---------|---------|
| `fileServer` | The outstation serves the `files` of its fixture |
| `fileClient` | The master has `file-get`, `file-put`, `file-ls` and `file-info` |
| `fileDeleteClient` | The master has `file-delete` |
| `fileTransfer` | `fileServer` or `fileClient` (the 1.0 feature) |

**Outstation.** It serves the files of its fixture's `files` section (the
`files` fixture has some; see [FIXTURES.md](FIXTURES.md)): a master can read
them, list their directories, ask for their descriptors, replace or create a
file in an existing directory, and delete files. Like the points, the files
belong to the process: restart the container to return to the fixture. File
authentication (AUTHENTICATE_FILE) is not used unless the server is given
`--file-user` (contract 1.3, [below](#file-append-and-authentication)). An outstation without
`fileServer` refuses a fixture with `files` (exit 4); one with it but given a
fixture without `files` refuses the file function codes (IIN2
NO_FUNC_CODE_SUPPORT).

Server event `file`, for every file request the outstation answered:

| Field | Meaning |
|-------|---------|
| `action` | `read` (opened for reading), `write` (written and closed), `delete`, `info` (GET_FILE_INFO), `list` (a directory opened for reading); 1.3: `append` (appended and closed), `authenticate` (AUTHENTICATE_FILE) |
| `name` | The file name the master sent; for `authenticate`, the user name (never the password) |
| `status` | The file status the outstation answered, by name (below) |

**Master operations.** Every one takes `--name NAME`, an absolute file name
(`/data/readme.txt`; `/` is the root directory). Other names follow the rules
of the fixture's file names (at most 200 characters; path elements of
`A-Za-z0-9._-` not starting with `.`); any other name is a usage error
(exit 2).

| Operation | Flags | Sends | Succeeds when | Result fields |
|-----------|-------|-------|---------------|---------------|
| `file-get` | `--name` | OPEN_FILE (read), READ of the blocks, CLOSE_FILE | The whole file arrived | `file`: `name`, `size`, `content` |
| `file-put` | `--name`, `--content HEX` | OPEN_FILE (write, with the size), WRITE of the blocks, CLOSE_FILE | Every block was accepted | |
| `file-ls` | `--name` (a directory) | As `file-get`, of the directory | The listing arrived | `files`: the entries |
| `file-info` | `--name` | GET_FILE_INFO | The descriptor arrived | `file`: the entry |
| `file-delete` | `--name` | DELETE_FILE | The outstation answered SUCCESS | |

An entry (`file-ls`, `file-info`) is
`{ "name", "type", "size", "permissions", "time" }`: `type` `file` or
`directory`, `size` in octets (what the outstation reports for a
directory), `permissions` the nine permission bits as `rwxr-x---`, `time` the
creation time the outstation reports. `name` is the entry's name as the
outstation sends it (the last path element). `content` is lower-case hex.
`file-get` of a directory fails with whatever status the outstation answers.

`fileStatus` (string or null): the file status of the outstation's last
answer, by name, or null when there was none. A status other than SUCCESS is
the error `file-error`. Names (IEEE 1815 table 4-8): `SUCCESS` (0),
`PERMISSION_DENIED`, `INVALID_MODE`, `FILE_NOT_FOUND`, `FILE_LOCKED`,
`TOO_MANY_OPEN`, `INVALID_HANDLE`, `WRITE_BLOCK_SIZE`, `COMM_LOST`,
`CANNOT_ABORT` (9), `NOT_OPENED` (16), `HANDLE_EXPIRED`, `BUFFER_OVERRUN`,
`FATAL`, `BLOCK_SEQ` (20), `UNDEFINED` for any other value.

### File append and authentication

*Contract 1.3.*

| Where | Flag or operation | Meaning |
|-------|-------------------|---------|
| client | `file-put --append` | OPEN_FILE in append mode: the content goes after what the file holds. Feature `fileAppendClient` (master), `fileAppendServer` (outstation) |
| client | `--file-user U --file-password P` on `file-get`, `file-put`, `file-ls`, `file-delete` | AUTHENTICATE_FILE (g70v2) with the credentials first, and the key the outstation answers in the open or delete. Both or neither (exit 2). Feature `fileAuthClient`. `file-info` takes neither (exit 2): GET_FILE_INFO carries no key |
| client | `file-authenticate --file-user U --file-password P` | AUTHENTICATE_FILE alone. Succeeds when the outstation answers a key other than 0; key 0 is the error `file-error` with `fileStatus` `PERMISSION_DENIED`. The key is not reported |
| server | `--file-user U --file-password P` | The outstation requires AUTHENTICATE_FILE with these credentials before an open or delete, and answers PERMISSION_DENIED to one without a valid key. Needs a fixture with files (exit 2 otherwise). Feature `fileAuthServer` |

A master that sends AUTHENTICATE_FILE to an outstation that does not require
it gets whatever that outstation answers, usually NO_FUNC_CODE_SUPPORT (the
error `rejected`). AUTHENTICATE_FILE carries the password in clear.

## Secure authentication

*Contract 1.1.* Feature `secureAuthentication`: DNP3 Secure Authentication
version 5 (IEEE 1815-2012 clause 7), challenge-reply mode, in every role the
image has. The master and the outstation share a user number and that user's
**update key**; the master establishes session keys with it (Session Key
Status Request g120v4, Session Key Status g120v5, Session Key Change g120v6)
before its first request and whenever they expire. The outstation then
challenges (g120v1) every critical request, and the master answers with a
MAC (g120v2) over the challenge and the request. Aggressive mode, remote key
management (g120v8, v10-v15) and authentication of unsolicited responses are
not in the contract.

| Flag | Default | Meaning |
|------|---------|---------|
| `--sa-update-key HEX` | none | The update key, 16 octets (AES-128 key wrap) or 32 (AES-256). Turns secure authentication on |
| `--sa-user N` | 1 | The user number, 1..65535 |
| `--sa-mac NAME` | `hmac-sha256-16` | The MAC algorithm: `hmac-sha1-4`, `hmac-sha1-8`, `hmac-sha1-10`, `hmac-sha256-8`, `hmac-sha256-16`, `aes-gmac`. One the image does not support (its capability document lists them) is a usage error |

Accepted by `server` and every `client` operation. An outstation with secure
authentication answers a critical request from a master without it with a
challenge the master cannot answer: the master's operation fails (`timeout`
or `auth-failed`).

Which requests are critical is the outstation's choice within the standard;
the capability document lists the function codes an image's outstation
challenges (`secureAuthentication.criticalFunctions`).

Server events:

| `event` | Fields | When |
|---------|--------|------|
| `auth` | `user`, `function` (the function code of the request, a number) | A critical request was authenticated and is executed |
| `auth-error` | `user`, `code` (the error code of the g120v7 object, a number: 1 authentication failed, 4 aggressive mode not supported, 5 MAC algorithm not supported, 6 key wrap algorithm not supported, 7 authorization failed, 8 update key change method not permitted, 9 invalid signature, 10 invalid certification, 11 unknown user, 12 maximum session key status requests exceeded) | The outstation sent an authentication error |

The master's error is `auth-failed` when the outstation refused its
authentication or a session key change failed.

### Security statistics

*Contract 1.2.* An outstation with secure authentication on serves its
security statistics (IEEE 1815-2012 table 7-8): g121v1 by READ of a range
(not in class 0) and g122 events in a class, one each time a statistic has
grown by a threshold.

| `server` flag | Default | Meaning |
|---------------|---------|---------|
| `--sa-statistics-class N` | 1 | The class (1..3) of the g122 events |
| `--sa-statistics-threshold N` | 10 | How many increments of a statistic raise an event |

Both need `--sa-update-key`. Features: `securityStatisticsServer` (the
outstation serves them), `securityStatisticsClient` (the master reports
them). Object documents for groups 121 and 122: `value` the count, `flags`
(the counter flags), `association`, `time` for g122v2. An outstation without
secure authentication refuses g121 (`rejected`).

DSC go-dnp3's master drops groups 121 and 122, so the dscsystems adapter reads
them from the frames itself, from responses that carry nothing but them (a
class poll that returns other events before them leaves them out).

## Several controls in one request

*Contract 1.3.* `operate-multi --controls LIST [--mode direct|sbo]` sends one
DIRECT OPERATE (or SELECT, then OPERATE after a select whose every control
succeeded) carrying every control of LIST, one object header each, in order.
LIST is comma-separated: `g12v1:INDEX:CODE` (codes as for `operate`; count 1,
on and off time 100 ms) and `g41v1..v4:INDEX:VALUE`. Feature
`multipleControlsClient`.

| Result field | Meaning |
|--------------|---------|
| `commandStatus` | The first status other than SUCCESS, or SUCCESS when all succeeded; null when no echo arrived or the response carried a request error |
| `commandStatuses` | The status of each control in order; null when no echo arrived, when only some controls were echoed, when the response carried a request error, or where the master library reports only the first refusal (stepfunc) |

When the outstation echoed only some of the controls, `commandStatuses` is
null (it has one entry per control or none) and the operation fails:
`command-failed` when an echoed control was refused (`commandStatus` the
first refusal), `failed` otherwise. DSC go-dnp3's master checks only the
controls a SELECT echoed: after a select echo that carried some of the
controls, all SUCCESS, it sends the OPERATE, and the result is that of the
operate's echo (a library difference the adapter cannot change; the other
masters send no OPERATE).

It succeeds when every control is echoed SUCCESS; a refused one is
`command-failed`, as for `operate`. The outstation carries out the controls
that succeed and not the others: a request can partly succeed.

An outstation whose fixture sets `maxControlsPerRequest` carries out that
many controls of a request and answers TOO_MANY_OPS for the rest (feature
`maxControlsServer`).

## Freeze at time and analog freeze

*Contract 1.3.* More flags of `freeze`:

| Flag | Meaning |
|------|---------|
| `--group G` | 20 (default): the counters. 30: the analog inputs, which a freeze copies into the frozen analog inputs of the same index. Feature `analogFreezeClient` (master), `analogFreezeServer` (outstation) |
| `--at T` | FREEZE AT TIME (FREEZE AT TIME NO ACK with `--no-ack`): a g50v2 with the time and interval, then the objects of `--group`. T is `immediately` (time 0), `+MS` (from now) or milliseconds since the epoch. Not with `--clear`. Feature `freezeAtTimeClient` (master), `freezeAtTimeServer` (outstation) |
| `--interval-ms N` | With `--at`: repeat every N ms (default 0: once). Without `--at` a usage error (exit 2) |

An outstation freezes at the time given, once or every interval; time 0 with
interval 0 freezes at once (IEEE 1815-2012 A.23.2). A time already past with
no interval is refused (PARAMETER_ERROR: the master's error is `rejected`);
with an interval the first freeze is the next time of the schedule after
now, without catching up on the ones missed.
It succeeds when the outstation accepted the request, not when the freeze
happens.

## Link layer

*Contract 1.3.* Link-layer confirmation and keep-alives (IEEE 1815 clause 9).

| Where | Flag | Meaning |
|-------|------|---------|
| client, server | `--link-confirms` | Send user data as CONFIRMED_USER_DATA (after a RESET_LINK_STATES) and retransmit it until the peer acknowledges it. Feature `linkConfirmsClient` (master), `linkConfirmsServer` (outstation). A peer acknowledges them (dnp3py's master does not: a known bug) |
| client | `--keep-alive-ms N` | Send REQUEST_LINK_STATUS when the link has been idle for N ms (0, the default: never). Meaningful with `monitor`. Feature `keepAliveClient` |

Server event `link-status`, no fields: the outstation received a
REQUEST_LINK_STATUS. Feature `linkStatusEventServer`.

An exchange that the peer does not acknowledge fails as the operation would
without an answer (`timeout` or `connection-lost`).

## Several outstations

*Contract 1.3.* `server --outstations N` (1..16, default 1) serves N
outstations on the one channel, at the link addresses `--local-addr` (or the
fixture's) to that plus N-1, each with its own copy of the fixture. A master
picks one with `--remote-addr`; a request to another address goes unanswered.
Addresses past 65519, or that take in the master's, are a usage error (exit
2). The `ready` event gives the first address. Feature
`multipleOutstationsServer`.

## Management functions

*Contract 1.3.* `manage --function F` sends one of the management functions
of IEEE 1815 clause 4.4.8: `initialize-data` (15), `initialize-application`
(16), `start-application` (17), `stop-application` (18) or
`save-configuration` (19). The application functions carry g90v1 with
qualifier 0x06 (all applications); the others no objects. It succeeds when
the outstation answers without an IIN2 error, and fails with `rejected` when
it answers NO_FUNC_CODE_SUPPORT or PARAMETER_ERROR. Feature `manageClient`.

An outstation with `manageServer` accepts all five and reports each in the
server event `manage`:

| Field | Meaning |
|-------|---------|
| `function` | The function code, a number |

ACTIVATE_CONFIG (31, g70v8 file specifications answered with g91v1) is not in
the contract: no reference master sends it, and an outstation refuses it
without a `manage` event. The one difference is how: the dscsystems
outstation answers PARAMETER_ERROR, not NO_FUNC_CODE_SUPPORT, because DSC
go-dnp3 v0.8.2 routes 31 to the same handler as 15..19 and answers
NO_FUNC_CODE_SUPPORT only to an outstation without any management handler.

## Broadcast

*Contract 1.3.* `operate --mode direct-no-ack --broadcast` sends the request
to the broadcast address 0xFFFF (IEEE 1815 clause 9.2.5.3: confirmation
optional) instead of `--remote-addr`. Nothing answers it; it succeeds when it
was sent. `--broadcast` with another mode is a usage error (exit 2). Feature
`broadcastClient`.

An outstation carries out a broadcast request it supports and sets IIN1.0
ALL_STATIONS in its next response, which a master sees in `iin` as
`BROADCAST`. Feature `broadcastServer`.

Server event `broadcast`, for a request to a broadcast address. Feature
`broadcastEventServer`.

| Field | Meaning |
|-------|---------|
| `function` | The function code of the request, a number |

## Event buffer overflow

*Contract 1.3.* An outstation whose fixture sets `eventBufferSize` buffers
at most that many events (see [FIXTURES.md](FIXTURES.md) for how each library
counts). An event that does not fit is lost, and the outstation sets IIN2.3
EVENT_BUFFER_OVERFLOW, which a master sees in `iin` as `EVENT_BUFFER_OVERFLOW`.
Feature `eventBufferServer`. The `events` fixture has a buffer of three.

## Analog deadbands

*Contract 1.3.* The reporting deadband of an analog input, group 34: a change
of the value larger than it makes an event, a smaller one does not.

| Operation | Flags | Does |
|-----------|-------|------|
| `deadband-write` | `--deadbands INDEX:VALUE[,...]`, VALUE a non-negative number | WRITE of g34v3 (single precision) with a one- or two-octet index prefix (the master library's choice; stepfunc always two). Feature `deadbandWriteClient` (master), `deadbandWriteServer` (outstation) |

It succeeds when the outstation accepted the write. A master reads the
deadbands with `read --group 34 --variation 1|2|3 --start A --stop B` (or
variation 0 without a range); each comes as an object of group 34 whose
`value` is the deadband, without `flags`. Feature `deadbandReadClient`
(master), `deadbandReadServer` (outstation).

## Device attributes

*Contract 1.3.* Device attributes, group 0 (IEEE 1815-2012 clause 11.9.1 and
table 11-1): an outstation serves those of its fixture (`deviceAttributes`;
the `attributes` fixture has a set of them), a master reads and writes them.
Features `deviceAttributesServer` and `deviceAttributesClient`.

| Operation | Flags | Sends | Succeeds when | Result |
|-----------|-------|-------|---------------|--------|
| `attribute-read` | `--set N` (required), `--variation V` (default 254: every attribute of the set) | READ g0vV, the set as the range | The response arrived without a request error | `attributes` |
| `attribute-write` | `--set N`, `--variation V` (required), exactly one of `--text S`, `--uint N`, `--int N` | WRITE g0vV with the value | A null response without a request error | |

`attributes`: `[{ "set", "variation", "type", "value" }]` in the order they
arrived. `type` is one of `visibleString`, `unsignedInt`, `signedInt`,
`float`, `octetString`, `bitString`, `time`, `attributeList`,
`extendedAttributeList`; `value` a string, a number, lower-case hex
(octet and bit strings), a time stamp, or null (lists). A read of set 0
returns what the outstation library adds there itself (point counts, sizes);
compare attributes of the fixture's set.

An outstation accepts a write of an attribute its fixture marks `writable`,
of the same type, and reports it with the server event `attribute-write`
(`set`, `variation`, `type`, `value`); it refuses other writes (a request
error).

## Counters that change

*Contract 1.4.* Nothing a master sends makes a counter count, so the running
outstation takes the change from outside DNP3, as a device takes pulses from
its inputs. `counter-increment` runs **in the server's container**:

```text
docker exec <server container> dnp3-interop counter-increment --index N [--by D]
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--index N` | required | The counter, 0..65535 |
| `--by D` | 1 | What to add, 1..4294967295. The value wraps modulo 2^32; no flag changes |
| `--control-socket PATH` | `/run/dnp3-interop/control.sock` | The server's control socket |
| `--timeout-ms N` | 5000 | How long to wait for the server |

The server opens the Unix socket before it writes its readiness file and
removes it on a clean stop; it opens none with `--outstations` above 1. What
passes on the socket is private to an adapter. The counter takes its new value
through the library's own update, as a device's input would, so an event of
class 1..3 is raised by the library's own detection, with the current time for
a variation that has one. Frozen counters do not change until a freeze.

The command prints one result document, as `client` does (`connected` means
the socket answered; `iin` is null, `objects` empty), with:

| Field | Meaning |
|-------|---------|
| `index` | The counter |
| `value` | Its new value; null on failure |

| Exit, `error.code` | When |
|--------------------|------|
| 0 | The counter changed; the server answered once the change was handed to the library |
| 1, `failed` | No counter at that index |
| 2 | Usage error: a bad flag; or an image with `counterIncrementServer` false, which has no such command |
| 3, `connect-failed` | No server on the socket |

Feature `counterIncrementServer`. The stepfunc, dscsystems and dnp3py adapters
read the value from the library's database. opendnp3 has no read of its
database, so that adapter holds the value itself, and learns of a FREEZE AND
CLEAR from the header of the request, which is all the library tells: it cannot
see the request's range, and takes every FREEZE AND CLEAR for one of all
counters, the only one `freeze --clear` sends.

### Frozen event policy

What a freeze does when it leaves a frozen value as it was is the outstation's
policy, and not settled here as a matter of the standard
([COMPATIBILITY.md](COMPATIBILITY.md#frozen-counter-events-a-freeze-semantic-and-an-event-policy)):

| Policy | A freeze that leaves the frozen value as it was |
|--------|-------------------------------------------------|
| `on-change` | Raises no frozen counter event |
| `always` | Raises one, with the time of that freeze |

Under both, a freeze that changes the value (or its flags) raises an event.
`frozenEventPolicies` in the capability document lists what the outstation can
do, its default first. Two entries: `server --frozen-event-policy P` sets it.
One: it is fixed by the library. None: the outstation raises no frozen counter
events (`frozenCounterEventsServer` false; `unsupportedReasons` says why).
A policy the image does not list is a usage error (exit 2).

Whether a freeze raises an event is one thing; what the outstation keeps of a
point's events until a master confirms them is another. `frozenEventRetention`
says which: `all`, or `most-recent` for only the latest event of each point.
Under either policy a freeze sets the frozen value and its time of freeze: a
read of g21v5 or g21v6 reports the newest freeze, also when it raised no event.

## More point types

*Contract 1.2.* Object documents for three more kinds of object, served by
outstations whose fixture has them (the `extended` fixture; see
[FIXTURES.md](FIXTURES.md)) and decoded by masters that declare it:

| Objects | Served when | Master feature | Document |
|---------|-------------|----------------|----------|
| Frozen analog inputs, g31 (static), g33 (events) | `pointTypes` has `frozenAnalogInputs` | `frozenAnalogInputsClient` | as an analog input: `value`, `flags` (analog flags), `time` for the variations with one |
| Binary output command events, g13 | feature `commandEventsServer`, and the output has a `commandEventClass` | `commandEventsClient` | `value` the commanded state (true for LATCH_ON, PULSE_ON, CLOSE), `status` the command status by name, `time` for g13v2; no `flags` |
| Analog output command events, g43 | as g13 | `commandEventsClient` | `value` the commanded value, `status`, `time` for g43v3, v4, v7, v8; no `flags` |
| Time and interval, g50v4 | `pointTypes` has `timeAndIntervals` | `timeAndIntervalClient` | `value` the interval, `time` the start time, `units` the interval units (IEEE 1815 table A-6: 0 none, 1 ms, 2 s, 3 min, 4 h, 5 day, ...) |

`read` names them as any other object: `read --group 31 --variation 0
--start 0 --stop 1`, `read --group 50 --variation 4 --start 0 --stop 0`.
Command events arrive in event polls of their class. A master that does not
declare the feature may fail a response that carries them (opendnp3 cannot
parse groups 31 and 33) or leave them out: read them only with masters that
declare it.

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
| `status` | Groups 13 and 43 (1.2): the command status of the control, by name ([command status](#command-status)) |
| `units` | Group 50 variation 4 (1.2): the units of the interval |
| `association` | Groups 121 and 122 (1.2): the association of the security statistic |

| Groups | `value` |
|--------|---------|
| 1, 2, 10, 11 | boolean |
| 3, 4 | double-bit state 0..3 (0 intermediate, 1 determined off, 2 determined on, 3 indeterminate) |
| 20, 21, 22, 23 | integer |
| 30, 32, 40, 42 | number |
| 110, 111 | the octets, lower-case hex |
| 31, 33 | number (1.2) |
| 13 | the commanded state, boolean (1.2) |
| 43 | the commanded value, number (1.2) |
| 50 (variation 4) | the interval, integer (1.2) |
| 121, 122 | the count of the security statistic, integer (1.2) |
| 34 | the deadband, non-negative number, no `flags` (1.3) |

`flags` names by bit, in bit order. The state bits of the binary types are
in `value`, not in `flags`.

| Bit | Groups 1, 2, 3, 4, 10, 11 | Groups 20..23, 121, 122 | Groups 30..33, 40, 42 |
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
image to stdout, byte for byte, and exits 0. An unknown name exits 1; a
name that is empty or contains a path separator (`/` or `\`) or `..` is a
usage error (exit 2).

A consumer reads the expected values from the image it tests against, so it
needs no checkout of this repository and cannot drift from the image.

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Success. `server`: clean stop after SIGTERM/SIGINT |
| 1 | The operation failed (see `error.code`); `server`: could not listen, TLS files that cannot be loaded, or an end without SIGTERM/SIGINT |
| 2 | Usage error: unknown command, operation or flag, missing or invalid value. Nothing was sent |
| 3 | `client`: the connection could not be established |
| 4 | `server`: the fixture is missing, invalid, or needs what the adapter does not serve |

## Output rules

- **stdout carries JSON only**: server events as JSON Lines, one client
  result, one capability document. `print-fixture` and `version` are the
  exceptions and print their own format.
- **stderr carries diagnostics** for people. Nothing on stderr is part of the
  contract. Library logging goes there too. `DNP3_INTEROP_DEBUG=1` in the
  environment turns the library logging up to its most detailed level
  (frames and objects); all four adapters read it.
- Field names and exit codes are stable within a major contract version.
  Fields may be added in a minor version; consumers ignore fields they do not
  know.
- A number with no fractional part is printed without one (`42`, not
  `42.0`), so that every adapter prints the same text for the same value.
- Every wait is bounded: by `--timeout-ms`, `--connect-timeout-ms` or the
  duration flags. No client operation runs indefinitely.
