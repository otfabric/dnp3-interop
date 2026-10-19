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

Everything else is on its latest release as of 2026-10-19: Debian 13
(trixie), Rust 1.99.1.

The pins live in the Dockerfiles and `adapters/stepfunc/Cargo.toml` and
`Cargo.lock`, and are mirrored in [versions.yaml](../versions.yaml);
`scripts/check-versions.sh` keeps them in step. The **Upstream candidates**
workflow (`.github/workflows/candidate.yml`, weekly and on demand) resolves
the latest dnp3 crate, builds the stepfunc adapter on it when it differs,
runs the smoke and cross-stack self-tests and writes a pin-vs-latest decision
table to its job summary. It never changes a pin. opendnp3's repository is
archived, so there is nothing to probe.

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

| Client → Server | stepfunc | opendnp3 |
|-----------------|:--------:|:--------:|
| **stepfunc** | not yet run | not yet run |
| **opendnp3** | not yet run | not yet run ¹ |

¹ The expectations in `tests/cases.tsv` were generated from a native build of
opendnp3 3.1.2 (macOS, clang) in both roles and checked against
[FIXTURES.md](FIXTURES.md); the containerised run is still to be done.

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

### opendnp3's outstation sets an IIN2 error bit with NOT_SUPPORTED

When its control handler answers NOT_SUPPORTED, the opendnp3 outstation also
sets an IIN2 error bit in the response. The contract therefore gives an
echoed command status precedence over the IIN: the error is
`command-failed`, not `rejected`.

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
