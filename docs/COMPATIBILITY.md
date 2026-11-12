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
| `opendnp3` | [opendnp3](https://github.com/dnp3/opendnp3) | 3.1.2 | 3.1.2 (archived) | commit `26b4c01e4839bbbda8866655e086471c4917ee53` | independent, C++ |
| `dnp3py` | [dnp3py](https://github.com/craigpnnl/dnp3py) (both roles; outstation on the dnp3py fixture) | 0.6.0 | 0.6.0 | PyPI wheel `dnp3py==0.6.0`, hash in `requirements.txt` (tag commit `c00d5df22e779df72f7caa4402c3f34957eb276d`) | independent, Python |
| `dscsystems` | [DSC Systems go-dnp3](https://github.com/dscsystems/go-dnp3) | v0.8.2 | v0.8.2 | Go module `github.com/dscsystems/go-dnp3@v0.8.2` (commit `1fcec1d7881541d794fe257d2ede3ba46587b500`), checksum in `go.sum` | independent, Go |
| `cbsigma` | [cB-Sigma dnp3](https://github.com/cB-Sigma/dnp3) | v0.3.1 | v0.3.1 | Go module `github.com/cB-Sigma/dnp3@v0.3.1` (commit `91f2f33c90e978e48bf67d20bca1022e769ed4d6`), checksum in `go.sum` | independent, Go |
| `nfmdnp3` | [nfm-dnp3](https://github.com/fxodell/dnp3py) (master only) | 1.0.1 | 1.0.1 | PyPI wheel `nfm-dnp3==1.0.1`, hash in `requirements.txt` (tag commit `8d1dc7f50d319a8dbb90e1c3091754d6ab02d1a6`) | independent, Python |

Everything else is on its latest release as of 2026-10-19: Debian 13
(trixie), Go 1.27.4, Python 3.14.10.

The pins live in the Dockerfiles, the adapters' `go.mod` and `requirements.txt`, and are mirrored in [versions.yaml](../versions.yaml);
`scripts/check-versions.sh` keeps them in step. The **Upstream**
workflow (`.github/workflows/upstream.yml`, daily and on demand) resolves
the latest stable release of every stack with `scripts/latest-upstream.py`:
opendnp3's tags on GitHub, DSC go-dnp3 and cB-Sigma dnp3 on the Go
module proxy and dnp3py and nfm-dnp3 on PyPI, and checks the digest of every pinned base
image. It builds the adapter on each release that differs from its pin (once
per pin and latest version), runs the smoke and cross-stack self-tests,
writes one pin-vs-latest table, a row per stack and per base image, to its
job summary, and keeps an issue open while a pin is behind. It never changes a pin. opendnp3's
repository is archived: commits after its last tag were never released and
are not candidates.

opendnp3's configure step downloads its own dependencies (asio 1.16.0 and two
Automatak helper libraries at fixed commits) and checks them against the SHA-1
hashes in its `deps/*.cmake`; they are recorded in `versions.yaml`.

## Protocol violations and implementation choices

Interoperability tests must distinguish protocol violations from
implementation choices permitted by the specification. A difference in
behavior is not automatically a defect.

So a check here is hard where the behaviour is required, and states the
accepted alternatives where stacks may legitimately differ. Only a violation
belongs in [tests/known-bugs.tsv](../tests/known-bugs.tsv). The freeze below is
the worked example.


`make interop` runs every case of [tests/cases.tsv](../tests/cases.tsv) in
all pairings and requires, besides the expected outcome, that the result
documents of all pairings are identical apart from time stamps, header order
and the restart bit. Then it checks values: the integrity poll against the
fixture, the events controls and freezes cause, unsolicited responses, class
assignment and time synchronization. A case that needs a feature a pairing
does not declare is skipped for that pairing and counted as such.

| Client → Server | opendnp3 | dscsystems | cbsigma |
|-----------------|:--------:|:----------:|:-------:|
| **opendnp3** | pass | pass ² | pass ⁵ |
| **dscsystems** | pass ¹ | pass ¹ ² ³ | pass ¹ |
| **dnp3py** | pass ⁴ | pass ² ⁴ | pass ⁴ |
| **cbsigma** | pass | pass ² | pass |
| **nfmdnp3** | known bug ⁶ | known bug ⁶ | known bug ⁶ |

¹ without all-objects reads, restart delays and class assignment, which the
DSC go-dnp3 master cannot send or report; with direct operate no ack, which it
can send.
² with a known bug of the DSC go-dnp3 outstation's FREEZE AND CLEAR, below.
³ with the known master-address bug below.
⁴ with direct operate no ack and the freezes without acknowledgement, which
the dnp3py master sends. dnp3py has no server column: it cannot serve the
baseline. How the outstations compare on the `dnp3py` fixture is below
([dnp3py's outstation: the dnp3py fixture](#dnp3pys-outstation-the-dnp3py-fixture)).
⁵ with the known bug of the opendnp3 master, which reads the coarse restart
delay (g52v1, in seconds) that the cB-Sigma dnp3 outstation sends as
milliseconds.
⁶ nfm-dnp3 1.0.1 sends the application confirm as CONFIRMED_USER_DATA without
a link reset, so it stalls against every outstation that asks for a
confirmation, as the baseline does with its null unsolicited response. On the
`dnp3py` fixture, which sends no unsolicited responses, it passes against
every outstation except for the defects below.

The numbers of a run (checks passed and failed, known-bug hits, skips, per
pairing and architecture) are not kept here: every release's qualification
runs the self-tests on the published images, natively on linux/amd64 and
linux/arm64, and attaches them as `compatibility-report.md` to the
[GitHub release](https://github.com/otfabric/dnp3-interop/releases).

OT Fabric's own image (`own/go-dnp3`) is built from the latest go-dnp3
release when its workflow runs, not from a version pinned here, and is in no
release, so no qualification runs it. Its workflow runs the same self-tests against the
reference images of the latest release. With go-dnp3 v1.11.0 as a sixth
stack, on linux/arm64 locally on 2026-11-12 against the five references of
this branch (v0.12.0): smoke 75 passed; cross-stack on the baseline 2559
passed, 164 known bugs, 124 skipped; on the dnp3py fixture 2522 passed, 38
known bugs, 140 skipped; features 2827 passed, 92 known bugs, 405 skipped;
none failed. Every known bug is one of the reference stacks'; one shows only
against the own image and is in `own/go-dnp3/known-bugs.tsv` (see
[Management functions](#management-functions)); go-dnp3's
[permitted differences](#ot-fabric-go-dnp3-permitted-differences) pass.

The cross-stack self-test runs one suite per server adapter at the same time
(an outstation serves one master at a time, so the work is split by server)
and checks the agreement of the documents across pairings when all are done;
on a laptop the smoke and cross-stack tests together take a few minutes.

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

Covered where declared: class assignment (opendnp3 and dnp3py
masters; opendnp3 and DSC go-dnp3 outstations); direct operate no ack (DSC go-dnp3
and dnp3py masters, all outstations); freezes without acknowledgement
(dnp3py master, all outstations); all-objects reads and restart delays
(opendnp3 and dnp3py masters).

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
| TLS | opendnp3, dscsystems → opendnp3, dscsystems (4) |
| UDP | the same 4 |
| Serial (socat pseudo-terminal line) | the same 4 |
| File transfer | dscsystems → dscsystems (1); the opendnp3 outstation refuses file transfer |
| Secure authentication v5 | dscsystems → dscsystems (1); no other stack has it |

dnp3py has TCP only and none of the 1.1 features.

### Contract 1.2 point types: one outstation, three masters

Only the DSC go-dnp3 outstation serves the extended fixture: frozen analog inputs,
command events of its outputs and g50v4. Its masters' counterparts:

| Objects | opendnp3 master | DSC go-dnp3 master | dnp3py master |
|---------|:-:|:-:|:-:|
| g31/g33 frozen analogs | no: the response fails to parse | yes | no |
| g13/g43 command events | yes | yes | no |
| g50v4 time and interval | yes | no: dropped | no |

DSC go-dnp3 records a command event for every control that reaches its control
handler, with the status it answered: a value out of range produces a g43
with OUT_OF_RANGE. It records none for SELECT.

### Freeze at time and analog freeze

FREEZE AT TIME works with DSC go-dnp3's outstation (the library schedules it),
from the DSC go-dnp3 and dnp3py masters; opendnp3's and dnp3py's outstations do
not support it and opendnp3's master cannot send it. Both are limits of the
libraries, not of the adapters: opendnp3's outstation dispatches no
FREEZE_AT_TIME or FREEZE_AT_TIME_NR and answers anything it does not list
with FUNC_NOT_SUPPORTED
([`OutstationContext.cpp#L631-L686`](https://github.com/dnp3/opendnp3/blob/26b4c01e4839bbbda8866655e086471c4917ee53/cpp/lib/src/outstation/OutstationContext.cpp#L631-L686)),
and dnp3py's outstation has no executor for it and answers
NO_FUNC_CODE_SUPPORT
([`outstation.py#L1108-L1149`](https://github.com/craigpnnl/dnp3py/blob/c00d5df22e779df72f7caa4402c3f34957eb276d/src/dnp3/outstation/outstation.py#L1108-L1149)).
The own go-dnp3 image (`own/go-dnp3`) serves it as well. DSC go-dnp3 differs in two
ways: time 0 with interval 0 is refused with PARAMETER_ERROR rather than
frozen at once (a known bug), and scheduled freezes run on a tick of the
session that serves a connection, so a freeze that comes due while no master
is connected runs when the next one is. Only DSC go-dnp3's outstation freezes
analog inputs (a g30 header in a freeze): the opendnp3 and dnp3py
masters send it, DSC go-dnp3's master cannot.

### Several outstations

opendnp3 (several stacks on one channel) and DSC go-dnp3 (its multidrop bus)
serve several outstations on one port, told apart by link address; every
master reaches each by its address. DSC go-dnp3's bus detaches every station when
a connection closes and starts routing as soon as the next connection is up,
before the stations' sessions have re-attached: a request that arrives first
would be unrouted and lost. The DSC go-dnp3 adapter holds the bus's first read of
each connection until every station is back. A master that listens and picks
among outstations dialling in by link address is not in the contract: no
reference master can do it.

### Management functions

Only DSC go-dnp3's outstation accepts the management functions (INITIALIZE DATA,
INITIALIZE/START/STOP APPLICATION, SAVE CONFIGURATION); opendnp3's master and
dnp3py's (requests built by the adapter) send them all. OT Fabric's own outstation
(`own/go-dnp3`, not a reference) accepts all five too, and answers SAVE
CONFIGURATION with a time delay object (g52v2), as IEEE 1815-2012 4.4.12 asks;
DSC go-dnp3 answers it with no object. opendnp3's master sends the five
through `PerformFunction`, whose task takes a response with objects as failed
([`EmptyResponseTask.cpp#L54-L58`](https://github.com/dnp3/opendnp3/blob/26b4c01e4839bbbda8866655e086471c4917ee53/cpp/lib/src/master/EmptyResponseTask.cpp#L54-L58)),
so `[manage] the five functions` fails from opendnp3 to the own image. That
row is in `own/go-dnp3/known-bugs.tsv`, which only the own image's runs read
(`EXTRA_KNOWN_BUGS`): a row of `tests/known-bugs.tsv` that no release run
reproduces would be stale. ACTIVATE CONFIG (31,
contract 1.5) is sent by dnp3py's master only (the adapter builds the g70v8
file specification; 31 is not in dnp3py's `FunctionCode`) and answered by DSC
go-dnp3's outstation only, through the library's `Config.ActivateConfig`,
with g91v1. dnp3py's parser cannot frame g91v1 ("unknown_width"), so the
activation status is not reported; the request succeeds by its IIN. Without
an `ActivateConfig` handler DSC go-dnp3 would hand 31 to the management
handler of 15..19 and answer PARAMETER_ERROR (v0.8.2
`outstation/services.go`, `onManagement`).

### Broadcast

DSC go-dnp3's and dnp3py's masters send a broadcast (a DIRECT OPERATE NO ACK to
0xFFFF); opendnp3's master has no direct-operate-no-ack mode. dnp3py's
`MasterConfig` takes 0xFFFF as the outstation address (it checks against
65535, not the unused `MAX_ADDRESS` 0xFFFE) and its runner's `send()` frames
to it. The opendnp3 and DSC go-dnp3 outstations carry
it out and set IIN1.0 in their next response;
dnp3py's ignores it. DSC go-dnp3's master sends its start-up WRITE (clearing
IIN1.7 RESTART) to the broadcast address as well, and a DSC go-dnp3 master that
reads afterwards gets IIN1.0 in the response to that WRITE, not in the one it
reports.

### Link layer

Only DSC go-dnp3 sends confirmed user data, in both roles. The opendnp3 and
dnp3py outstations and the opendnp3 master acknowledge it; dnp3py's master
does not, so it gets no answer from an outstation that asks for link-layer
confirms (a known bug: its runner's `_consume_frame` takes the user data of a
CONFIRMED_USER_DATA frame and sends no ACK,
[`master/tcp_runner.py#L725-L771`](https://github.com/craigpnnl/dnp3py/blob/c00d5df22e779df72f7caa4402c3f34957eb276d/src/dnp3/master/tcp_runner.py#L725-L771)). The opendnp3 and DSC go-dnp3 masters send keep-alives (REQUEST_LINK_STATUS) on an idle link, and so does
the dnp3py adapter, which writes the library's own frame on the runner's
channel (the runner never sends it). Every outstation answers them, and DSC
go-dnp3's adapter reports each one it receives.

### File append and authentication

Only DSC go-dnp3's outstation serves files, so it is the only one to test
against. It appends and, given `--file-user`, requires AUTHENTICATE_FILE
before every open and delete. DSC go-dnp3's master authenticates but opens
files for writing only; no other master has file transfer. It gets
PERMISSION_DENIED without credentials and with a wrong password.

### Analog deadbands

The DSC go-dnp3 master writes deadbands (g34v3) to the DSC go-dnp3
outstation, which then holds back events of a smaller change. DSC go-dnp3's
outstation refuses a read of group 34, and its master drops one. opendnp3's outstation refuses the write; dnp3py
has no deadbands.

### Several controls in one request

Every outstation carries out each control of a request on its own: in a
request with one refused control the others take effect. The opendnp3 and
DSC go-dnp3 outstations apply a limit the same way: the controls within it
are carried out, the rest answered TOO_MANY_OPS. dnp3py's ignores the limit.
DSC go-dnp3's master sends the OPERATE after a SELECT echo that carried only some
of the controls, all SUCCESS; the other masters stop after the select.

### Event buffer overflow

Every outstation applies the fixture's `eventBufferSize` and sets IIN2.3
EVENT_BUFFER_OVERFLOW when an event does not fit; the event is lost. They
count differently: opendnp3 per type, DSC go-dnp3 over all types
(one buffer), dnp3py per type and class, so a class holds three times the
size (FIXTURES.md). dnp3py 0.6.0 keeps IIN2.3 set after the master has
confirmed the events, where the others clear it (a known bug).

### Device attributes: DSC go-dnp3

The DSC go-dnp3 outstation serves the attributes of a private set, accepts
writes of the writable ones and refuses the others; it also derives the
standard attributes of set 0 (point counts, fragment sizes) itself. Its master
reads and writes them. The outstation encodes -42 in one octet, as the
standard's shortest form.

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
responses. Within that it agrees with the other outstations on every
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

### Dual end point: two stacks, both ways

The outstations of opendnp3 (a client channel; channels carry either role)
and DSC go-dnp3 (a client channel; sessions take any channel) dial a
listening master, and the masters of both listen: opendnp3 with a server
channel, DSC go-dnp3 with `channel.TCPServer`/`TLSServer`. Every pairing works both ways
(`make features`, `[reverse]`). A dialling DSC go-dnp3 outstation may connect
twice to a DSC go-dnp3 master that listens: the first connection closes and it
redials; the operation succeeds.

### opendnp3: one UDP peer, no TLS name check

Its UDP channel binds and then connects to the remote endpoint, so an
outstation answers one master endpoint only and needs it up front
(`--udp-peer`); a master always sends from its `--udp-local-port`. Its TLS
master checks the outstation's certificate against the CA but not the name
in it (`--tls-server-name` has nothing to act on): a certificate issued to
another name is accepted
([`TLSClient.cpp#L66-L69`](https://github.com/dnp3/opendnp3/blob/26b4c01e4839bbbda8866655e086471c4917ee53/cpp/lib/src/channel/tls/TLSClient.cpp#L66-L69)),
which `make features` shows (`[tls]`, listed in the known bugs). Built against OpenSSL 3,
asio 1.16 warns about deprecated calls; TLS 1.2 and 1.3 both work.

### TLS is mutual everywhere, and a refused master learns it late

Both libraries with TLS (opendnp3 and DSC go-dnp3) require a client
certificate on the outstation and verify the outstation's on the master; none can switch verification off.
With TLS 1.3 the master finishes its side of the handshake before the
outstation checks the master's certificate, so a master the outstation does
not trust sees the connection close afterwards, and its error code differs
by library; the self-test checks only that the operation fails. A master that does not trust the outstation
fails to connect (`connect-failed`) with both.

### DSC go-dnp3 (v0.8.2): its UDP outstation loses requests without a peer

Without a configured peer, the outstation's first unsolicited response fails
with "no UDP peer known yet". The session takes that for a lost connection
and reconnects in a loop, while the readers of the abandoned sessions keep
taking datagrams from the shared socket: requests go missing. The dscsystems
adapter drops such a datagram instead of returning the error (as UDP would
lose it), so the outstation works with or without `--udp-peer`. It also
answers the first endpoint it hears from for its whole life.

### DSC go-dnp3: file transfer

The DSC go-dnp3 outstation serves a directory: entries report the file system's
sizes (a directory its block size, 4096) and modification times as creation
times. It handles one transfer at a time. GET_FILE_INFO is answered with the
name the file handler gives, which for the library's directory handler is the
last path element (`readme.txt`), not the name of the request (`/readme.txt`)
that IEEE 1815-2012 4.4.18 asks for (`outstation/file.go`, `onGetFileInfo`):
a known difference (`dscsystems-file-info-base-name`) of the `[files]
file-info` check.

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

Contract 1.5 runs the serial checks a second time with 19200 baud, even
parity and two stop bits at both ends. A pseudo-terminal carries the octets
whatever the settings, so this checks that each adapter accepts and applies
them, not their timing. Seven data bits are not run: a DNP3 frame needs all
eight bits of each octet.

## Findings

### cB-Sigma dnp3 (v0.3.1)

The self-tests found four behaviours of v0.3.0 that differed from the other
stacks, each changed in v0.3.1 (cB-Sigma/dnp3#2 to #5, pull requests #6 to
#9); three were defects, and the third below was not. Their rows of v0.3.0
have left [tests/known-bugs.tsv](../tests/known-bugs.tsv):

- The outstation answered a request with the same sequence number and octets
  as the previous one from its cache, READ included and across connections,
  so a master that reconnected and polled again could get the old values and
  events already confirmed.
- FREEZE AND CLEAR cleared the counters without a counter event.
- GET_FILE_INFO answered with the name of the request (the full path), where
  DSC go-dnp3 answered with the last path element. v0.3.0 was right: IEEE
  1815-2012 4.4.18 has the filename offset, file name size, request ID and
  file name octets of the answer filled in with the same data as in the
  request, so the answer for `/readme.txt` names `/readme.txt`. The report
  was mistaken, and v0.3.1 now answers with the last path element
  (`readme.txt`, `outfile.go`, `fileInfo`). Since v0.12.0 the `[files]
  file-info` check asks for the name of the request, and the answer of v0.3.1
  is a known difference (`cbsigma-file-info-base-name`).
- After a keep-alive the master waited its response timeout before it started
  the keep-alive timer again, so keep-alives came at the interval plus that
  timeout.

v0.3.1 adds no API, so the adapter's capabilities are those of v0.3.0.

Two behaviours, unchanged in v0.3.1 and still rows of the known bugs, differ
from this contract and not, as far as could be established, from
IEEE 1815-2012: FREEZE AT TIME with a time already past is done at once
instead of refused, and the class 0 response with secure authentication
includes the g121 security statistics.

The outstation challenges with 4 octets of challenge data; the DSC go-dnp3
master accepts only 32 and so cannot operate it with secure authentication
(a known bug of that master, reported as dscsystems/go-dnp3#22 with the fix
in #23). The opendnp3 master reads the coarse restart
delay (g52v1, seconds) as milliseconds (`RestartOperationTask.cpp`).

### nfm-dnp3 (1.0.1)

A master over TCP. The self-tests found these defects, each reported upstream
(fxodell/dnp3py#1 to #10) and tracked here by an issue with the label
`upstream`: the application confirm is sent as CONFIRMED_USER_DATA without a
link reset; link frames without user data (ACK, NACK) fail the request;
qualifier 0x28 is read and written with a 1-octet count; received frames are
not checked against the master's address; DEVICE_RESTART is never cleared;
an unsolicited response can be taken as the answer to a request; the
broadcast addresses are refused as a destination; the CROB trip and close
codes are swapped; and several object sizes are wrong, with no decoders for
some event objects. The adapter works around none of them in the library: it
declares what cannot work false and the rest are known bugs.

### opendnp3's master cannot synchronize time on request

opendnp3 3.1.2 runs the LAN or non-LAN procedure only as an automatic task,
when a response carries IIN1.4 NEED_TIME and `MasterParams.timeSyncMode`
names a procedure; the public API has no call for it and no way to write
g50v1 or g50v3. The adapter sets the procedure, sends a READ without object
headers to learn the IIN, and waits for the automatic task. Against an
outstation that does not set NEED_TIME it fails with `failed`; the capability
`timeSyncOnDemand` is false. The baseline fixture sets NEED_TIME at start for
this reason.

### opendnp3's master waits for an answer to a `_NR` function

opendnp3's master has no direct-operate-no-ack mode and sends any other
function code through a task that waits for a response (`PerformFunction`),
so a `_NR` request ends in a response timeout: its `directOperateNoAckClient`
is false. For IMMED_FREEZE_NR and FREEZE_CLEAR_NR the adapter waits at most
1 s and takes that timeout as "sent" (`freezeNoAckClient`); the outstation's
frozen and cleared counters show that the request arrived (interop value
checks, the `freeze --no-ack` cases against all three outstations). The DSC
go-dnp3 and dnp3py masters send DIRECT OPERATE NO ACK as well.

### opendnp3's outstation sets PARAMETER_ERROR with NOT_SUPPORTED

When the control handler answers NOT_SUPPORTED, the opendnp3 outstation sets
IIN2.2 PARAMETER_ERROR in the response; the opendnp3 master reports both the
IIN and the echoed status. The contract makes such a response `rejected` with `commandStatus` null for every adapter
(the opendnp3 adapter names the echo in the message). Refusals by status
alone, such as OUT_OF_RANGE, carry no IIN bit and are `command-failed` with
the status.

### opendnp3's outstation loses a CONFIRM that arrives with another request

While the opendnp3 outstation sends a response, it keeps one received request
for later (`deferred`, a single slot, in
[`OutstationContext.cpp#L200-L204`](https://github.com/dnp3/opendnp3/blob/26b4c01e4839bbbda8866655e086471c4917ee53/cpp/lib/src/outstation/OutstationContext.cpp#L186-L195)).
A CONFIRM followed by another request before the transmission completes
fills that slot twice, and the CONFIRM is lost. When it is the confirm of
the null unsolicited response, the outstation stays waiting for it until
the confirm timeout ends, then repeats the null response. Only after that
does it send the events of a class the master has enabled meanwhile.

A master that confirms the null unsolicited response and sends its next
request at once can hit this. The DSC go-dnp3 master does: it confirms the
null response as it arrives, together with the response to its startup
WRITE, and then sends its first request. One cross-stack run on 2026-11-03
lost the confirm this way (dscsystems → opendnp3, "enabling class 1 delivers
its events unsolicited"). A raw master that sends the WRITE, the CONFIRM and
ENABLE_UNSOLICITED in one TCP segment gets the events only after the
confirm timeout, every time; sent one after the other, at once.

The repository is archived, so there is no upstream report: the fix, with a
unit test, is on a fork
([boeboe/opendnp3@fbe2051d](https://github.com/boeboe/opendnp3/commit/fbe2051d41f7bdf143c334994290fe7b664d3520)),
tracked in [#76](https://github.com/otfabric/dnp3-interop/issues/76). The unsolicited
checks of `scripts/interop.sh` start the outstation with
`--confirm-timeout-ms 1000`, so a lost confirm costs one second, which is
within the window in which the master collects the events.

### opendnp3 has no update for frozen counters

`UpdateBuilder` has no `Update(FrozenCounter, ...)`: frozen counters change
only by a freeze. The adapter sets their initial value by writing it to the
counter of the same index, freezing that counter and restoring it, all with
`EventMode::Suppress`.

### FREEZE AND CLEAR clears counters without a frozen counter

opendnp3's internal freeze clears every selected counter, whether or not a
frozen counter with its index exists. That is what IEEE 1815 describes, and
the fixture behaviour says so.

### opendnp3 needs `<cstdint>` with GCC 14

The 2022 sources use the fixed-width integer types without including
`<cstdint>`, which GCC 14 no longer provides through other headers. The
Dockerfile passes `-include cstdint`.

### DSC go-dnp3: FREEZE AND CLEAR clears only counters that have a frozen counter

`Database.freezeRange` walks the indexes below the smaller of the number of
counters and of frozen counters, and clears only those. The baseline's
counter 2 has no frozen counter and keeps its value after FREEZE AND CLEAR;
opendnp3 clears it. Listed in
[tests/known-bugs.tsv](../tests/known-bugs.tsv).

### Frozen counter events: a freeze semantic and an event policy

One view of a frozen counter event is that it reports a snapshot taken at a
freeze, not necessarily a change in value: a meter that recorded nothing in an
interval still took its reading. Others report a change. The stacks differ.

This section separates two things. An **interoperability contract** is what
this repository's tests require so that stacks work together; a stack that
breaks it fails the run. A **normative conformance requirement** is what IEEE
1815 requires. No item below has been verified against the text of the
standard, so nothing here is a statement of conformance.

**Mandatory interoperability contract, hard checks in `make interop` and
`make features`, for every pairing:**

- a freeze copies the counters into the frozen counters, and an event reports
  a frozen value that changed;
- a second freeze of unchanged counters keeps the frozen values correct and
  leaves the counters;
- a freeze after the counters changed updates the frozen values;
- FREEZE AND CLEAR freezes the value before it clears, clears the counters
  and produces their counter events;
- every event that is raised has the right index, value and flags, and a time
  stamp if its variation has one;
- a confirmed event is not sent again.
- a newer freeze sets the static time of freeze (g21v5), also when it raises no
  event.

**Implementation-specific, either accepted if consistent:**

- *Event-generation policy*: whether a freeze that leaves a frozen value as it
  was raises a frozen counter event (`frozenEventPolicies`).
- *Event-retention policy*: whether the outstation keeps all of a point's
  frozen counter events until a master confirms them, or only the most recent
  (`frozenEventRetention`).

An event is not an invalid duplicate merely because its value matches an
earlier one; a duplicate is an event sent again after it was confirmed. An
outstation with a setting is tested under both generation policies
(`server --frozen-event-policy`), one without under the policy it declares, and
every outstation must keep one policy across freezes and for every master. A
stack with either policy is not a bug, so neither belongs in
`tests/known-bugs.tsv` unless a real violation of the standard is shown.

Observed, stack by stack:

| Outstation | A freeze that changes no frozen value | Generation setting | Events not yet polled |
|------------|---------------------------------------|--------------------|-----------------------|
| DSC go-dnp3 v0.8.2 | Raises a frozen counter event: `setFrozen` is told a freeze produced the value ([outstation/database.go](https://github.com/dscsystems/go-dnp3/blob/1fcec1d7881541d794fe257d2ede3ba46587b500/outstation/database.go#L399-L403), where a comment gives this as the design) | none | all kept |
| opendnp3 3.1.2 | Raises none: the library's freeze detects the event ([FreezeRequestHandler.cpp](https://github.com/dnp3/opendnp3/blob/26b4c01e4839bbbda8866655e086471c4917ee53/cpp/lib/src/outstation/FreezeRequestHandler.cpp#L48-L60), default `EventMode::Detect`) | none: an application's `FreezeCounter` takes a mode, a master's freeze does not | all kept |
| dnp3py 0.6.0 | Raises no frozen counter events at all | none | n/a |

Up to v0.7.0 the first row was listed as a known bug of DSC go-dnp3
(`dscsystems-freeze-unchanged-events`). It is not one.

**Outstanding research item (not a blocker).** Accepting both generation
policies is an engineering decision about what these tests accept, and that
decision is closed. It says nothing about the standard: no clause of IEEE 1815
on this has been read, so nothing here says the standard permits or prohibits
either behaviour. What was checked on 2026-11-01:

| Source | Result |
|--------|--------|
| IEEE 1815-2012 | Not read: [IEEE](https://standards.ieee.org/ieee/1815/5414/) offers it by purchase or subscription, and lists no free access |
| DNP3 IED Certification Procedure 3.1 rev 1 (DNP Users Group) | The public [PDF](https://www.dnp.org/API/Evotiva-UserFiles/FileActionsServices/DownloadFile?ItemId=5008&ModuleId=9582&TabId=66) is 17 pages of front matter and contents; section 8.16.2 (frozen counters) is listed at page 91 and is not in it |
| [dnp3c](https://github.com/stepfunc/dnp3c/blob/881209943b670cc59b16301346dffa7d5523ab72/src/main/scala/io/stepfunc/dnp3c/conformance/v31r1/FrozenCounters.scala) 0.2.0, an independent implementation of that procedure | A secondary source. For a device with frozen counter events, it requires a g23 event of every point in the read after each freeze of 8.16.2.2.3 to 8.16.2.2.6. As dnp3c runs them on a device whose frozen values start apart from its counters, each of these freezes changes the frozen values (counts generated between freezes; a second FREEZE AND CLEAR takes nonzero to zero), so they do not separate the two policies. dnp3c forces no change before the first freeze; whether the procedure does is in the part that is not public |
| Comments of the stacks | DSC go-dnp3 gives its behaviour as a design choice, citing no clause. opendnp3 and dnp3py cite none on this |
| Public bulletins and vendor manuals found by search | Nothing on this. The manuals read list group 23 and its variations, not when an event is due or what is kept |

Still needed: the text of IEEE 1815 on frozen counter events and freezes (the
object library for group 23, and the freeze function codes), and section 8.16.2
of the full certification procedure. DNP Users Group membership gives access to
the protocol documents and technical bulletins.

The `counters` fixture's frozen counter events are g23v5 and g23v6: the
self-test (`make features`, `[counters]`) requires each to carry the time of
the freeze that raised it, read against the outstation's own clock, and, under
`always`, a later time at a later freeze, and g21v5 to carry the time of the
newest freeze under either policy. Its counters change by
`counter-increment`, so a freeze of a changed value is tested with a change
that is not a clear. The dnp3py outstation is reported as unsupported for
g23v5, g23v6 and the policy, with the reason, and tested for the change itself.

The baseline's frozen counter events are g23v1 and g23v2, which have no time
stamp, so there the time stamp is checked only for being absent.

### DSC go-dnp3: the outstation does not filter the master address

Its outstation processes requests from a master address other than its
configured master's and answers them, to that address: the stack checks the
destination and that the source is a valid address, not that it is the
configured master
([`internal/stack/stack.go#L305-L342`](https://github.com/dscsystems/go-dnp3/blob/1fcec1d7881541d794fe257d2ede3ba46587b500/internal/stack/stack.go#L305-L342)),
and the session answers to the request's source
([`outstation/session.go#L1709-L1716`](https://github.com/dscsystems/go-dnp3/blob/1fcec1d7881541d794fe257d2ede3ba46587b500/outstation/session.go#L1709-L1716)).
Its master drops frames addressed to another master address (v0.8.2:
`Stack.addressedToUs`), but the answer is addressed to the address it asked
from, so it takes it.
The other masters drop the answer, so DSC go-dnp3 against itself reads an IIN
where every other pairing times out. A control sent from another master
address is carried out whichever master sends it (`make features`,
`[address]`). Listed in the known bugs.

### DSC go-dnp3 sets PARAMETER_ERROR only for a control of a point that does not exist

For a control the handler refuses with NOT_SUPPORTED on an existing point (an
unsupported code, a direct operate of a select-required output) the DSC go-dnp3
outstation answers with the status and no IIN bit; opendnp3 also sets
PARAMETER_ERROR. The capability `parameterErrorWithNotSupported` says
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

Unlike the opendnp3 master, it confirms the outstation's null unsolicited
response before it sends its first request.

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

Like the opendnp3 master, it confirms the null unsolicited response only
after its first request's response.

### dnp3py's outstation cannot serve the baseline fixture

It has no double-bit inputs, octet strings or per-point variations (analog
inputs are always g30v1), no analog output events, and its TCP runner does
not send unsolicited responses. The adapter serves the `dnp3py` fixture
instead (see
[dnp3py's outstation: the dnp3py fixture](#dnp3pys-outstation-the-dnp3py-fixture)).

### DSC go-dnp3 refuses a READ without object headers

It answers PARAMETER_ERROR ("request carried no objects but requires them");
opendnp3 answers a null response. The opendnp3 adapter sends such
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

### OT Fabric go-dnp3: permitted differences

OT Fabric's own stack (`own/go-dnp3`, not a reference; see
[own/go-dnp3/README.md](../own/go-dnp3/README.md)) differs from the reference
outstations in two ways that the standard and the contract leave open. None
is a bug, and none is in `tests/known-bugs.tsv`: the checks accept both.

**Binary events with relative time while unsynchronized.** An outstation of
go-dnp3 v1.11.0 that requests time synchronization (`needTime` in the fixture,
which the adapter maps to `server.WithTimeSync`) counts its time as
unsynchronized until a master writes the time. A binary or double-bit input
event recorded in that time and read by a class poll goes out with relative
time under an unsynchronized CTO (g51v2, then g2v3 or g4v3) instead of the
point's event variation (`server/read.go`, `eventItems`), on purpose: g2v2
cannot say that its time is unsynchronized, and a class poll leaves the
variation to the outstation, so a master has to accept g2v3 after g51v2. A
read that names the variation (g2v2) gets it. The other outstations report
g2v2. The checks that poll binary input events before a time synchronization
(three value checks of `scripts/interop.sh`, five checks of
`scripts/features.sh`) accept the point's variation or the relative one
(`class_poll_objects` in `scripts/lib.sh`). The relative one must carry a time
between the control and the poll, with a second of slack. Result documents do
not show the CTO, so the g51v2 is not checked. The dnp3py master leaves the
time of a relative-time object null: such an object is taken at its value.

**The file name of GET_FILE_INFO** is no longer a difference. Since go-dnp3
v1.11.0 the File Descriptor (g70v7) of the answer carries the file name of the
request (`/readme.txt`), as IEEE 1815-2012 4.4.18 asks, and since v0.12.0 the
`[files] file-info` checks of `scripts/features.sh` ask for exactly that. The
DSC go-dnp3 and cB-Sigma v0.3.1 outstations answer with the last path element
(`readme.txt`), which are known differences in `tests/known-bugs.tsv`.

**The order of the attributes of a set.** The contract keeps the attributes in
the order they arrived and does not fix that order: go-dnp3 serves g0v254 from
the highest variation down, DSC go-dnp3 from the lowest up. The attribute
checks of `scripts/features.sh` compare the attributes by variation.

**Secure authentication, since go-dnp3 v1.11.0.** Two more behaviours follow
IEEE 1815-2012 where the reference outstations do not, and the `[sa]` checks
take both since v0.12.0. At every session key change the outstation reports
the Authentication Failures statistic as an event (g122v2, index 2), whatever
its threshold, as Table 7-6 asks; the `[sa]` checks leave g122 out of the
polls they compare. A session key status request for a user it does not know
is answered with the key status AUTH_FAIL (A.45.5), not with error 11, so
the check of the reported refusals asks for error 1 (the wrong update key)
only.

### dnp3py's outstation: master address, select expiry, event confirms

Three checks of `make features` fail against dnp3py's outstation and are
listed in the known bugs:

- `[address]`: a control sent from a master address other than the
  configured master's is carried out. The TCP runner checks a frame's
  destination, not its source, and learns the master address from the first
  frame
  ([`tcp_runner.py#L159-L168`](https://github.com/craigpnnl/dnp3py/blob/c00d5df22e779df72f7caa4402c3f34957eb276d/src/dnp3/outstation/tcp_runner.py#L159-L168)).
- `[select]`: an OPERATE after the select timer expired is answered
  NO_SELECT, not TIMEOUT. Expired selections are dropped before the OPERATE
  is checked, so it finds none
  ([`outstation.py#L2039-L2084`](https://github.com/craigpnnl/dnp3py/blob/c00d5df22e779df72f7caa4402c3f34957eb276d/src/dnp3/outstation/outstation.py#L2039-L2084)).
  The control is not carried out either way.
- `[confirm]`: events a master did not confirm are not sent again. Events
  are removed when the response is built, not on the confirm
  ([`outstation.py#L1172-L1176`](https://github.com/craigpnnl/dnp3py/blob/c00d5df22e779df72f7caa4402c3f34957eb276d/src/dnp3/outstation/outstation.py#L1172-L1176);
  upstream issues
  [#77](https://github.com/craigpnnl/dnp3py/issues/77) and
  [#187](https://github.com/craigpnnl/dnp3py/issues/187)).
