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

## Features

A feature without a role in its name applies to both roles: the outstation
serves it and the master sends it.

| Feature | Meaning | stepfunc | opendnp3 |
|---------|---------|:--------:|:--------:|
| `integrityPoll` | Class 1, 2, 3 and 0 in one READ | yes | yes |
| `eventPoll` | READ of event classes | yes | yes |
| `read` | READ of one group and variation, all objects or a range | yes | yes |
| `directOperate` | DIRECT OPERATE of g12v1 and g41v1..v4 | yes | yes |
| `selectBeforeOperate` | SELECT, then OPERATE, and `selectRequired` points | yes | yes |
| `directOperateNoAckServer` | The outstation applies DIRECT OPERATE NO ACK | no ¹ | no ¹ |
| `directOperateNoAckClient` | `operate --mode direct-no-ack` | no ² | no ² |
| `timeSyncLan` | RECORD CURRENT TIME and WRITE g50v3 | yes | yes |
| `timeSyncNonLan` | DELAY MEASURE and WRITE g50v1 | yes | yes |
| `timeSyncOnDemand` | The master synchronizes whether or not the outstation sets NEED_TIME | yes | no ³ |
| `restart` | COLD RESTART and WARM RESTART with the delay | yes | yes |
| `unsolicited` | ENABLE and DISABLE UNSOLICITED, and events in unsolicited responses | yes | yes |
| `assignClassServer` | The outstation applies ASSIGN CLASS | no ⁴ | yes |
| `assignClassClient` | `assign-class` | yes | yes |
| `freeze` | IMMEDIATE FREEZE and FREEZE AND CLEAR of g20v0 | yes | yes |
| `freezeNoAckServer` | The outstation applies the `_NR` freezes | no ¹ | no ¹ |
| `freezeNoAckClient` | `freeze --no-ack` | no ² | no ² |
| `octetStrings` | g110 in class 0 and reads | yes | yes |
| `tcp` | DNP3 over TCP | yes | yes |
| `udp` | DNP3 over UDP | no ⁵ | no ⁵ |
| `tls` | DNP3 over TLS (IEC 62351-3) | no ⁵ | no ⁵ |
| `serial` | DNP3 over a serial line | no ⁵ | no ⁵ |
| `fileTransfer` | File transfer, group 70 | no ⁶ | no ⁷ |
| `secureAuthentication` | Secure authentication v5, group 120 | no ⁷ | no ⁷ |

¹ Both outstations handle the request, but neither master can send it, so
the self-tests do not exercise it. It becomes testable with a master that
can (DSC Systems go-dnp3 has DIRECT OPERATE NO ACK).

² The library's master has no way to send it: opendnp3's `PerformFunction`
and stepfunc's `send_and_expect_empty_response` always wait for a response,
and neither has a direct-operate-no-ack mode. The flag is a usage error
(exit 2).

³ opendnp3's master synchronizes only on NEED_TIME and offers no call to do
it on request. The adapter reads the IIN first and fails with `failed` when
NEED_TIME is clear. The baseline outstation sets NEED_TIME until its time is
written, so the operation works against it.

⁴ stepfunc's outstation answers ASSIGN CLASS with NO_FUNC_CODE_SUPPORT.

⁵ Both libraries have these transports; contract 1.0 covers TCP only. See
[PLAN.md](../PLAN.md).

⁶ stepfunc's master reads files; its outstation has no file transfer, and
no other published adapter serves files yet.

⁷ Not in the library: opendnp3 3.x removed secure authentication and has no
file transfer; stepfunc's open edition has no secure authentication.

A capability is a property of the adapter and of the upstream version in the
image. The self-tests read it from the image and skip what a pairing lacks
(the `needs` column of [tests/cases.tsv](../tests/cases.tsv)), so "no" here
means "not tested", never "tested and failing".

## Point types and variations

Both adapters serve all eight point arrays of the fixture format, with the
static and event variations [FIXTURES.md](FIXTURES.md#points) lists, and
handle and send all five control objects (`g12v1`, `g41v1`..`g41v4`).

## Rules for consumers

- Read the capabilities from the image under test, not from this page.
- Skip, do not fail, a scenario that needs a feature the adapter declares
  `false`.
- Count independent implementations by `upstream.independentEngine`.
