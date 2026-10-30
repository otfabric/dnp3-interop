# Coverage

What a consumer can exercise against the published adapters today.
"Cross-stack" is what this repository's own self-tests (`make interop`,
`make features`) cover between the reference stacks.

| Category | Available to a consumer | Cross-stack self-test |
|----------|-------------------------|-----------------------|
| TCP connection lifecycle | connect, disconnect; one master at a time, a new connection replaces the old; reconnect by restarting the server container | connect, disconnect |
| Link layer | Addressing (outstation 1024, master 1); frames from another master address or to another outstation address are not answered | wrong outstation address, wrong master address (not by the DSC go-dnp3 outstation and master: known bug, see [docs/COMPATIBILITY.md](docs/COMPATIBILITY.md)) |
| Integrity poll | Class 1, 2, 3 and 0 in one READ | yes, values and flags against the fixture |
| Static data | Groups 1, 3, 10, 20, 21, 30, 40, 110 in the static variations of the fixture format, including the variations without flags and the 16-bit ones; all objects, ranges, and a specific variation | yes |
| Event data | Groups 2, 4, 11, 22, 23, 32, 42 with and without time, caused by controls and freezes, in classes 1..3; confirmed events are not sent again | yes |
| Controls | g12v1 (LATCH_ON, LATCH_OFF, CLOSE, TRIP) and g41v1..v4; DIRECT OPERATE and SELECT/OPERATE; refusals NOT_SUPPORTED (with PARAMETER_ERROR) and OUT_OF_RANGE; select-required points | yes |
| DIRECT OPERATE NO ACK | Served by every outstation; sent by the stepfunc, DSC go-dnp3 and dnp3py masters | yes |
| Freeze without acknowledgement | Served by every outstation; sent by the dnp3py master | yes |
| Time synchronization | LAN (RECORD CURRENT TIME, g50v3) and non-LAN (DELAY MEASURE, g50v1); NEED_TIME until the time is written; the written time in the server's event stream | yes |
| Restart | Cold and warm restart with the delay in g52v2; the outstation does not actually restart | yes |
| Unsolicited responses | Null unsolicited response at connection; ENABLE and DISABLE UNSOLICITED per class; buffered and new events sent unsolicited and confirmed | yes |
| Freeze | IMMEDIATE FREEZE and FREEZE AND CLEAR of g20v0; frozen counter and counter events | yes |
| Assign class | Served by the stepfunc, opendnp3 and DSC go-dnp3 outstations; sent by the stepfunc, opendnp3 and dnp3py masters | yes, where declared |
| IIN | DEVICE_RESTART until cleared, NEED_TIME, class event bits, request errors | yes, without DEVICE_RESTART |
| UDP, TLS, serial (1.1) | stepfunc, opendnp3 and DSC go-dnp3, both roles | yes |
| File transfer, g70 (1.1) | Served by the DSC go-dnp3 outstation; sent by the stepfunc (no delete) and DSC go-dnp3 masters | yes |
| Secure authentication v5, g120 (1.1) | DSC go-dnp3, both roles | yes, DSC go-dnp3 against itself |
| Frozen analog inputs, command events, time and interval (1.2) | Served by the DSC go-dnp3 outstation (`extended` fixture); reported by the masters that declare them (g31/g33: stepfunc, DSC go-dnp3; g13/g43: stepfunc, opendnp3, DSC go-dnp3; g50v4: opendnp3) | yes |
| Dual end point (1.2) | stepfunc, opendnp3 and DSC go-dnp3, both roles; stepfunc's master listens over TCP only | yes |
| Security statistics, g121/g122 (1.2) | DSC go-dnp3, both roles | yes, DSC go-dnp3 against itself |
| Device attributes, g0 (1.3) | stepfunc and DSC go-dnp3, both roles | yes |
| Several controls in one request (1.3) | Sent by every master; the fixture's limit applied by the stepfunc, opendnp3 and DSC go-dnp3 outstations | yes |
| Freeze at time and analog freeze (1.3) | FREEZE AT TIME: served by the stepfunc and DSC go-dnp3 outstations, sent by the stepfunc, DSC go-dnp3 and dnp3py masters. Analog freeze: served by the DSC go-dnp3 outstation, sent by the stepfunc, opendnp3 and dnp3py masters | yes |
| Analog deadbands, g34 (1.3) | Write: stepfunc and DSC go-dnp3, both roles. Read: stepfunc, both roles | yes |
| File append and authentication (1.3) | Served by the DSC go-dnp3 outstation; append sent by the stepfunc master, AUTHENTICATE_FILE by the stepfunc and DSC go-dnp3 masters | yes |
| Event buffer overflow (1.3) | Every outstation sets IIN2.3 | yes; dnp3py's outstation keeps IIN2.3 after the confirm (known bug) |
| Link-layer confirms and keep-alives (1.3) | Confirms: DSC go-dnp3, both roles. Keep-alives: sent by the stepfunc, opendnp3 and DSC go-dnp3 masters, reported by the DSC go-dnp3 outstation | yes |
| Broadcast (1.3) | Sent by the DSC go-dnp3 master; carried out by the stepfunc, opendnp3 and DSC go-dnp3 outstations | yes |
| Management functions (1.3) | Served by the DSC go-dnp3 outstation; sent by the opendnp3 master | yes |
| Several outstations on one port (1.3) | opendnp3 and DSC go-dnp3 | yes |

## Not provided on purpose

Malformed frames, wrong CRCs, out-of-sequence fragments and precise timer
boundaries. A reference implementation is a correct peer, not a fault
injector; a library tests these with scripted peers of its own.

## Per adapter

| | stepfunc | opendnp3 | dscsystems | dnp3py |
|---|:---:|:---:|:---:|:---:|
| Outstation (server role) | yes | yes | yes | yes (`dnp3py` fixture only) |
| Everything above except the rows below, as master | yes | yes | yes | yes |
| Assign class, outstation | yes | yes | yes | no |
| Assign class, master | yes | yes | no | yes |
| All-objects read and restart delay, master | yes | yes | no | yes |
| DIRECT OPERATE NO ACK, master | yes | no | yes | yes |
| Freeze without acknowledgement, master | no | no | no | yes |
| SELECT as the first request after the outstation started | no | yes | yes | — |
| Time synchronization when the outstation does not ask for it | yes | no | yes | yes |
| FREEZE AND CLEAR of a counter without frozen counter | yes | yes | no | — |
| Fixtures with gaps in indexes, or without NEED_TIME at start | yes | yes | no | no |

— : not tested.

The reasons are in [docs/CAPABILITIES.md](docs/CAPABILITIES.md) and
[docs/COMPATIBILITY.md](docs/COMPATIBILITY.md). Read them from the image
(`print-capabilities`) rather than from this table.
