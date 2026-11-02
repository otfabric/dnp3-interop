# Coverage

What a consumer can exercise against the published adapters today.
"Cross-stack" is what this repository's own self-tests (`make interop`,
`make features`) cover between the reference stacks.

| Category | Available to a consumer | Cross-stack self-test |
|----------|-------------------------|-----------------------|
| TCP connection lifecycle | connect, disconnect; one master at a time, a new connection replaces the old; reconnect by restarting the server container | connect, disconnect; a second connection is served and the first is closed, on every outstation |
| Link layer | Addressing (outstation 1024, master 1); frames from another master address or to another outstation address are not answered | wrong outstation address, wrong master address (not by the DSC go-dnp3 outstation and master: known bug, see [docs/COMPATIBILITY.md](docs/COMPATIBILITY.md)); a control from another master address is not carried out (the DSC go-dnp3 and dnp3py outstations carry it out: known bugs) |
| Integrity poll | Class 1, 2, 3 and 0 in one READ | yes, values and flags against the fixture |
| Static data | Groups 1, 3, 10, 20, 21, 30, 40, 110 in the static variations of the fixture format, including the variations without flags and the 16-bit ones; all objects, ranges, and a specific variation | yes |
| Event data | Groups 2, 4, 11, 22, 23, 32, 42 with and without time, caused by controls and freezes, in classes 1..3; confirmed events are not sent again; events the master did not confirm are sent again (1.5) | yes; unconfirmed events: the dnp3py master (`--no-confirm`) against every outstation (dnp3py's outstation loses them: known bug) |
| Controls | g12v1 (LATCH_ON, LATCH_OFF, CLOSE, TRIP; PULSE_ON and PULSE_OFF on a `pulse` output, 1.5) and g41v1..v4; DIRECT OPERATE and SELECT/OPERATE; refusals NOT_SUPPORTED (with PARAMETER_ERROR) and OUT_OF_RANGE; select-required points; an OPERATE after the select timeout answers TIMEOUT and one without a SELECT NO_SELECT (1.5); the fields of the server's control event | yes; select expiry: the dnp3py master (`--operate-delay-ms`) against every outstation (dnp3py's outstation answers NO_SELECT: known bug) |
| DIRECT OPERATE NO ACK | Served by every outstation; sent by the DSC go-dnp3 and dnp3py masters | yes |
| Freeze without acknowledgement | Served by every outstation; sent by the opendnp3 and dnp3py masters | yes, values by the dnp3py fixture too |
| Time synchronization | LAN (RECORD CURRENT TIME, g50v3) and non-LAN (DELAY MEASURE, g50v1); NEED_TIME until the time is written; the written time in the server's event stream | yes |
| Restart | Cold and warm restart with the delay in g52v2; the outstation does not actually restart; DEVICE_RESTART until a master clears it; the server's restart event | yes |
| Unsolicited responses | Null unsolicited response at connection; ENABLE and DISABLE UNSOLICITED per class; buffered and new events sent unsolicited and confirmed; nothing sent unsolicited after DISABLE UNSOLICITED | yes |
| Freeze | IMMEDIATE FREEZE and FREEZE AND CLEAR of g20v0; frozen counter and counter events | yes |
| Counters that change (1.4) | `counter-increment` on every outstation: the value, its g22 event, the frozen value held until a freeze, a change after a clear; a wrap modulo 2^32 | yes |
| Frozen counter events with time (1.4) | g23v5 and g23v6 from the opendnp3 and DSC go-dnp3 outstations to every master, with the time of the freeze. dnp3py's outstation: unsupported, with the reason | yes |
| Frozen event policy (1.4) | opendnp3 (`on-change`) and DSC go-dnp3 (`always`) as declared; g21v5 carries the newest freeze's time with or without an event; `frozenEventRetention` as declared (both: `all`) | yes |
| Assign class | Served by the opendnp3 and DSC go-dnp3 outstations; sent by the opendnp3 and dnp3py masters | yes, where declared; the server event of opendnp3's outstation |
| IIN | DEVICE_RESTART until cleared, NEED_TIME, class event bits, request errors | yes |
| UDP, TLS, serial (1.1) | opendnp3 and DSC go-dnp3, both roles | yes; TLS: a wrong CA, an untrusted master and a certificate of another name are refused (opendnp3's master accepts the name: known bug); UDP: a master's own local port; serial: also 19200 baud, even parity, two stop bits |
| File transfer, g70 (1.1) | Served by the DSC go-dnp3 outstation; sent by the DSC go-dnp3 and dnp3py masters | yes, with the server's delete event |
| Secure authentication v5, g120 (1.1) | DSC go-dnp3, both roles | yes, DSC go-dnp3 against itself, with an AES-128 and an AES-256 update key |
| Frozen analog inputs, command events, time and interval (1.2) | Served by the DSC go-dnp3 outstation (`extended` fixture); time and interval by opendnp3's outstation too; reported by the masters that declare them (g31/g33: DSC go-dnp3, dnp3py; g13/g43: every master; g50v4: opendnp3, dnp3py) | yes |
| Dual end point (1.2) | Every adapter, both roles (TLS: opendnp3 and DSC go-dnp3) | yes |
| Security statistics, g121/g122 (1.2) | DSC go-dnp3, both roles | yes, DSC go-dnp3 against itself |
| Device attributes, g0 (1.3) | Served by the DSC go-dnp3 outstation; read and written by the DSC go-dnp3 and dnp3py masters | yes: visible string, unsigned and signed integer, and (1.5) float, octet string, bit string and time, read and written |
| Several controls in one request (1.3) | Sent by every master; the fixture's limit applied by the opendnp3 and DSC go-dnp3 outstations | yes |
| Freeze at time and analog freeze (1.3) | FREEZE AT TIME: served by the DSC go-dnp3 outstation, sent by the DSC go-dnp3 and dnp3py masters. Analog freeze: served by the DSC go-dnp3 outstation, sent by the opendnp3 and dnp3py masters | yes, with a time in the past and a repeating interval |
| Analog deadbands, g34 (1.3) | Write: served by the DSC go-dnp3 outstation, sent by the DSC go-dnp3 and dnp3py masters. Read: no adapter | write: yes |
| File append and authentication (1.3) | Served by the DSC go-dnp3 outstation; AUTHENTICATE_FILE sent by the DSC go-dnp3 and dnp3py masters; an append by the dnp3py master | yes, with the server's append event |
| Event buffer overflow (1.3) | Every outstation sets IIN2.3 | yes; dnp3py's outstation keeps IIN2.3 after the confirm (known bug) |
| Link-layer confirms and keep-alives (1.3) | Confirms: DSC go-dnp3, both roles. Keep-alives: sent by every master, reported by the DSC go-dnp3 outstation | yes; dnp3py's master does not acknowledge a confirmed frame (known bug) |
| Broadcast (1.3) | Sent by the DSC go-dnp3 and dnp3py masters; carried out by the opendnp3 and DSC go-dnp3 outstations | yes |
| Management functions (1.3) | Served by the DSC go-dnp3 outstation; sent by the opendnp3 and dnp3py masters | yes |
| ACTIVATE CONFIG (1.5) | Served by the DSC go-dnp3 outstation; sent by the dnp3py master; refused by the others | yes |
| Several outstations on one port (1.3) | opendnp3 and DSC go-dnp3 | yes |

## Not provided on purpose

Malformed frames, wrong CRCs, out-of-sequence fragments and precise timer
boundaries. A reference implementation is a correct peer, not a fault
injector; a library tests these with scripted peers of its own.

## Per adapter

| | opendnp3 | dscsystems | dnp3py |
|---|:---:|:---:|:---:|
| Outstation (server role) | yes | yes | yes (`dnp3py` fixture only) |
| Everything above except the rows below, as master | yes | yes | yes |
| Assign class, outstation | yes | yes | no |
| Assign class, master | yes | no | yes |
| All-objects read and restart delay, master | yes | no | yes |
| DIRECT OPERATE NO ACK, master | no | yes | yes |
| Freeze without acknowledgement, master | yes | no | yes |
| Time synchronization when the outstation does not ask for it | no | yes | yes |
| FREEZE AND CLEAR of a counter without frozen counter | yes | no | — |
| Frozen counter event for a freeze that changes no frozen value (a policy, not a requirement) | no | yes | — |
| Fixtures with gaps in indexes, or without NEED_TIME at start | yes | no | no |
| File transfer, attributes, deadband write, broadcast, master | no | yes | yes |
| File append, ACTIVATE CONFIG, select expiry and unconfirmed events, master | no | no | yes |

— : not tested.

The own go-dnp3 image (`own/go-dnp3`, adapter `otfabric`) runs the same
self-tests against the three reference stacks and itself, in both roles. It
is not a reference and not in these tables: see
[own/go-dnp3/README.md](own/go-dnp3/README.md).

The reasons are in [docs/CAPABILITIES.md](docs/CAPABILITIES.md) and
[docs/COMPATIBILITY.md](docs/COMPATIBILITY.md). Read them from the image
(`print-capabilities`) rather than from this table.
