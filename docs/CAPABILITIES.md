# Capabilities

`print-capabilities` tells a consumer what an adapter image can do, so that a
test suite can skip what a stack does not support instead of failing on it.

A capability is declared `true` only when the self-tests of this repository
exercise it for that adapter. An upstream library having an API for something
is not enough.

## Document

One JSON object on one line ([schema](../schemas/capabilities.schema.json)):

```json
{
  "schemaVersion": "1.0",
  "adapter": "opendnp3",
  "adapterVersion": "v0.1.0",
  "protocol": "dnp3",
  "upstream": {
    "name": "opendnp3",
    "version": "3.1.2",
    "revision": "26b4c01e4839bbbda8866655e086471c4917ee53",
    "license": "Apache-2.0",
    "language": "C++",
    "independentEngine": true
  },
  "roles": { "server": true, "client": true },
  "features": { "integrityPoll": true, "...": false },
  "pointTypes": ["binaryInputs", "..."],
  "commandTypes": ["g12v1", "g41v1", "..."],
  "clientOperations": ["integrity-poll", "..."]
}
```

An image with the feature `secureAuthentication` adds (contract 1.1):

```json
"secureAuthentication": { "macAlgorithms": ["hmac-sha256-16"], "keyWrapAlgorithms": ["aes-128", "aes-256"],
                          "criticalFunctions": [2, 3, 4, 5, "..."], "aggressiveMode": false }
```

| Field | Meaning |
|-------|---------|
| `macAlgorithms` | Values of `--sa-mac` the image accepts |
| `keyWrapAlgorithms` | Key wrap algorithms, by the length of the update key |
| `criticalFunctions` | Function codes the outstation challenges |
| `aggressiveMode` | Whether aggressive mode is used (never, in contract 1.1) |

| Field | Meaning |
|-------|---------|
| `adapter` | Adapter name; the image is `dnp3-interop-<adapter>` |
| `adapterVersion` | Release of this repository the image was built from (`dev` for a local build) |
| `upstream` | The implementation inside: name, exact version and revision, licence, language |
| `upstream.independentEngine` | `false` when the adapter wraps the protocol engine of another adapter. Results from such an adapter do not count as an additional independent implementation |
| `roles` | Which of `server` (outstation) and `client` (master) the image provides |
| `features` | See below; every value is a boolean |
| `pointTypes` | Point arrays a fixture may use with this adapter as server |
| `commandTypes` | Control objects the adapter handles as server and sends as client |
| `clientOperations` | Operations of `client` |
| `serverFeatures` | Contract 1.2, optional: features whose value for the server role differs from `features`, which then holds the client role's. The self-tests read a `server:` need from here first |

## Features

A feature without a role in its name applies to both roles: the outstation
serves it and the master sends it.

