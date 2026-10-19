# Plan

Delivery in phases, each with an acceptance gate. Status as of 2026-10-19.

| Phase | Scope | Status |
|-------|-------|--------|
| 1 | Foundation: contract, fixture format, validation, version manifest, tooling | done |
| 2 | opendnp3 adapter, client and server | done |
| 3 | stepfunc adapter; cross-stack self-test | done |
| 4 | First published images; consumption from a library's tests | not started |
| 5 | dnp3py, DSC Systems go-dnp3, Triangle MicroWorks adapters | evaluated, not started |
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

Met on linux/arm64 (2026-10-19): `make smoke-stepfunc` 32 of 32; `make interop` 344 checks passed, none failed, 8 known-bug hits, 6 skipped for undeclared capabilities. The known bug is the stepfunc outstation's first SELECT after start, when the master's confirm of the null unsolicited response arrives between SELECT and OPERATE; select-before-operate is checked separately after that and passes. Details in [docs/COMPATIBILITY.md](docs/COMPATIBILITY.md). linux/amd64 is left to CI.

## Phase 4 - First published images

- `v0.1.0` through the release workflow: linux/amd64 and linux/arm64, both images to GHCR, `manifest.json` with the digests.
- A library pins those digests and runs its tests against them without a checkout of this repository.

The stepfunc image is published although its upstream licence is
non-commercial: the images exist for interoperability testing only, which the
owner of this repository decided is the purpose they are published for. The
licence travels with the image (`/licenses/dnp3/LICENSE.txt`) and is stated
in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

## Phase 5 - Additional adapters

Evaluated on 2026-10-19 by reading the sources at the pins; nothing built yet.
Each joins `ADAPTERS` in the Makefile and the CI matrix once it passes `make
smoke-<adapter>` and the cross-stack self-test for the roles it declares.

| Adapter | Pin | Roles | What it adds | What it lacks (would be declared false) |
|---------|-----|-------|--------------|------------------------------------------|
| `dnp3py` (P1) | dnp3py 0.6.0, PyPI, commit `c00d5df2`, MIT | both | A Python engine | Master: no restart, time synchronization, freeze or assign-class calls, no command status in results, group and variation not passed to the handler (the adapter must decode responses itself). Outstation: no double-bit inputs, octet strings or per-point variations (analog inputs always g30v1), no analog output events, and the TCP runner does not appear to send unsolicited responses. Needs either its own small fixture or a `pointTypes`-aware skip |
| `dscsystems` (P1, experimental) | DSC Systems go-dnp3 v0.8.1, commit `70ff72bb`, GPL-3.0 | both | DIRECT OPERATE NO ACK (master), file transfer g70 in both roles, secure authentication v5 (symmetric subset) in both roles, LAN and non-LAN time synchronization | Master: restart delay not returned, no ASSIGN CLASS, no all-objects read, no `_NR` freezes. The adapter is GPL-3.0-or-later. Its own interop notes report opendnp3 3.1.2 tested in both directions |
| `tmw` (P2) | Triangle MicroWorks DNP Source Code Library, ANSI C edition (product page: https://products.trianglemicroworks.com/products/source-code-libraries/dnp-scl-pages/ansi-c-source-code), commercial | both | Industry reference | The source is licensed per customer and not public: the adapter builds from an archive supplied at build time (`adapters/tmw/vendor/`, ignored by git and the build context), verified against a SHA-256 in `versions.yaml`. Waiting for the owner of this repository to say how the archive reaches local builds and CI (for example a private repository or a build secret) |

## Phase 6 - Expanded coverage

Each enters the contract when at least two adapters can exercise it against
each other (REQUIREMENTS.md R8.3):

- UDP and TLS: both P0 libraries have them; needs transport flags in the contract and, for TLS, test certificates baked into the images.
- Serial: both P0 libraries have it; needs a virtual serial link between two containers (socat pty pair).
- File transfer (g70): stepfunc master against the go-dnp3 outstation, and go-dnp3 in both roles.
- Secure authentication v5 (g120): go-dnp3 in both roles; a second stack only with Triangle MicroWorks.
- DIRECT OPERATE NO ACK and the `_NR` freezes: with the go-dnp3 master against both P0 outstations.
- Further point types (frozen analogs, g13/g43 command events, time-and-interval) where two stacks serve them.

## Phase 7 - Release qualification (open)

- Image scanning and SBOMs attached to releases
- A compatibility report generated from the self-test documents
