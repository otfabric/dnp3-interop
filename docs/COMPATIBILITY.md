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
| `stepfunc` | [Step Function I/O dnp3](https://github.com/stepfunc/dnp3) | 1.7.0 | 1.7.0 | crate `dnp3 =1.7.0` from crates.io, checksum in `Cargo.lock` (tag commit `d7ccb9f6c0a8dc2900e90ca8d0a9a9d5336bb8ca`) | independent, Rust |
| `opendnp3` | [opendnp3](https://github.com/dnp3/opendnp3) | 3.1.2 | 3.1.2 (archived) | commit `26b4c01e4839bbbda8866655e086471c4917ee53` | independent, C++ |
| `dnp3py` | [dnp3py](https://github.com/craigpnnl/dnp3py) (client role) | 0.6.0 | 0.6.0 | PyPI wheel `dnp3py==0.6.0`, hash in `requirements.txt` (tag commit `c00d5df22e779df72f7caa4402c3f34957eb276d`) | independent, Python |
| `dscsystems` | [DSC Systems go-dnp3](https://github.com/dscsystems/go-dnp3) | v0.8.1 | v0.8.1 | Go module `github.com/dscsystems/go-dnp3@v0.8.1` (commit `70ff72bb09755db183d1bd69859adc72cd663aec`), checksum in `go.sum` | independent, Go |

Everything else is on its latest release as of 2026-10-19: Debian 13
(trixie), Rust 1.99.1, Go 1.27.2, Python 3.14.8.

The pins live in the Dockerfiles, `adapters/stepfunc/Cargo.toml` and
`Cargo.lock` and `adapters/dscsystems/go.mod`, and are mirrored in [versions.yaml](../versions.yaml);
`scripts/check-versions.sh` keeps them in step. The **Upstream candidates**
workflow (`.github/workflows/candidate.yml`, weekly and on demand) resolves
the latest release of every stack: the dnp3 crate on crates.io, opendnp3's
latest tag on GitHub, go-dnp3 on the Go module proxy and dnp3py on PyPI. It
builds the adapter on each that differs from its pin, runs the smoke and
cross-stack self-tests and writes one pin-vs-latest decision table, a row per
stack, to its job summary. It never changes a pin. opendnp3's repository is
archived and its default branch has commits after 3.1.2 that were never
tagged; the workflow builds and self-tests that tip as information, but the
pin follows releases.

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
go-dnp3 master cannot send or report; with direct operate no ack, which it
can send.
⁴ with two known bugs of the go-dnp3 outstation's freeze, below.
⁵ with the known address-filtering bug below.
⁶ with direct operate no ack and the freezes without acknowledgement, which
the dnp3py master sends. dnp3py has no server column: it is a master only.

Verified on linux/arm64 with the pins above on 2026-10-20: 48 cases, the
value checks over 12 pairings; 1114 checks passed, none failed, 19 known-bug
hits, 117 skipped (a capability a pairing does not declare; pairings with
dnp3py as the server are not counted, it has no server role). The known-bug
hits: the first SELECT on a fresh stepfunc outstation, 12 (stepfunc, opendnp3
and dnp3py masters; for the first two it depends on timing, so the count
varies between runs); the go-dnp3 freeze, 6; go-dnp3 against itself with a
wrong master address, 1. The 484 documents the run kept validate against
`schemas/`. `make smoke`: 32 of 32 checks for each server adapter, 28 of 28
for dnp3py.

The same on GitHub Actions, natively on both architectures (CI runs
39336826490 and 39339934244): every smoke check passed for all four adapters,
and the cross-stack self-test had no failure on linux/amd64 or linux/arm64.

The cross-stack self-test runs one suite per server adapter at the same time
(an outstation serves one master at a time, so the work is split by server)
and checks the agreement of the documents across pairings when all are done;
on a laptop the smoke and cross-stack tests together take about four minutes.

The untagged tip of opendnp3's archived repository (commit `c1dc7165`) passes
the smoke test and the cross-stack self-test like 3.1.2 (Upstream candidates
run 39339958041); the pin stays on the release.

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
masters; opendnp3 and go-dnp3 outstations); direct operate no ack (go-dnp3
and dnp3py masters, all outstations); freezes without acknowledgement
(dnp3py master, all outstations); all-objects reads and restart delays
(stepfunc, opendnp3 and dnp3py masters).

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

### No master here sends DIRECT OPERATE NO ACK or the `_NR` freezes

Neither opendnp3's master nor stepfunc's has a direct-operate-no-ack mode,
and both send any other function code through a task that waits for a
response (`PerformFunction`, `send_and_expect_empty_response`), so a `_NR`
request would end in a timeout. Both outstations handle these requests, but
nothing in the repository can send them yet; the features are declared false
on both sides.

### The first SELECT on a fresh stepfunc outstation fails with NO_SELECT

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

No adapter can avoid it without disabling unsolicited responses. It is listed
in [tests/known-bugs.tsv](../tests/known-bugs.tsv) for the stepfunc outstation
at 1.7.0, and `scripts/interop.sh` checks select-before-operate on that
outstation after the null response was confirmed, where it passes. A library
that tests against the stepfunc image should not make its first request
after the container start a SELECT, or should expect this.

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

### go-dnp3: FREEZE AND CLEAR clears only counters that have a frozen counter

`Database.freezeRange` walks the indexes below the smaller of the number of
counters and of frozen counters, and clears only those. The baseline's
counter 2 has no frozen counter and keeps its value after FREEZE AND CLEAR;
opendnp3 and the stepfunc adapter clear it. Listed in
[tests/known-bugs.tsv](../tests/known-bugs.tsv).

### go-dnp3: a freeze raises events for unchanged frozen values

A second freeze of a counter that has not changed raises a frozen counter
event again (the frozen value is the same; only its time differs).
opendnp3 raises none. Listed in the known bugs.

### go-dnp3: link addresses are not filtered

Its master accepts frames addressed to another master address: configured
as master 7, it received the outstation's null unsolicited response meant for
master 1 and acted on its DEVICE_RESTART bit. Its outstation processes
requests from a master address other than its configured master's: it
accepted that master's WRITE clearing the restart bit, and answered it, to
master 1. Against the other stacks each half is harmless (they filter); go-dnp3
against itself reads an IIN where every other pairing times out. Listed in
the known bugs.

### go-dnp3 sets PARAMETER_ERROR only for a control of a point that does not exist

For a control the handler refuses with NOT_SUPPORTED on an existing point (an
unsupported code, a direct operate of a select-required output) the go-dnp3
outstation answers with the status and no IIN bit; opendnp3 and stepfunc also
set PARAMETER_ERROR. The capability `parameterErrorWithNotSupported` says
which an outstation does, and the cases expect `command-failed` or
`rejected` accordingly.

### go-dnp3 carries out a restart

A cold restart discards the unconfirmed events and asserts DEVICE_RESTART and
NEED_TIME again; a restart requested within the delay announced for the
previous one is answered with the time left and ALREADY_EXECUTING. The
restart cases therefore run on a fresh outstation for every adapter.

### go-dnp3's master: ranges and classes only, no restart delay, no ASSIGN CLASS

`Session.ScanRange` reads start-stop ranges; there is no all-objects read of a
group other than the classes. `Session.Restart` returns no delay. There is no
call for ASSIGN CLASS. The capabilities `readAllObjectsClient`,
`restartDelayClient` and `assignClassClient` are false for it.

### go-dnp3's master confirms before its first request

Unlike the stepfunc and opendnp3 masters, it confirms the outstation's null
unsolicited response before it sends its first request, so its first SELECT
on a fresh stepfunc outstation succeeds.

### go-dnp3's outstation: one connection at a time, contiguous indexes, NEED_TIME at start

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
not appear to send unsolicited responses. The adapter has the client role
only; serving dnp3py would need a second fixture within those limits.