| Feature | Meaning | stepfunc | opendnp3 | dscsystems | dnp3py ¹³ |
|---------|---------|:--------:|:--------:|:----------:|:------:|
| `integrityPoll` | Class 1, 2, 3 and 0 in one READ | yes | yes | yes | yes |
| `eventPoll` | READ of event classes | yes | yes | yes | yes |
| `read` | READ of one group and variation, all objects or a range | yes | yes | yes | yes |
| `readAllObjectsClient` | `read` of all objects of a group (qualifier 0x06), not only a range | yes | yes | no ¹¹ | yes |
| `directOperate` | DIRECT OPERATE of g12v1 and g41v1..v4 | yes | yes | yes | yes |
| `selectBeforeOperate` | SELECT, then OPERATE, and `selectRequired` points | yes | yes | yes | yes |
| `parameterErrorWithNotSupported` | Server: the outstation sets IIN2.2 PARAMETER_ERROR with every NOT_SUPPORTED command status, not only for a point that does not exist. A behaviour, not a capability: cases name it either way | yes | yes | no | no |
| `directOperateNoAckServer` | The outstation applies DIRECT OPERATE NO ACK | yes | yes | yes | yes |
| `directOperateNoAckClient` | `operate --mode direct-no-ack` | no ² | no ² | yes | yes |
| `timeSyncLan` | RECORD CURRENT TIME and WRITE g50v3 | yes | yes | yes | yes |
| `timeSyncNonLan` | DELAY MEASURE and WRITE g50v1 | yes | yes | yes | yes |
| `timeSyncOnDemand` | The master synchronizes whether or not the outstation sets NEED_TIME | yes | no ³ | yes | yes |
| `restart` | COLD RESTART and WARM RESTART with the delay | yes | yes | yes | yes |
| `restartDelayClient` | `restart` reports the announced delay in `restartDelayMs` | yes | yes | no ¹² | yes |
| `unsolicited` | ENABLE and DISABLE UNSOLICITED, and events in unsolicited responses | yes | yes | yes | yes |
| `assignClassServer` | The outstation applies ASSIGN CLASS | no ⁴ | yes | yes | no |
| `assignClassClient` | `assign-class` | yes | yes | no ⁸ | yes |
| `freeze` | IMMEDIATE FREEZE and FREEZE AND CLEAR of g20v0 | yes | yes | yes ⁹ | yes |
| `freezeNoAckServer` | The outstation applies the `_NR` freezes | yes | yes | yes | yes |
| `freezeNoAckClient` | `freeze --no-ack` | no ² | no ² | no ² | yes |
| `octetStrings` | g110 in class 0 and reads | yes | yes | yes | yes |
| `tcp` | DNP3 over TCP | yes | yes | yes | yes |
| `udp` | DNP3 over UDP (1.1) | yes | yes ⁵ | yes ¹⁰ | no ¹⁴ |
| `tls` | DNP3 over TLS (IEC 62351-3), mutual authentication (1.1) | yes | yes ⁵ | yes | no ¹⁴ |
| `serial` | DNP3 over a serial line (1.1) | yes | yes | yes | no ¹⁴ |
| `fileTransfer` | File transfer, group 70, in at least one role (`fileServer` or `fileClient`) | yes ⁶ | no ⁷ | yes | no ¹⁴ |
| `fileServer` | The outstation serves the files of its fixture (1.1) | no ⁶ | no ⁷ | yes | no |
| `fileClient` | `file-get`, `file-put`, `file-ls`, `file-info` (1.1) | yes | no ⁷ | yes | no ¹⁴ |
| `fileDeleteClient` | `file-delete` (1.1) | no ⁶ | no ⁷ | yes | no ¹⁴ |
| `fileAppendServer` | The outstation opens a file for appending (1.3) | no ⁶ | no ⁷ | yes | no ¹⁴ |
| `fileAppendClient` | `file-put --append` (1.3) | yes | no ⁷ | no ³⁵ | no ¹⁴ |
| `fileAuthServer` | The outstation requires AUTHENTICATE_FILE with `--file-user` (1.3) | no ⁶ | no ⁷ | yes | no ¹⁴ |
| `fileAuthClient` | `file-authenticate`, `--file-user`, `--file-password` (1.3) | yes | no ⁷ | yes | no ¹⁴ |
| `secureAuthentication` | Secure authentication v5, group 120, in every role of the image (1.1) | no ⁷ | no ⁷ | yes ¹⁵ | no ¹⁴ |
| `commandEventsServer` | The outstation records command events (g13, g43) of outputs with a `commandEventClass` (1.2) | no ¹⁶ | no ¹⁶ | yes | no |
| `commandEventsClient` | The master reports g13 and g43 objects (1.2) | yes | yes | yes | no ¹⁷ |
| `frozenAnalogInputsClient` | The master reports g31 and g33 objects (1.2) | yes | no ¹⁶ | yes | no ¹⁷ |
| `timeAndIntervalClient` | The master reports g50v4 objects (1.2) | no ¹⁸ | yes | no ¹⁸ | no ¹⁷ |
| `dualEndpointServer` | `server --connect`: the outstation dials (TCP, and TLS where `tls`) (1.2) | yes | yes | yes | no |
| `dualEndpointClient` | `client --listen`: the master listens, over TCP (1.2) | yes | yes | yes | no ¹⁴ |
| `dualEndpointClientTls` | `client --listen --tls` (1.2) | no ¹⁹ | yes | yes | no ¹⁴ |
| `securityStatisticsServer` | With secure authentication on, the outstation serves g121 and raises g122 events (1.2) | no ⁷ | no ⁷ | yes | no |
| `securityStatisticsClient` | The master reports g121 and g122 objects (1.2) | no ⁷ | no ⁷ | yes ²⁰ | no ¹⁴ |
| `deviceAttributesServer` | The outstation serves the `deviceAttributes` of its fixture (1.3) | yes | no ²¹ | yes | no |
| `deviceAttributesClient` | `attribute-read`, `attribute-write` (1.3) | yes ²² | no ²¹ | yes | no |
| `multipleControlsClient` | `operate-multi` (1.3) | yes ²³ | yes | yes | yes |
| `maxControlsServer` | The outstation applies the fixture's `maxControlsPerRequest` (1.3) | yes | yes | yes | no ²⁴ |
| `linkConfirmsServer` | `server --link-confirms` (1.3) | no ³⁷ | no ³⁸ | yes | no ³⁹ |
| `linkConfirmsClient` | `client --link-confirms` (1.3) | no ³⁷ | no ³⁸ | yes | no ³⁹ |
| `keepAliveClient` | `client --keep-alive-ms` (1.3) | yes | yes | yes | no ³⁹ |
| `linkStatusEventServer` | The server event `link-status` (1.3) | no ⁴⁰ | no ⁴⁰ | yes | no ⁴⁰ |
| `multipleOutstationsServer` | `server --outstations N` (1.3) | no ⁴⁶ | yes | yes | no |
| `manageServer` | The outstation accepts the management functions (1.3) | no | no | yes | no |
| `manageClient` | `manage` (1.3) | no ⁴⁴ | yes | no ⁴⁵ | no |
| `broadcastServer` | The outstation carries out a broadcast request and sets IIN1.0 (1.3) | yes | yes | yes | no ⁴¹ |
| `broadcastEventServer` | The server event `broadcast` (1.3) | yes | no ⁴² | yes ⁴⁰ | no ⁴¹ |
| `broadcastClient` | `operate --broadcast` (1.3) | no ⁴³ | no ² | yes | no ⁴¹ |
| `eventBufferServer` | The outstation applies the fixture's `eventBufferSize` and sets IIN2.3 when it overflows (1.3) | yes | yes | yes ³⁶ | yes ³⁶ |
| `freezeAtTimeServer` | The outstation applies FREEZE AT TIME (1.3) | yes ²⁵ | no ²⁶ | yes ²⁷ | no ²⁶ |
| `freezeAtTimeClient` | `freeze --at` (1.3) | yes | no ²⁸ | yes | yes |
| `analogFreezeServer` | The outstation freezes analog inputs into frozen analog inputs (1.3) | no ²⁹ | no ²⁹ | yes | no ²⁹ |
| `analogFreezeClient` | `freeze --group 30` (1.3) | yes | yes | no ³⁰ | yes |
| `deadbandWriteServer` | The outstation applies a deadband written (g34) (1.3) | yes ³¹ | no ³² | yes | no ³² |
| `deadbandWriteClient` | `deadband-write` (1.3) | yes | no ³³ | yes | no ³³ |
| `deadbandReadServer` | The outstation serves a read of g34 (1.3) | yes | no ³² | no ³⁴ | no ³² |
| `deadbandReadClient` | A read of g34 comes back as objects (1.3) | yes | no ³³ | no ³⁴ | no ³³ |

