# Compatibility

Which upstream versions the adapters are built on, what the reference stacks
were verified to do against each other, and what was found on the way.

Everything here is about the reference stacks. It makes no statement about
any library that consumes the images.

## Pinned upstreams

The rule is the latest upstream release. An adapter stays behind only for a
bug that removes coverage, and then the latest release is still built and
self-tested as a candidate.

| Adapter | Upstream | Pinned | Latest | Pin | Engine |
|---------|----------|--------|--------|-----|--------|
| `stepfunc` | [Step Function I/O dnp3](https://github.com/stepfunc/dnp3) | 1.8.0 | 1.8.0 | crate `dnp3 =1.8.0` from crates.io, checksum in `Cargo.lock` (tag commit `235533ae276cd8f33b669c6db395979a308ef848`) | independent, Rust |
| `opendnp3` | [opendnp3](https://github.com/dnp3/opendnp3) | 3.1.2 | 3.1.2 (archived) | commit `26b4c01e4839bbbda8866655e086471c4917ee53` | independent, C++ |
| `dnp3py` | [dnp3py](https://github.com/craigpnnl/dnp3py) (both roles; outstation on the dnp3py fixture) | 0.6.0 | 0.6.0 | PyPI wheel `dnp3py==0.6.0`, hash in `requirements.txt` (tag commit `c00d5df22e779df72f7caa4402c3f34957eb276d`) | independent, Python |
| `dscsystems` | [DSC Systems go-dnp3](https://github.com/dscsystems/go-dnp3) | v0.8.2 | v0.8.2 | Go module `github.com/dscsystems/go-dnp3@v0.8.2` (commit `1fcec1d7881541d794fe257d2ede3ba46587b500`), checksum in `go.sum` | independent, Go |

Everything else is on its latest release as of 2026-10-19: Debian 13
(trixie), Rust 1.99.1, Go 1.27.2, Python 3.14.8.

The pins live in the Dockerfiles, `adapters/stepfunc/Cargo.toml` and
`Cargo.lock` and `adapters/dscsystems/go.mod`, and are mirrored in [versions.yaml](../versions.yaml);
`scripts/check-versions.sh` keeps them in step. The **Upstream**
workflow (`.github/workflows/upstream.yml`, daily and on demand) resolves
the latest stable release of every stack with `scripts/latest-upstream.py`:
the dnp3 crate on crates.io, opendnp3's tags on GitHub, DSC go-dnp3 on the Go
module proxy and dnp3py on PyPI, and checks the digest of every pinned base
image. It builds the adapter on each release that differs from its pin (once
per pin and latest version), runs the smoke and cross-stack self-tests,
writes one pin-vs-latest table, a row per stack and per base image, to its
job summary, and keeps an issue open while a pin is behind. It never changes a pin. opendnp3's
repository is archived: commits after its last tag were never released and
are not candidates.

opendnp3's configure step downloads its own dependencies (asio 1.16.0 and two
Automatak helper libraries at fixed commits) and checks them against the SHA-1
hashes in its `deps/*.cmake`; they are recorded in `versions.yaml`.

## Cross-stack matrix

`make interop` runs every case of [tests/cases.tsv](../tests/cases.tsv) in
all pairings and requires, besides the expected outcome, that the result
documents of all pairings are identical apart from time stamps, header order
and the restart bit. Then it checks values: the integrity poll against the
fixture, the events controls and freezes cause, unsolicited responses, class
assignment and time synchronization. A case that needs a feature a pairing
does not declare is skipped for that pairing and counted as such.

| Client → Server | stepfunc | opendnp3 | dscsystems |
|-----------------|:--------:|:--------:|:----------:|
| **stepfunc** | pass ¹ ² | pass | pass ⁴ |
| **opendnp3** | pass ¹ ² | pass | pass ⁴ |
| **dscsystems** | pass ¹ ³ | pass ³ | pass ³ ⁴ ⁵ |
| **dnp3py** | pass ¹ ² ⁶ | pass ⁶ | pass ⁴ ⁶ |

¹ without class assignment: the stepfunc outstation does not implement it.
² the first select-before-operate on a freshly started stepfunc outstation
can fail (known bug below); select-before-operate after that is checked
separately and passes.
³ without all-objects reads, restart delays and class assignment, which the
DSC go-dnp3 master cannot send or report; with direct operate no ack, which it
can send.
⁴ with two known bugs of the DSC go-dnp3 outstation's freeze, below.
⁵ with the known address-filtering bug below.
⁶ with direct operate no ack and the freezes without acknowledgement, which
the dnp3py master sends. dnp3py has no server column: it cannot serve the
baseline. How the four outstations compare on the `dnp3py` fixture is below
([dnp3py's outstation: the dnp3py fixture](#dnp3pys-outstation-the-dnp3py-fixture)).

The numbers of a run (checks passed and failed, known-bug hits, skips, per
pairing and architecture) are not kept here: every release's qualification
runs the self-tests on the published images (and on a stepfunc image built
from the release tag, which is not published), natively on linux/amd64 and
linux/arm64, and attaches them as `compatibility-report.md` to the
[GitHub release](https://github.com/otfabric/dnp3-interop/releases).

The cross-stack self-test runs one suite per server adapter at the same time
(an outstation serves one master at a time, so the work is split by server)
and checks the agreement of the documents across pairings when all are done;
on a laptop the smoke and cross-stack tests together take about four minutes.

The untagged tip of opendnp3's archived repository (commit `c1dc7165`) was
built and self-tested once (Upstream candidates run 39339958041): it passes
the smoke test and the cross-stack self-test like 3.1.2. It is not a release,
so the pin stays on 3.1.2.

Covered by every pairing: integrity poll (values and flags against the
fixture), event polls per class, reads of every point type (all objects, a
range, a specific variation, class 0 by g60v1); wrong outstation address and
wrong master address (no answer); direct operate of g12v1 (LATCH_ON,
LATCH_OFF, CLOSE, TRIP) and g41v1..v4, select-before-operate of g12v1 and
g41v3, refusals (unknown index, unsupported code, direct operate of a
select-required point, a value out of range); the events controls produce,
in the classes of the fixture, once; a control that changes nothing produces
no event; LAN and non-LAN time synchronization and NEED_TIME; cold and warm
restart with the delay; immediate freeze and freeze-and-clear with their
events; enable and disable unsolicited, and buffered events delivered
unsolicited and not polled again; monitor.

Covered where declared: class assignment (stepfunc, opendnp3 and dnp3py
masters; opendnp3 and DSC go-dnp3 outstations); direct operate no ack (DSC go-dnp3
and dnp3py masters, all outstations); freezes without acknowledgement
(dnp3py master, all outstations); all-objects reads and restart delays
(stepfunc, opendnp3 and dnp3py masters).

## Contract 1.1 features

`scripts/features.sh` (`make features`) checks every pairing that declares a
feature: over TLS, UDP and serial an integrity poll against the fixture, a
control, its events and that confirmed events are gone; TLS refusals in both
directions; file reading (one block and several), listing, descriptors,
writing, reading back, deletion and a missing file, with the server's file
events and the same documents for every master; secure authentication with
the server's `auth` events, and refusal of a wrong update key, an unknown
user and a master without authentication, without the refused control
taking effect.

| Feature | Pairings (client → server) |
|---------|---------------------------|
| TLS | stepfunc, opendnp3, dscsystems → stepfunc, opendnp3, dscsystems (9) |
| UDP | the same 9 |
| Serial (socat pseudo-terminal line) | the same 9 |
| File transfer | stepfunc, dscsystems → dscsystems (2); stepfunc refuses DELETE_FILE; stepfunc and opendnp3 outstations refuse file transfer |
| Secure authentication v5 | dscsystems → dscsystems (1); no other stack has it |

dnp3py has TCP only and none of the 1.1 features.

### Contract 1.2 point types: one outstation, three masters

Only the DSC go-dnp3 outstation serves the extended fixture: frozen analog inputs,
command events of its outputs and g50v4. Its masters' counterparts:

| Objects | stepfunc master | opendnp3 master | DSC go-dnp3 master | dnp3py master |
|---------|:-:|:-:|:-:|:-:|
| g31/g33 frozen analogs | yes | no: the response fails to parse | yes | no |
| g13/g43 command events | yes | yes | yes | no |
| g50v4 time and interval | no: ignored | yes | no: dropped | no |

DSC go-dnp3 records a command event for every control that reaches its control
handler, with the status it answered: a value out of range produces a g43
with OUT_OF_RANGE. It records none for SELECT.

### Freeze at time and analog freeze

FREEZE AT TIME works with stepfunc's outstation (the adapter schedules what the
library hands over) and DSC go-dnp3's (the library schedules it), from the
stepfunc, DSC go-dnp3 and dnp3py masters; opendnp3's and dnp3py's outstations do
not support it and opendnp3's master cannot send it. DSC go-dnp3 differs in two
ways: time 0 with interval 0 is refused with PARAMETER_ERROR rather than
frozen at once (a known bug), and scheduled freezes run on a tick of the
session that serves a connection, so a freeze that comes due while no master
is connected runs when the next one is. Only DSC go-dnp3's outstation freezes
analog inputs (a g30 header in a freeze): the stepfunc, opendnp3 and dnp3py
masters send it, DSC go-dnp3's master cannot.

### Several outstations

opendnp3 (several stacks on one channel) and DSC go-dnp3 (its multidrop bus)
serve several outstations on one port, told apart by link address; every
master reaches each by its address. DSC go-dnp3's bus detaches every station when
a connection closes and starts routing as soon as the next connection is up,
before the stations' sessions have re-attached: a request that arrives first
would be unrouted and lost. The DSC go-dnp3 adapter holds the bus's first read of
each connection until every station is back. stepfunc's server picks an outstation per
connection by the master's IP address, so it cannot. A master that listens and
picks among outstations dialling in by link address is not in the contract:
of the references only stepfunc's master can do it.

### Management functions

Only DSC go-dnp3's outstation accepts the management functions (INITIALIZE DATA,
INITIALIZE/START/STOP APPLICATION, SAVE CONFIGURATION), and only opendnp3's
master sends them all. ACTIVATE CONFIG (31) is not in the contract, as no
reference master sends it; DSC go-dnp3 hands it to the same handler as the
others, so the dscsystems adapter refuses it there, and the library answers
that refusal with PARAMETER_ERROR rather than NO_FUNC_CODE_SUPPORT.

### Broadcast

Only DSC go-dnp3's master can send a broadcast (a DIRECT OPERATE NO ACK to
0xFFFF): stepfunc's master cannot address one, opendnp3's has no request that
expects no answer, and dnp3py's refuses the address. The stepfunc, opendnp3
and DSC go-dnp3 outstations carry it out and set IIN1.0 in their next response;
dnp3py's ignores it. DSC go-dnp3's master sends its start-up WRITE (clearing
IIN1.7 RESTART) to the broadcast address as well, and a DSC go-dnp3 master that
reads afterwards gets IIN1.0 in the response to that WRITE, not in the one it
reports.

### Link layer

Only DSC go-dnp3 sends confirmed user data, in both roles. The stepfunc, opendnp3
and dnp3py outstations and the stepfunc and opendnp3 masters acknowledge it;
dnp3py's master does not, so it gets no answer from an outstation that asks
for link-layer confirms (a known bug). The stepfunc, opendnp3 and DSC go-dnp3 masters
send keep-alives (REQUEST_LINK_STATUS) on an idle link; every outstation
answers them, and DSC go-dnp3's adapter reports each one it receives.

### File append and authentication

Only DSC go-dnp3's outstation serves files, so it is the only one to test
against. It appends and, given `--file-user`, requires AUTHENTICATE_FILE
before every open and delete. stepfunc's master appends and authenticates;
DSC go-dnp3's master authenticates but opens files for writing only. Both
masters get PERMISSION_DENIED without credentials and with a wrong password.

### Analog deadbands

The stepfunc and DSC go-dnp3 masters write deadbands (g34v3) to the stepfunc and
DSC go-dnp3 outstations, which then hold back events of a smaller change. Only
stepfunc serves a read of group 34 and decodes one; DSC go-dnp3's outstation
refuses it. stepfunc compares a change with the last value it reported, and
starts with none (the RESTART value): the first change after start makes an
event whatever the deadband. opendnp3's outstation refuses the write; dnp3py
has no deadbands.

### Several controls in one request

All four outstations carry out each control of a request on its own: in a
request with one refused control the others take effect. The stepfunc,
opendnp3 and DSC go-dnp3 outstations apply a limit the same way: the controls
within it are carried out, the rest answered TOO_MANY_OPS. dnp3py's ignores
the limit. stepfunc's master reports only the first refused control. DSC
go-dnp3's master sends the OPERATE after a SELECT echo that carried only some
of the controls, all SUCCESS; the other masters stop after the select.

### Event buffer overflow

All four outstations apply the fixture's `eventBufferSize` and set IIN2.3
EVENT_BUFFER_OVERFLOW when an event does not fit; the event is lost. They
count differently: stepfunc and opendnp3 per type, DSC go-dnp3 over all types
(one buffer), dnp3py per type and class, so a class holds three times the
size (FIXTURES.md). dnp3py 0.6.0 keeps IIN2.3 set after the master has
confirmed the events, where the others clear it (a known bug).

### Device attributes: stepfunc and DSC go-dnp3

Both outstations serve the attributes of a private set, accept writes of the
writable ones and refuse the others (stepfunc with NO_FUNC_CODE_SUPPORT for an
attribute that is not writable, PARAMETER_ERROR when its application refuses);
DSC go-dnp3 also derives the standard attributes of set 0 (point counts, fragment
sizes) itself. Both masters read and write them. Before 1.8.0, stepfunc's
master read a signed integer of one octet as unsigned: -42 arrived as 214.
Both outstations encode -42 in one octet, as the standard's shortest form.

### DSC go-dnp3: security statistics

With secure authentication on, the DSC go-dnp3 outstation serves 18 statistics
(g121v1, indexes 0..17, association 0, flags ONLINE) by range READ and raises
g122v2 events in a configurable class every n increments. Of the 18, it
counts 1, 2, 3, 5, 6, 8 to 14; 0, 4, 7 and 15 to 17 stay 0. Statistic 5
counts only the authentication responses it sends. Its master drops both
groups: the dscsystems adapter decodes them itself. No other image has
them, so they are checked DSC go-dnp3 against DSC go-dnp3 only.

### dnp3py's outstation: the dnp3py fixture

dnp3py's outstation serves a fixed layout: one static and one event variation
per type (g1v2/g2v1, g10v2, g20v1/g22v5, g21v1, g30v1/g32v1, g40v1 or v2),
binary inputs in class 1, analog inputs in class 2, counters in class 3, no
events of outputs and frozen counters (it would encode them as g2 and g22),
integer analog values, no double-bit inputs, octet strings or unsolicited
responses. Within that it agrees with the other three outstations on every
case of tests/cases-dnp3py.tsv, with two known bugs: a read of a range
returns every point of the type, and a control of a point that does not
exist is answered NOT_SUPPORTED without IIN2.2 (the others set
PARAMETER_ERROR). The adapter works around more: dnp3py's updates reset a
point's flags to ONLINE (the adapter passes them back), its analog output
tracking does the same (turned off; the adapter sets the value), it removes
events on READ before the confirm, it executes requests from any master
address (answering its configured master), and it does not tell its handler
the variation of an analog control or whether a direct operate expects a
response (the adapter reads both from the request).

With unsolicited responses off, the DSC go-dnp3 outstation answers requests from
any master address, to that address: a known bug in the dnp3py fixture runs.

### Dual end point: three stacks, both ways

The outstations of stepfunc (`spawn_outstation_tcp_client`,
`spawn_outstation_tls_client`), opendnp3 (a client channel; channels carry
either role) and DSC go-dnp3 (a client channel; sessions take any channel) dial
a listening master, and the masters of all three listen: stepfunc with
`spawn_master_tcp_server` (no TLS listener), opendnp3 with a server channel,
DSC go-dnp3 with `channel.TCPServer`/`TLSServer`. Every pairing works both ways
(`make features`, `[reverse]`). A dialling DSC go-dnp3 outstation may connect
twice to a DSC go-dnp3 master that listens: the first connection closes and it
redials; the operation succeeds.

### opendnp3: one UDP peer, no TLS name check

Its UDP channel binds and then connects to the remote endpoint, so an
outstation answers one master endpoint only and needs it up front
(`--udp-peer`); a master always sends from its `--udp-local-port`. Its TLS
master checks the outstation's certificate against the CA but not the name
in it (`--tls-server-name` has nothing to act on). Built against OpenSSL 3,
asio 1.16 warns about deprecated calls; TLS 1.2 and 1.3 both work.

### TLS is mutual everywhere, and a refused master learns it late

All three libraries require a client certificate on the outstation and
verify the outstation's on the master; none can switch verification off.
With TLS 1.3 the master finishes its side of the handshake before the
outstation checks the master's certificate, so a master the outstation does
not trust sees the connection close afterwards, and its error code differs
by library; the self-test checks only that the operation fails. A master that does not trust the outstation
fails to connect (`connect-failed`) with all three.

### DSC go-dnp3 (v0.8.2): its UDP outstation loses requests without a peer

Without a configured peer, the outstation's first unsolicited response fails
with "no UDP peer known yet". The session takes that for a lost connection
and reconnects in a loop, while the readers of the abandoned sessions keep
taking datagrams from the shared socket: requests go missing. The dscsystems
adapter drops such a datagram instead of returning the error (as UDP would
lose it), so the outstation works with or without `--udp-peer`. It also
answers the first endpoint it hears from for its whole life.

### DSC go-dnp3 and stepfunc: file transfer

The DSC go-dnp3 outstation serves a directory: entries report the file system's
sizes (a directory its block size, 4096) and modification times as creation
times. It handles one transfer at a time. The stepfunc master has
`read_file`, `read_directory`, `get_file_info` and open/write/close, but no
DELETE_FILE: `file-delete` is not among its operations. Both masters
produce the same listing and descriptors of the same outstation.

### DSC go-dnp3: secure authentication, against itself only

DSC go-dnp3 is the only stack in the images with SAv5, so it is checked against
itself, which proves the exchange is self-consistent, not that it
interoperates. Its MAC inputs agree with opendnp3's own SAv5 implementation
(removed in 2016; commit `22047af20^`, `cpp/libs/src/secauth/`): the
challenge-reply MAC is computed over the whole challenge fragment followed by
the whole critical request; the key-status MAC over the whole key change
request with the monitor-direction key. They differ in the key-wrap
plaintext: the opendnp3 master wraps the key-status object as received,
including its MAC value after a first key change, and its outstation compares
only the part before the MAC; the DSC go-dnp3 master leaves the MAC out, and its
outstation refuses more than seven octets after the object, so an
opendnp3-style master fails every key change after the first. DSC go-dnp3 fixes
the challenge at 32 octets and the session keys at 32 octets, has MAC
algorithm 4 only, no aggressive mode, does not authenticate unsolicited
responses, does not parse g120v7 in its master, and challenges every request
but READ, CONFIRM and DELAY MEASURE.

### Serial over a pseudo-terminal

The images carry socat. The outstation holds one end of a pseudo-terminal
pair for its life and each master's TCP connection is attached to the other
end (`docs/CONTAINER_CONTRACT.md`). A single pseudo-terminal per TCP
connection (socat's `fork` on the pty side) loses responses to the
lingering process of the previous connection.

## Findings

### opendnp3's master cannot synchronize time on request

opendnp3 3.1.2 runs the LAN or non-LAN procedure only as an automatic task,
when a response carries IIN1.4 NEED_TIME and `MasterParams.timeSyncMode`
names a procedure; the public API has no call for it and no way to write
g50v1 or g50v3. The adapter sets the procedure, sends a READ without object
headers to learn the IIN, and waits for the automatic task. Against an
outstation that does not set NEED_TIME it fails with `failed`; the capability
`timeSyncOnDemand` is false. The baseline fixture sets NEED_TIME at start for
this reason.

### Neither opendnp3's nor stepfunc's master sends DIRECT OPERATE NO ACK or the `_NR` freezes

Neither opendnp3's master nor stepfunc's has a direct-operate-no-ack mode,
and both send any other function code through a task that waits for a
response (`PerformFunction`, `send_and_expect_empty_response`), so a `_NR`
request would end in a timeout. Their `directOperateNoAckClient` and
`freezeNoAckClient` are false. The DSC go-dnp3 and dnp3py masters send them
(DSC go-dnp3 DIRECT OPERATE NO ACK only), so every outstation is tested with
them.

### Before 1.8.0, the first SELECT on a fresh stepfunc outstation failed with NO_SELECT

With unsolicited responses supported, the stepfunc outstation sends a null
unsolicited response as soon as a master connects. Both masters (stepfunc
and opendnp3) send their confirm of it only after the response to their
first request. When that request is a SELECT, the confirm arrives between
SELECT and OPERATE:

```text
TX UnsolicitedResponse seq 0 (null, CON)
RX Select seq 0
TX Response seq 0
RX Confirm (UNS) seq 0
RX Operate seq 1          -> "received OPERATE without prior SELECT", NO_SELECT
```

The outstation checks that OPERATE is the frame directly after SELECT
(`SelectState::match_operate` compares frame counters) and counts the
confirm as one. stepfunc keeps the "null response confirmed" state for the
life of the outstation, not per connection, so only the first SELECT after
start is affected. The opendnp3 outstation checks the sequence number only
and accepts the same exchange.

No adapter could avoid it without disabling unsolicited responses. stepfunc
1.8.0 fixes it: since dnp3-interop v0.5.2 it is no longer in
[tests/known-bugs.tsv](../tests/known-bugs.tsv), and the first SELECT after
start passes.

### Both outstations set PARAMETER_ERROR with NOT_SUPPORTED, and stepfunc's master drops the echo

When the control handler answers NOT_SUPPORTED, both the opendnp3 and the
stepfunc outstation set IIN2.2 PARAMETER_ERROR in the response. The stepfunc
master then fails the task with `RejectedByIin2` and does not report the
echoed status; the opendnp3 master reports both. The contract therefore
makes such a response `rejected` with `commandStatus` null for every adapter
(the opendnp3 adapter names the echo in the message). Refusals by status
alone, such as OUT_OF_RANGE, carry no IIN bit and are `command-failed` with
the status.

### opendnp3 has no update for frozen counters

`UpdateBuilder` has no `Update(FrozenCounter, ...)`: frozen counters change
only by a freeze. The adapter sets their initial value by writing it to the
counter of the same index, freezing that counter and restoring it, all with
`EventMode::Suppress`.

### FREEZE AND CLEAR clears counters without a frozen counter

opendnp3's internal freeze clears every selected counter, whether or not a
frozen counter with its index exists. That is what IEEE 1815 describes, and
the fixture behaviour says so; the stepfunc adapter, whose library leaves the
freeze to the application, does the same.

### opendnp3 needs `<cstdint>` with GCC 14

The 2022 sources use the fixed-width integer types without including
`<cstdint>`, which GCC 14 no longer provides through other headers. The
Dockerfile passes `-include cstdint`.

### stepfunc's outstation does not implement ASSIGN CLASS

It answers function code 22 with NO_FUNC_CODE_SUPPORT; `assignClassServer`
is false. Its master sends ASSIGN CLASS, and the self-test exercises that
against the opendnp3 outstation.

### stepfunc keeps octet strings out of class 0 by default

`ClassZeroConfig.octet_string` defaults to false; the adapter enables it, so
that both outstations answer an integrity poll with the same points.

### DSC go-dnp3: FREEZE AND CLEAR clears only counters that have a frozen counter

`Database.freezeRange` walks the indexes below the smaller of the number of
counters and of frozen counters, and clears only those. The baseline's
counter 2 has no frozen counter and keeps its value after FREEZE AND CLEAR;
opendnp3 and the stepfunc adapter clear it. Listed in
[tests/known-bugs.tsv](../tests/known-bugs.tsv).

### DSC go-dnp3: a freeze raises events for unchanged frozen values

A second freeze of a counter that has not changed raises a frozen counter
event again (the frozen value is the same; only its time differs).
opendnp3 raises none. Listed in the known bugs.

### DSC go-dnp3: link addresses are not filtered

Its master accepts frames addressed to another master address: configured
as master 7, it received the outstation's null unsolicited response meant for
master 1 and acted on its DEVICE_RESTART bit. Its outstation processes
requests from a master address other than its configured master's: it
accepted that master's WRITE clearing the restart bit, and answered it, to
master 1. Against the other stacks each half is harmless (they filter); DSC go-dnp3
against itself reads an IIN where every other pairing times out. Listed in
the known bugs.

### DSC go-dnp3 sets PARAMETER_ERROR only for a control of a point that does not exist

For a control the handler refuses with NOT_SUPPORTED on an existing point (an
unsupported code, a direct operate of a select-required output) the DSC go-dnp3
outstation answers with the status and no IIN bit; opendnp3 and stepfunc also
set PARAMETER_ERROR. The capability `parameterErrorWithNotSupported` says
which an outstation does, and the cases expect `command-failed` or
`rejected` accordingly.

### DSC go-dnp3 carries out a restart

A cold restart discards the unconfirmed events and asserts DEVICE_RESTART and
NEED_TIME again; a restart requested within the delay announced for the
previous one is answered with the time left and ALREADY_EXECUTING. The
restart cases therefore run on a fresh outstation for every adapter.

### DSC go-dnp3's master: ranges and classes only, no restart delay, no ASSIGN CLASS

`Session.ScanRange` reads start-stop ranges; there is no all-objects read of a
group other than the classes. `Session.Restart` returns no delay. There is no
call for ASSIGN CLASS. The capabilities `readAllObjectsClient`,
`restartDelayClient` and `assignClassClient` are false for it.

### DSC go-dnp3's master confirms before its first request

Unlike the stepfunc and opendnp3 masters, it confirms the outstation's null
unsolicited response before it sends its first request, so its first SELECT
on a fresh stepfunc outstation succeeded even before stepfunc 1.8.0.

### DSC go-dnp3's outstation: one connection at a time, contiguous indexes, NEED_TIME at start

Its TCP channel serves one connection and leaves the next one waiting in the
backlog until the first ends; the adapter puts its own listener in front, so
that a new connection replaces the current one as with the other stacks. Its
database holds the indexes 0..n-1 of each type, and it asserts NEED_TIME until
a master writes the time; the adapter refuses fixtures with gaps or with
`needTime` false (exit 4). It defers a READ until its null unsolicited
response is confirmed, as IEEE 1815 allows.

### dnp3py: a master library to build on, not a complete master

dnp3py 0.6.0's master has calls for integrity and class polls and for
unsolicited responses; it has none for range or all-objects reads, controls
other than g12v1 and g41v1, time synchronization, restarts, freezes or
ASSIGN CLASS, and its SOE handler receives neither the group and variation of
a value nor the status echo of a control. The adapter builds those requests
with dnp3py's application-layer types (`RequestHeader`, `ObjectHeader`,
`ObjectBlock`), sends them through its runner, and decodes responses with the
master's own decoders, which are module-private (`_decode_binary` and
others); the exact pin keeps them in place and the image build fails if they
move. Its master does not clear the outstation's restart bit, and the adapter
connects without a link reset (`LinkResetPolicy.NEVER`), so that connected
means the TCP connection as for every adapter.

Like the stepfunc and opendnp3 masters, it confirms the null unsolicited
response only after its first request's response, so it trips the stepfunc
outstation's first-SELECT bug every time.

### dnp3py's outstation cannot serve the baseline fixture

It has no double-bit inputs, octet strings or per-point variations (analog
inputs are always g30v1), no analog output events, and its TCP runner does
not send unsolicited responses. The adapter serves the `dnp3py` fixture
instead (see
[dnp3py's outstation: the dnp3py fixture](#dnp3pys-outstation-the-dnp3py-fixture)).

### DSC go-dnp3 refuses a READ without object headers

It answers PARAMETER_ERROR ("request carried no objects but requires them");
opendnp3 and stepfunc answer a null response. The opendnp3 adapter sends such
a READ to learn the IIN before its library's time synchronization; it judges
`time-sync` by the outcome of the synchronization itself, not by the IIN of
that READ, which the null unsolicited response can make the last one to
arrive.

### Masters close only after their last confirm

The adapters wait 200 ms after an operation before closing: the libraries
write the application confirm of the last response asynchronously, and an
immediate close occasionally lost it, so the outstation kept events the
master had already reported. The parallel self-test exposed this with
opendnp3 and DSC go-dnp3 masters.
