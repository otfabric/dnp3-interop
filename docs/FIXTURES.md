# Fixtures

A fixture describes a simulated DNP3 outstation: its link addresses, its
point database and what controls do to it. It contains no assertions and no
implementation detail. Every server adapter serves the same fixture the same
way; this document says what "the same way" is.

- Format: JSON, [schema](../fixtures/schema/dnp3-fixture.schema.json), `schemaVersion` `"1.0"`
- Location: `fixtures/<name>/fixture.json`, baked into every image under `/fixtures`
- Validation: `make validate` (schema plus the cross-field rules below); adapters apply the same rules at start and exit 4 on a violation

## Format

```json
{
  "schemaVersion": "1.0",
  "name": "baseline",
  "outstation": { "address": 1024, "masterAddress": 1, "unsolicited": true, "needTime": true,
                  "coldRestartDelayMs": 5000, "warmRestartDelayMs": 1000 },
  "binaryInputs": [ { "index": 0, "value": true, "class": 1 } ],
  "binaryOutputs": [ { "index": 0, "value": false, "class": 1, "target": 1 } ],
  "analogOutputs": [ { "index": 1, "value": 0, "staticVariation": 3, "selectRequired": true, "min": -100, "max": 100 } ]
}
```

### Outstation

| Field | Meaning |
|-------|---------|
| `address` | The outstation's link address, 0..65519 |
| `masterAddress` | The link address of the one master it answers; must differ from `address` |
| `unsolicited` | Whether the outstation supports unsolicited responses (default false). See [Unsolicited responses](#unsolicited-responses) |
| `needTime` | Whether IIN1.4 NEED_TIME is set at start (default false). It is cleared when a master writes the time |
| `coldRestartDelayMs`, `warmRestartDelayMs` | The delay the outstation announces for a cold or warm restart, 0..65535 ms. Absent: that restart is refused |
| `maxControlsPerRequest` | Contract 1.3: the most controls of one request the outstation carries out, 1..255; the rest are answered TOO_MANY_OPS. Absent: the library's default (at least 16) |
| `eventBufferSize` | Contract 1.3: the most events the outstation buffers, 1..65535: of each type (stepfunc, opendnp3), in all (DSC go-dnp3, one buffer), or of each type in each class (dnp3py, whose class buffer holds three times it). An event that does not fit is lost and the outstation sets IIN2.3 EVENT_BUFFER_OVERFLOW. Absent: the adapter's default (1000; dnp3py 100) |

### Points

Eight arrays, one per point type, each optional (two more under
[More point types](#more-point-types)). Within a type, indexes are
unique; they need not be contiguous.

| Array | Static group | Event group | Controlled by |
|-------|-------------:|------------:|---------------|
| `binaryInputs` | 1 | 2 | |
| `doubleBitInputs` | 3 | 4 | |
| `binaryOutputs` | 10 | 11 | g12v1 |
| `counters` | 20 | 22 | freeze |
| `frozenCounters` | 21 | 23 | freeze |
| `analogInputs` | 30 | 32 | |
| `analogOutputs` | 40 | 42 | g41v1..v4 |
| `octetStrings` | 110 | 111 | |

| Field | Meaning |
|-------|---------|
| `index` | Point index, 0..65535 |
| `value` | Initial value: see the table |
| `flags` | Flag names that are set (see [Object documents](CONTAINER_CONTRACT.md#object-documents)); default `["ONLINE"]`. `[]` is an offline point |
| `class` | Event class 1..3, or 0 (the default) for a point that produces no events |
| `staticVariation` | Variation in a class 0 response and a read with variation 0 |
| `eventVariation` | Variation of its events |
| `target` | `binaryOutputs`, `analogOutputs`: index of a binary or analog input that follows the output; at most one output per input |
| `selectRequired` | `binaryOutputs`, `analogOutputs`: when true, only select-before-operate is accepted. Default false |
| `min`, `max` | `analogOutputs`: the accepted range of a control value, inclusive. Default unlimited |

| Array | `value` | Static variations (default first) | Event variations (default first) |
|-------|---------|-----------------------------------|----------------------------------|
| `binaryInputs` | boolean | 2, 1 | 2, 1 |
| `doubleBitInputs` | 0..3 | 2 | 2, 1 |
| `binaryOutputs` | boolean | 2 | 2, 1 |
| `counters` | 0..4294967295 | 1, 2, 5, 6 | 1, 2, 5, 6 |
| `frozenCounters` | 0..4294967295 | 1, 2, 5, 6, 9, 10 | 1, 2, 5, 6 |
| `analogInputs` | number | 1, 2, 3, 4, 5, 6 | 1, 2, 3, 4, 5, 6, 7, 8 |
| `analogOutputs` | number | 1, 2, 3, 4 | 1, 2, 3, 4, 5, 6, 7, 8 |
| `octetStrings` | 1..255 octets, lower-case hex | the length | the length |

These are the variations the stepfunc, opendnp3 and dscsystems outstations
serve (dnp3py serves fewer: see the `dnp3py` fixture in
[COMPATIBILITY.md](COMPATIBILITY.md#dnp3pys-outstation-the-dnp3py-fixture)). Variations with relative
time (g2v3, g4v3) are left out because they need a CTO header whose content
depends on the outstation.

Cross-field rules, checked by `make validate` and by every adapter:

- The value fits every variation the point uses: 16-bit variations take
  0..65535 (counters) or -32768..32767 (analogs), integer variations take
  integers, and every analog value (and `min`, `max`) is exactly
  representable in single precision, so that every stack prints the same
  number. Use 230.5, not 230.1.
- `min` <= `value` <= `max`.
- A `target` exists in the matching input type and is the target of one
  output only.
- Every frozen counter has a counter with the same index: that is the
  counter a freeze copies.
- A point whose static variation has no flags octet (g1v1, g20v5, g20v6,
  g21v9, g21v10, g30v3, g30v4, g31v5, g31v6) keeps the default flags
  `["ONLINE"]`: a master reads it as online whatever the outstation holds.
- `commandEventVariation` needs `commandEventClass`.
- Each (`set`, `variation`) of `deviceAttributes` is listed once.

### More point types

*Fixture format 1.0, used from container contract 1.2.* An adapter serves a
fixture with these only if it declares them (`pointTypes`, feature
`commandEventsServer`); every other adapter refuses it (exit 4).

| Field | Meaning |
|-------|---------|
| `frozenAnalogInputs` | Points like analog inputs (`index`, `value`, `flags`, `class`), groups 31 and 33. Static variations 1, 2, 5, 6, 7, 8 (default 1; the ones with a time of freeze are left out), event variations 1, 2, 5, 6 (default 1). Their events come from freezes of analog inputs, which no master in the contract sends |
| `timeAndIntervals` | `[{ "index", "time": "YYYY-MM-DDTHH:MM:SS.mmmZ", "interval", "units" }]`: group 50 variation 4 objects, static only, read by range (not in class 0) |
| `commandEventClass`, `commandEventVariation` | On `binaryOutputs` and `analogOutputs`: the class (1..3) and variation (g13: 1, 2; g43: 1..8) of the command events the outstation records when a control of the point reaches its control handler: OPERATE, DIRECT OPERATE and DIRECT OPERATE NO ACK, not SELECT. The event carries the commanded state or value and the status the outstation answered, refusals included (OUT_OF_RANGE, ...) |

The value rules of analog inputs apply to frozen analog inputs.

### Device attributes

*Fixture format 1.0, used from container contract 1.3.* Only an adapter with
the feature `deviceAttributesServer` serves a fixture with `deviceAttributes`.

| Field | Meaning |
|-------|---------|
| `deviceAttributes` | `[{ "set": 1..255, "variation": 1..253, "type": "visibleString" \| "unsignedInt" \| "signedInt", "value", "writable": false }]`; a `visibleString` value has at most 255 characters and must also fit in 255 octets of UTF-8 (the length octet on the wire); an adapter refuses a longer one (exit 4). Private sets only (set 0 holds the standard's attributes, which the libraries derive) |

A writable attribute accepts a WRITE of the same type, which replaces its
value until the outstation restarts.

### Files

*Fixture format 1.0, used from container contract 1.1.* Files the
outstation serves by DNP3 file transfer (group 70); see
[file transfer](CONTAINER_CONTRACT.md#file-transfer). Only an adapter with
the feature `fileServer` serves a fixture that has `files` or `directories`;
every other adapter refuses it (exit 4).

| Field | Meaning |
|-------|---------|
| `files` | `[{ "name": "/data/blocks.bin", "content": "<hex>" }]`: absolute names of at most 200 characters, of path elements of letters, digits, `.`, `_` and `-` (not starting with `.`); content up to 65536 octets, lower-case hex |
| `directories` | Further directories, empty at start: `["/logs"]`. The directories that hold files exist without being listed |

A name is a file or a directory, not both, and is listed once. Writes may
create or replace files in any directory that exists; deletes remove files.

## Outstation behaviour

### Link layer

The outstation answers frames addressed to `address` from `masterAddress`
only. A frame from another master address, or to another outstation
address, is not answered: the master times out.

### Reads

| Request | Answer |
|---------|--------|
| Class 0 (g60v1), alone or in an integrity poll | Every point of every type in its static variation |
| Class 1, 2, 3 (g60v2..v4) | The events of that class not yet confirmed, in the order they were generated |
| A group with variation 0, all objects or a range | Those points in their static variation |
| A group with a specific static variation | Those points in that variation |

The order of the types in a class 0 response, and how they are split into
headers, is the outstation library's: consumers compare by group, variation
and index. Events stay in the buffer until the master confirms the response
that carried them; masters confirm automatically.

What the outstation answers to a read of indexes it does not have, or of a
variation it does not serve, is left to its library and is not part of the
fixture behaviour; [COMPATIBILITY.md](COMPATIBILITY.md) records what each
does.

### Internal indications

| Bit | Set |
|-----|-----|
| DEVICE_RESTART (IIN1.7) | From start until a master clears it. Every master library does so as soon as it sees the bit |
| NEED_TIME (IIN1.4) | From start when `needTime`, until a master writes the time |
| CLASS_1_EVENTS .. CLASS_3_EVENTS | While unconfirmed events of that class exist |
| NO_FUNC_CODE_SUPPORT, OBJECT_UNKNOWN, PARAMETER_ERROR | In the response to a request the outstation refuses |

No other bit is ever set.

### Controls (SELECT, OPERATE, DIRECT OPERATE, DIRECT OPERATE NO ACK)

For a control of type `g12v1` on a binary output, or `g41v1..v4` on an analog
output, at index `i`:

| Condition, first match wins | Status | Effect |
|-----------------------------|--------|--------|
| No output of that type at `i` | NOT_SUPPORTED | none |
| `g12v1` with a code other than LATCH_ON, LATCH_OFF, CLOSE (pulse on, close) or TRIP (pulse on, trip) | NOT_SUPPORTED | none |
| `g41`: value below `min` or above `max` | OUT_OF_RANGE | none |
| DIRECT OPERATE (with or without ack) and `selectRequired` | NOT_SUPPORTED | none |
| SELECT | SUCCESS | none: the library arms the point |
| OPERATE after a matching select, or DIRECT OPERATE | SUCCESS | see below |

Effect of a successful operate:

| Control | Output | Target |
|---------|--------|--------|
| `g12v1` LATCH_ON, CLOSE | value := true | value := true |
| `g12v1` LATCH_OFF, TRIP | value := false | value := false |
| `g41` | value := control value | value := control value |

Count, on time and off time are not evaluated. Flags of the output and the
target are not changed. Matching an operate to its select, the select
timeout (`--select-timeout-ms`) and the sequence number check are done by
the outstation library before this table applies.

### Freeze (IMMEDIATE FREEZE, FREEZE AND CLEAR, and their `_NR` forms)

Only for g20v0, all objects (qualifier 0x06) or a range. For every selected
counter:

- if a frozen counter with the same index exists: its value := the counter's
  value, its flags := the counter's flags, its time stamp := now;
- FREEZE AND CLEAR then sets the counter to 0, whether or not it has a
  frozen counter.

The `_NR` forms do the same without a response.

### Events

A point with `class` 1..3 produces an event when a request or
[`counter-increment`](CONTAINER_CONTRACT.md#counters-that-change) changes its
value (a control, a freeze, a clear, an increment). An unchanged value produces
no event, with one exception: whether a freeze raises a frozen counter event
for a frozen value that stayed the same is the outstation's own policy, and the
libraries differ ([frozen event policy](CONTAINER_CONTRACT.md#frozen-event-policy)).
The event carries the point's flags, the new value and the current time when
its `eventVariation` has a time stamp.

Nothing else produces events: the outstation never changes a value on its
own, and a counter changes only by a clear or by `counter-increment`.

### Time synchronization

| Request | Answer | Effect |
|---------|--------|--------|
| RECORD CURRENT TIME, then WRITE g50v3 | Null responses | The outstation's time is set to the written time plus the delay since the record; NEED_TIME cleared |
| DELAY MEASURE | g52v2 with the processing delay | none |
| WRITE g50v1 | Null response | Time set; NEED_TIME cleared |

The outstation does not change the host clock. It reports the written time
in the `time-sync` event; event time stamps are taken from the host clock.

### Restart (COLD RESTART, WARM RESTART)

| Condition | Answer |
|-----------|--------|
| `coldRestartDelayMs` (or `warmRestartDelayMs`) given | g52v2 with that delay; the `restart` event |
| Not given | NO_FUNC_CODE_SUPPORT |

The outstation **does not restart**: the connection stays up and the point
values are unchanged. Restart the container for a real restart.

DSC go-dnp3 models the restart itself, and the dscsystems server cannot prevent
it: a cold restart discards the unconfirmed events and asserts DEVICE_RESTART
and NEED_TIME again, and a restart requested within the delay of the
previous one is answered with the time left of that delay and
ALREADY_EXECUTING.

### Unsolicited responses

Only when `unsolicited` is true. Otherwise ENABLE and DISABLE UNSOLICITED
are answered with NO_FUNC_CODE_SUPPORT.

- At start no class reports unsolicited. ENABLE UNSOLICITED with g60v2..v4
  turns reporting on for those classes, DISABLE UNSOLICITED off. This state
  belongs to the outstation and survives connections.
- The outstation sends a null unsolicited response (no objects) once a
  master is connected, as the standard requires, and repeats it until it is
  confirmed. Whether it does so again on every new connection is up to its
  library.
- Once that is confirmed, the events of enabled classes are sent in
  unsolicited responses, at once, and repeated until confirmed: those that
  are buffered when a class is enabled and every new one after that.

### Assign class (ASSIGN CLASS)

Only with the feature `assignClassServer`; otherwise NO_FUNC_CODE_SUPPORT. A g60
header names the class (g60v1: class 0, no events), the following headers
(variation 0, all objects or a range) the points. Events those points
produce from then on are in the new class. The assignment survives
connections.

### What an outstation never does

- Change a value without a request. Restart the container to return to the
  fixture.
- Send an unsolicited response before a master enabled a class, apart from
  the null response at start.

## The baseline fixture

Outstation 1024, master 1, unsolicited responses supported, NEED_TIME at
start, cold restart 5000 ms, warm restart 1000 ms.

| Type | Index | Value | Flags | Class | Static | Event | Notes |
|------|------:|-------|-------|------:|-------:|------:|-------|
| binary input | 0 | true | ONLINE | 1 | g1v2 | g2v2 | |
| binary input | 1 | false | ONLINE | 1 | g1v2 | g2v2 | target of binary output 0 |
| binary input | 2 | true | ONLINE, LOCAL_FORCED | 2 | g1v2 | g2v1 | |
| binary input | 3 | false | (offline) | 0 | g1v2 | | |
| binary input | 4 | true | ONLINE | 0 | g1v1 | | packed, no flags on the wire |
| double-bit input | 0 | 2 (determined on) | ONLINE | 1 | g3v2 | g4v2 | |
| double-bit input | 1 | 0 (intermediate) | ONLINE, COMM_LOST | 0 | g3v2 | | |
| binary output | 0 | false | ONLINE | 1 | g10v2 | g11v2 | target binary input 1 |
| binary output | 1 | false | ONLINE | 1 | g10v2 | g11v2 | select required |
| counter | 0 | 123456 | ONLINE | 3 | g20v1 | g22v1 | |
| counter | 1 | 65535 | ONLINE | 3 | g20v2 | g22v2 | 16 bits |
| counter | 2 | 4294967295 | ONLINE | 0 | g20v5 | | no flags on the wire |
| frozen counter | 0 | 0 | ONLINE | 3 | g21v1 | g23v1 | |
| frozen counter | 1 | 0 | ONLINE | 3 | g21v2 | g23v2 | |
| analog input | 0 | 230.5 | ONLINE | 2 | g30v5 | g32v7 | float |
| analog input | 1 | -1234 | ONLINE | 2 | g30v1 | g32v3 | |
| analog input | 2 | 32767 | ONLINE | 0 | g30v2 | | 16 bits |
| analog input | 3 | 0 | OVER_RANGE (offline) | 0 | g30v1 | | |
| analog input | 4 | 0 | ONLINE | 2 | g30v1 | g32v1 | target of analog output 0 |
| analog output | 0 | 0 | ONLINE | 1 | g40v1 | g42v1 | target analog input 4, range ±1000000 |
| analog output | 1 | 0 | ONLINE | 1 | g40v3 | g42v3 | float, select required, range ±100 |
| octet string | 0 | `48656c6c6f` ("Hello") | | 0 | g110v5 | | |

## The files fixture

`fixtures/files/fixture.json`: outstation 1024, master 1, NEED_TIME at start, one binary input,
and for file transfer:

| Name | Type | Content |
|------|------|---------|
| `/readme.txt` | file | "Hello from the dnp3-interop files fixture.\n" (43 octets) |
| `/data` | directory | |
| `/data/blocks.bin` | file | 3000 octets, octet *i* = (7*i* + 3) mod 256: several blocks with any block size |
| `/logs` | directory | empty |

## The dnp3py fixture

`fixtures/dnp3py/fixture.json`: the baseline within what dnp3py's outstation
serves, so that it can be compared with the others. Outstation 1024, master
1, NEED_TIME at start, no unsolicited responses, cold restart 5000 ms, warm
restart 1000 ms.

| Type | Index | Value | Flags | Class | Static | Event | Notes |
|------|------:|-------|-------|------:|-------:|------:|-------|
| binary input | 0 | true | ONLINE | 1 | g1v2 | g2v1 | |
| binary input | 1 | false | ONLINE | 1 | g1v2 | g2v1 | target of binary output 0 |
| binary input | 2 | true | ONLINE, LOCAL_FORCED | 1 | g1v2 | g2v1 | |
| binary input | 3 | false | (offline) | 0 | g1v2 | | |
| binary output | 0 | false | ONLINE | 0 | g10v2 | | target binary input 1 |
| binary output | 1 | false | ONLINE | 0 | g10v2 | | select required |
| counter | 0 | 123456 | ONLINE | 3 | g20v1 | g22v5 | |
| counter | 1 | 65535 | ONLINE | 3 | g20v1 | g22v5 | |
| counter | 2 | 4294967295 | ONLINE | 0 | g20v1 | | |
| frozen counter | 0, 1 | 0 | ONLINE | 0 | g21v1 | | |
| analog input | 0 | -1234 | ONLINE | 2 | g30v1 | g32v1 | |
| analog input | 1 | 32767 | ONLINE | 0 | g30v1 | | |
| analog input | 2 | 0 | OVER_RANGE (offline) | 0 | g30v1 | | |
| analog input | 3 | 0 | ONLINE | 2 | g30v1 | g32v1 | target of analog output 0 |
| analog output | 0 | 0 | ONLINE | 0 | g40v1 | | target analog input 3, range ±1000000 |
| analog output | 1 | 0 | ONLINE | 0 | g40v1 | | select required, range ±100 |

Every outstation whose point types cover it serves it;
`FIXTURE=dnp3py scripts/interop.sh` runs tests/cases-dnp3py.tsv against all
of them.

## The controls fixture

`fixtures/controls/fixture.json`: outstation 1024, master 1, NEED_TIME at
start, at most two controls per request. Binary inputs 0 and 1 (false, no
events), binary outputs 0 and 1 (targets binary inputs 0 and 1) and 2,
analog inputs 0 and 1, analog outputs 0 (range ±1000, target analog input 0)
and 1 (range ±100, target analog input 1). Every outstation serves it.

## The events fixture

`fixtures/events/fixture.json` (contract 1.3): outstation 1024, master 1,
NEED_TIME at start, an event buffer of three. Analog input 0 (class 2) and
analog output 0 (range ±100000, target analog input 0, no events): each
control of the output makes one analog input event. Every outstation serves
it.

## The counters fixture

`fixtures/counters/fixture.json` (contract 1.4): outstation 1024, master 1,
NEED_TIME at start. Counters 0 (100, g20v1, g22v1) and 1 (200, g20v2, g22v2) in
class 2; frozen counters 0 (g21v1, g23v5) and 1 (g21v2, g23v6) at 0, in class
3, so that the first freeze changes them and frozen counter events poll apart
from counter events. Every outstation with `frozenCounterEventsServer` serves
it.

## The attributes fixture

`fixtures/attributes/fixture.json`: outstation 1024, master 1, NEED_TIME at
start, one binary input, and in attribute set 1:

| Variation | Type | Value | Writable |
|----------:|------|-------|:--------:|
| 1 | visibleString | "dnp3-interop reference outstation" | no |
| 2 | unsignedInt | 4000000000 | no |
| 3 | signedInt | -42 | no |
| 10 | visibleString | "writable text" | yes |
| 11 | unsignedInt | 7 | yes |

## The extended fixture

`fixtures/extended/fixture.json`: outstation 1024, master 1, NEED_TIME at
start, no unsolicited responses.

| Type | Index | Value | Class | Notes |
|------|------:|-------|------:|-------|
| binary input | 0 | true | 0 | |
| binary input | 1 | false | 1 | target of binary output 0 |
| binary output | 0 | false | 1 | target binary input 1; command events g13v2 in class 2 |
| analog input | 0 | 0 | 1 | target of analog output 0 |
| analog output | 0 | 0 | 1 | target analog input 0, range ±1000000; command events g43v3 in class 2 |
| frozen analog input | 0 | 1500 | 0 | g31v1 |
| frozen analog input | 1 | 49.5 | 0 | g31v7 (float), flags ONLINE, OVER_RANGE |
| time and interval | 0 | 2026-01-01T00:00:00.000Z, every 15 minutes (units 3) | | g50v4 |

## Adding a fixture

1. Create `fixtures/<name>/fixture.json`; `name` must equal the directory.
2. `make validate`.
3. Use only types and variations the adapters that should serve it declare
   ([CAPABILITIES.md](CAPABILITIES.md)); a fixture must not need
   adapter-specific behaviour.
4. Rebuild the images (`make images`): fixtures are baked in.
5. If the fixture is meant for the self-tests, add cases for it; a fixture
   nothing exercises is not verified.