² The library's master has no way to send it: opendnp3's `PerformFunction`
and stepfunc's `send_and_expect_empty_response` always wait for a response,
and neither has a direct-operate-no-ack mode. The flag is a usage error
(exit 2).

³ opendnp3's master synchronizes only on NEED_TIME and offers no call to do
it on request. The adapter reads the IIN first and fails with `failed` when
NEED_TIME is clear. The baseline outstation sets NEED_TIME until its time is
written, so the operation works against it.

⁴ stepfunc's outstation answers ASSIGN CLASS with NO_FUNC_CODE_SUPPORT.

⁵ opendnp3's UDP socket is connected to one peer: its outstation needs
`--udp-peer` (exit 2 without it). Its TLS master does not check the name in
the outstation's certificate, only that the CA signed it.

⁶ stepfunc's master reads, lists, describes and writes files; the library
has no DELETE_FILE and no outstation side of file transfer.

⁷ Not in the library: opendnp3 3.x removed secure authentication and has no
file transfer; stepfunc's open edition has no secure authentication.

⁸ go-dnp3's master has no call for ASSIGN CLASS.

⁹ go-dnp3's FREEZE AND CLEAR leaves counters without a frozen counter
uncleared (listed in tests/known-bugs.tsv; see COMPATIBILITY.md).

¹⁰ go-dnp3's UDP outstation answers the first endpoint it hears from for its
whole life. Its v0.8.1 also drops requests when it has no peer to send an
unsolicited response to; the adapter works around that (COMPATIBILITY.md).

¹¹ go-dnp3's master reads start-stop ranges and classes, not all objects of a
group; `read --group 60 --variation 1..4` works, as a class read.

¹² go-dnp3's master returns no delay from `Restart`; `restartDelayMs` is
null.

¹³ dnp3py's outstation cannot serve the baseline fixture (no double-bit
inputs, octet strings or per-point variations); it serves the `dnp3py`
fixture (FIXTURES.md). Its `serverFeatures` turn `read` (it ignores ranges and
specific variations), `unsolicited` and `octetStrings` off for the server
role. The adapter builds the requests its master has no call for (everything
but the polls) with dnp3py's application-layer types, and decodes responses
with its decoders.

¹⁴ dnp3py 0.6.0 has TCP only, no file transfer and no secure
authentication.

