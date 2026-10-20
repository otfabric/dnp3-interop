# Plan

Delivery in phases, each with an acceptance gate. Status as of 2026-10-20.

| Phase | Scope | Status |
|-------|-------|--------|
| 1 | Foundation: contract, fixture format, validation, version manifest, tooling | done |
| 2 | opendnp3 adapter, client and server | done |
| 3 | stepfunc adapter; cross-stack self-test | done |
| 4 | First published images; consumption from a library's tests | v0.1.0 being released |
| 5 | dnp3py, DSC Systems go-dnp3, Triangle MicroWorks adapters | go-dnp3 done; dnp3py done as master; Triangle MicroWorks on hold |
| 6 | Expanded coverage: UDP, TLS, serial, file transfer, secure authentication | not started |
| 7 | Release qualification | workflow written, not exercised |

## Phase 1 - Foundation (done)

- Container contract 1.0 ([docs/CONTAINER_CONTRACT.md](docs/CONTAINER_CONTRACT.md)), schemas for the capability and client result documents
- Fixture format 1.0 with schema, cross-field validation and the baseline fixture ([docs/FIXTURES.md](docs/FIXTURES.md))
- `versions.yaml` and a check that it matches the build files
- Makefile, `compose.yaml`, smoke test, cross-stack self-test with its cases, CI, release and candidate workflows

**Gate:** fixtures validate and the repository builds with no reference to any consumer. Met: `make validate` passes the fixture checks; the only reference to a consumer is in the documentation.

## Phase 2 - opendnp3 (done)

- C++ adapter on opendnp3 3.1.2 (archived; its last release), pinned by commit, both roles, all eleven client operations.
- The master cannot send DIRECT OPERATE NO ACK or the `_NR` freezes and synchronizes time only on NEED_TIME: declared in its capabilities.

**Gate:** client and server operate against each other using opendnp3 alone (`make smoke-opendnp3`, and the self-test with one adapter).

Met: `make smoke-opendnp3` passes 32 of 32 checks; opendnp3 against itself passes every case and value check of `make interop`.

## Phase 3 - stepfunc (done)

- Rust adapter on the dnp3 crate 1.7.0, both roles, all eleven client operations.
- Its outstation refuses ASSIGN CLASS; its master cannot send DIRECT OPERATE NO ACK or the `_NR` freezes: declared in its capabilities.
- Cross-stack self-test over all four pairings: expected outcome per case, identical result documents across pairings, and the value checks of `scripts/interop.sh`.

**Gate:** both stacks satisfy the baseline contract in every direction (`make smoke-stepfunc`, `make interop`).

Met on linux/arm64 (2026-10-19): `make smoke-stepfunc` 32 of 32; `make interop` 344 checks passed, none failed, 8 known-bug hits, 6 skipped for undeclared capabilities. The known bug is the stepfunc outstation's first SELECT after start, when the master's confirm of the null unsolicited response arrives between SELECT and OPERATE; select-before-operate is checked separately after that and passes. Details in [docs/COMPATIBILITY.md](docs/COMPATIBILITY.md). Since run on linux/amd64 and linux/arm64 by CI as well.

## Phase 4 - First published images

- `v0.1.0` through the release workflow: linux/amd64 and linux/arm64, both images to GHCR, `manifest.json` with the digests.
- A library pins those digests and runs its tests against them without a checkout of this repository.

The stepfunc image is published although its upstream licence is
non-commercial: the images exist for interoperability testing only, which the
owner of this repository decided is the purpose they are published for. The
licence travels with the image (`/licenses/dnp3/LICENSE.txt`) and is stated
in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

## Phase 5 - Additional adapters

### DSC Systems go-dnp3 (done)

Evaluation: both roles, v0.8.1 (GPL-3.0), an engine of its own; pure Go, so
both architectures build natively. The adapter is GPL-3.0-or-later.

- Go adapter, both roles, ten client operations (no `assign-class`). Its
  master is the one that sends DIRECT OPERATE NO ACK, which makes
  `directOperateNoAckServer` testable and now declared for every outstation.
- Declared false: all-objects reads and restart delays (master), ASSIGN
  CLASS (master), PARAMETER_ERROR with NOT_SUPPORTED (outstation), file
  transfer and secure authentication (not in the contract yet).
- The outstation refuses fixtures with gaps in the indexes or without
  NEED_TIME at start; the adapter's listener lets a new connection replace
  the current one, as the contract requires.

**Gate:** smoke test and cross-stack self-test for the roles it declares. Met
on linux/arm64 (2026-10-20): 32 of 32 smoke checks; the three-adapter run has
789 checks passed, none failed, 14 known-bug hits, 81 skipped. Three upstream
bugs of go-dnp3 v0.8.1 are listed in `tests/known-bugs.tsv` (FREEZE AND CLEAR
of a counter without frozen counter, freeze events for unchanged values, link
address filtering). Details in [docs/COMPATIBILITY.md](docs/COMPATIBILITY.md).

### dnp3py (done as master)

Pin dnp3py 0.6.0 (PyPI, commit `c00d5df2`, MIT). Client role only: its
outstation cannot serve the baseline fixture (no double-bit inputs, octet
strings or per-point variations; analog inputs always g30v1; no analog output
events; the TCP runner does not send unsolicited responses). Serving it would
need a second fixture within those limits and cases for it; that is open.

The master runs dnp3py's link, transport and application layers and its
decoders; the requests its master has no call for (all-objects and range
reads, controls of every g41 variation, time synchronization, restart, ASSIGN
CLASS, freezes with and without acknowledgement, DIRECT OPERATE NO ACK) are
built with its application-layer types. Its master does not clear the restart
bit. It sends the freezes without acknowledgement, which makes
`freezeNoAckServer` testable and now declared for every outstation.

**Gate:** smoke test and cross-stack self-test for the role it declares. Met
on linux/arm64 (2026-10-20): 28 of 28 smoke checks (checked against the
opendnp3 server); the four-adapter run has 1114 checks passed, none failed,
19 known-bug hits, 117 skipped. Open: the outstation role, which needs a
second fixture within dnp3py's limits and cases for it.

### Triangle MicroWorks DNP3 SCL (on hold)

**On hold** since 2026-10-20, by decision of the owner of this repository:
nothing is built, published or tested for it, and no work is planned until
the hold is lifted.

The ANSI C DNP Source Code Library
(https://products.trianglemicroworks.com/products/source-code-libraries/dnp-scl-pages/ansi-c-source-code)
is licensed per customer and not public. When the hold is lifted, the
adapter would build from an archive supplied at build time
(`adapters/tmw/vendor/`, already ignored by git and by the Docker build
context), verified against a SHA-256 recorded in `versions.yaml`. What has to
be settled first is how the archive reaches local builds and CI (a private
repository, a build secret, a self-hosted runner) without putting it in this
public repository or in a public image.

## Phase 6 - Expanded coverage

Each enters the contract when at least two adapters can exercise it against
each other (REQUIREMENTS.md R8.3):

- UDP and TLS: both P0 libraries have them; needs transport flags in the contract and, for TLS, test certificates baked into the images.
- Serial: both P0 libraries have it; needs a virtual serial link between two containers (socat pty pair).
- File transfer (g70): stepfunc master against the go-dnp3 outstation, and go-dnp3 in both roles.
- Secure authentication v5 (g120): go-dnp3 in both roles; a second stack only with Triangle MicroWorks, which is on hold.
- Further point types (frozen analogs, g13/g43 command events, time-and-interval) where two stacks serve them.

## Phase 7 - Release qualification (open)

- Image scanning and SBOMs attached to releases
- A compatibility report generated from the self-test documents
