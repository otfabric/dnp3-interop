# Coverage

What a consumer can exercise against the published adapters today, by the
test categories of the plan. "Cross-stack" is what this repository's own
self-test covers between the reference stacks.

| Category | Available to a consumer | Cross-stack self-test |
|----------|-------------------------|-----------------------|
| TCP connection lifecycle | connect, disconnect; one master at a time, a new connection replaces the old; reconnect by restarting the server container | connect, disconnect |
| Link layer | Addressing (outstation 1024, master 1); frames from another master address or to another outstation address are not answered | wrong outstation address, wrong master address |
| Integrity poll | Class 1, 2, 3 and 0 in one READ | yes, values and flags against the fixture |
| Static data | Groups 1, 3, 10, 20, 21, 30, 40, 110 in the static variations of the fixture format, including the variations without flags and the 16-bit ones; all objects, ranges, and a specific variation | yes |
| Event data | Groups 2, 4, 11, 22, 23, 32, 42 with and without time, caused by controls and freezes, in classes 1..3; confirmed events are not sent again | yes |
| Controls | g12v1 (LATCH_ON, LATCH_OFF, CLOSE, TRIP) and g41v1..v4; DIRECT OPERATE and SELECT/OPERATE; refusals NOT_SUPPORTED and OUT_OF_RANGE; select-required points | yes |
| DIRECT OPERATE NO ACK | Served by both outstations | no: no master can send it |
| Time synchronization | LAN (RECORD CURRENT TIME, g50v3) and non-LAN (DELAY MEASURE, g50v1); NEED_TIME until the time is written; the written time in the server's event stream | yes |
| Restart | Cold and warm restart with the delay in g52v2; the outstation does not actually restart | yes |
| Unsolicited responses | Null unsolicited response at connection; ENABLE and DISABLE UNSOLICITED per class; buffered and new events sent unsolicited and confirmed | yes |
| Freeze | IMMEDIATE FREEZE and FREEZE AND CLEAR of g20v0; frozen counter and counter events | yes |
| Assign class | Served by the opendnp3 outstation; sent by both masters | yes, against the opendnp3 outstation |
| IIN | DEVICE_RESTART until cleared, NEED_TIME, class event bits, request errors | yes, without DEVICE_RESTART |
| File transfer (g70) | no | no |
| Secure authentication (g120) | no | no |
| UDP, TLS, serial | no | no |

## Not provided on purpose

Malformed frames, wrong CRCs, out-of-sequence fragments and precise timer
boundaries. A reference implementation is a correct peer, not a fault
injector; a library tests these with scripted peers of its own.

## Per adapter

| | stepfunc | opendnp3 |
|---|:---:|:---:|
| Everything above except the rows below | yes | yes |
| Assign class, outstation | no | yes |
| Time synchronization when the outstation does not ask for it | yes | no |

The reasons are in [docs/CAPABILITIES.md](docs/CAPABILITIES.md) and
[docs/COMPATIBILITY.md](docs/COMPATIBILITY.md). Read them from the image
(`print-capabilities`) rather than from this table.