¹⁵ go-dnp3's SAv5 is a subset: challenge-reply with MAC algorithm 4
(HMAC-SHA-256, 16 octets), AES-128 or AES-256 key wrap, no aggressive mode,
no remote key management, unsolicited responses not authenticated. The
capability document says so in `secureAuthentication`. It is tested against
itself only: no other image has secure authentication.

¹⁶ Not in the library: stepfunc's and opendnp3's outstations have no command
events and no frozen analogs (opendnp3 does not even parse groups 31 and 33,
so a response with them fails as a whole). Their `pointTypes` say what they
serve: opendnp3 could serve `timeAndIntervals` in a fixture without frozen
analogs, stepfunc cannot.

¹⁷ dnp3py's decoders know none of groups 13, 31, 33, 43 and 50.

¹⁸ stepfunc's master and go-dnp3's master drop g50v4 objects (stepfunc warns
"ignored header variation"; go-dnp3 has no handler for the type). go-dnp3's
outstation serves them.

¹⁹ stepfunc's master listens with `spawn_master_tcp_server`; the crate has
no TLS listener for a master.

²⁰ go-dnp3's master drops groups 121 and 122; the adapter reads them off the
wire, from responses that hold only them (CONTAINER_CONTRACT.md).

²¹ opendnp3 3.x knows no group 0: its outstation answers OBJECT_UNKNOWN and its
master cannot parse it.

²² stepfunc 1.7.0's master decodes a signed integer attribute of one octet
without its sign (tests/known-bugs.tsv).

²³ stepfunc's master reports only the first refused control of a request:
`commandStatuses` is null.

²⁴ dnp3py's outstation carries out every control of a request whatever its
`max_controls_per_request`.

²⁵ stepfunc hands FREEZE AT TIME to the application with the time and
interval; the adapter schedules the freezes.

²⁶ opendnp3 and dnp3py answer FREEZE AT TIME with NO_FUNC_CODE_SUPPORT.

²⁷ go-dnp3 refuses time 0 with interval 0 (PARAMETER_ERROR) instead of freezing
at once, and runs scheduled freezes only while a master is connected
(tests/known-bugs.tsv, COMPATIBILITY.md).

²⁸ opendnp3's master cannot carry the g50v2 of FREEZE AT TIME
(`PerformFunction` headers have no object data).

²⁹ The stepfunc, opendnp3 and dnp3py outstations freeze counters only.

³⁰ go-dnp3's master freezes counters only.

³¹ stepfunc compares a change with the last value it reported, which until
the first event is the RESTART value the library starts a point with: the
first change after start makes an event whatever the deadband.

³² opendnp3 answers a write of g34 with an error; dnp3py's outstation has no
deadband to write or read.

³³ The library's master cannot write group 34 or decode it.

³⁴ go-dnp3's outstation answers a read of g34 with OBJECT_UNKNOWN, and its
master drops g34.

³⁵ go-dnp3's master opens files for writing only.

³⁶ go-dnp3 counts the size over all types; dnp3py per type and class, so a
class holds three times it (FIXTURES.md).

³⁷ stepfunc acknowledges confirmed user data but never sends it.

³⁸ opendnp3 3.x dropped link-layer confirms; it acknowledges them.

³⁹ dnp3py's link layer has no settings.

⁴⁰ The library answers REQUEST_LINK_STATUS without telling the application;
the go-dnp3 adapter reads it off the wire.

⁴¹ dnp3py's outstation ignores broadcast addresses and its master refuses
them.

⁴² opendnp3 carries out a broadcast without telling the application.

⁴³ stepfunc's master cannot address a broadcast.

⁴⁴ stepfunc's master has no group 90 for the application functions.

⁴⁵ go-dnp3's master has no request for them.

⁴⁶ stepfunc's server gives each connection one outstation, chosen by the
master's IP address, not by link address.

A capability is a property of the adapter and of the upstream version in the
image. The self-tests read it from the image and skip what a pairing lacks
(the `needs` column of [tests/cases.tsv](../tests/cases.tsv)), so "no" here
means "not tested", never "tested and failing".

## Point types and variations

The three server adapters serve all eight point arrays of the fixture format, with
the static and event variations [FIXTURES.md](FIXTURES.md#points) lists, and
handle and send all five control objects (`g12v1`, `g41v1`..`g41v4`).

The dscsystems server has two limits of its library: the indexes of each
point type must run from 0 without gaps, and `outstation.needTime` must be
true (go-dnp3 asserts NEED_TIME until a master writes the time). It refuses
other fixtures with exit 4.

## Rules for consumers

- Read the capabilities from the image under test, not from this page.
- Skip, do not fail, a scenario that needs a feature the adapter declares
  `false`.
- Count independent implementations by `upstream.independentEngine`.
