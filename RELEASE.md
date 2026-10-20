# dnp3-interop Releases

## How a release is made

1. `make ci` on the commit to release.
2. Update `interop_version` in `versions.yaml` and the notes below.
3. Tag `vX.Y.Z` and push the tag. The release workflow runs one job on an amd64 runner and one on an arm64 runner: each validates, builds every image natively with `ADAPTER_VERSION` set to the tag, runs the smoke and cross-stack self-tests on those images and pushes exactly them to GHCR as `<image>:vX.Y.Z-amd64` and `-arm64`. A last job joins the two into the multi-arch `<image>:vX.Y.Z` and creates the GitHub release with `manifest.json` (upstream pins and the digests of the multi-arch images) and `checksums.txt`.
4. Consumers pin the digests from `manifest.json`.

Tags are immutable. A fix is a new version.

| What changed | Version |
|--------------|---------|
| Contract or fixture format changed incompatibly | major |
| New operation, behaviour, fixture, adapter or document field | minor |
| Upstream pin, base image or bug fix with unchanged contract | patch |

---

## v0.1.0

**Date:** 2026-10-20

## Summary

First version: three independent reference implementations as master and as
outstation, and a fourth as master, behind container contract 1.0.

## Changes

### Added

- **Container contract 1.0** — `server` (outstation), `client <operation>` (master), `print-capabilities`, `print-fixture`, `version`; JSON Lines server events; one result document per client operation; exit codes 0..4; readiness file and health check.
- **Fixture format 1.0** with JSON Schema, cross-field validation and the **baseline** fixture: 22 points of eight types in all three event classes, controls on binary and analog outputs with targets, select-required points and ranges, counters with frozen counters, an octet string, restart delays, NEED_TIME at start.
- **`stepfunc` adapter** — Step Function I/O dnp3 1.7.0, Rust.
- **`opendnp3` adapter** — opendnp3 3.1.2 (commit `26b4c01e`), C++.
- **`dnp3py` adapter** — dnp3py 0.6.0, Python, client (master) role only; with go-dnp3 the master that sends DIRECT OPERATE NO ACK, and the one that sends the freezes without acknowledgement.
- **`dscsystems` adapter** — DSC Systems go-dnp3 v0.8.1, Go, experimental upstream. GPL-3.0-or-later.
- **Client operations** — `integrity-poll`, `event-poll`, `read`, `operate` (g12v1, g41v1..v4; direct and select-before-operate), `time-sync` (LAN and non-LAN), `restart` (cold, warm), `enable-unsolicited`, `disable-unsolicited`, `assign-class`, `freeze` (with and without clear), `monitor`.
- **Self-tests** — `make smoke` (32 contract checks per server adapter, 28 for the master-only dnp3py) and `make interop` (48 cases over all twelve client/server pairings requiring identical result documents, plus value checks of controls, events, freezes, unsolicited responses, class assignment and time synchronization, run as one suite per server adapter in parallel: 1114 checks on linux/arm64, none failed, 19 hits of known upstream bugs). CI builds the images and runs the smoke tests in parallel.
- **Schemas** for the capability and client result documents, validated against every document the self-tests produce.
- **`versions.yaml`** with every upstream pin and base image digest, checked against the build files; an upstream candidates workflow that resolves the latest release of every stack, self-tests the adapters whose pin is behind and writes one decision table.

### Upstream notes

- opendnp3 is archived; 3.1.2 is its last release. Its sources need `<cstdint>` included explicitly with GCC 14. Its master cannot send DIRECT OPERATE NO ACK or the `_NR` freezes and synchronizes time only when the outstation sets NEED_TIME; both outstations set PARAMETER_ERROR along with a NOT_SUPPORTED command status, so such a refusal is `rejected` with no command status.
- The stepfunc outstation answers ASSIGN CLASS with NO_FUNC_CODE_SUPPORT, and fails the first SELECT after start with NO_SELECT because the master's confirm of its null unsolicited response arrives between SELECT and OPERATE (listed in `tests/known-bugs.tsv`); its master cannot send DIRECT OPERATE NO ACK or the `_NR` freezes.
- The Step Function I/O dnp3 crate is under a non-commercial, non-production licence; its image is published for interoperability testing only.

- go-dnp3 v0.8.1: FREEZE AND CLEAR clears only counters that have a frozen counter; a freeze raises events for unchanged frozen values; neither its master nor its outstation filters link addresses (all three in `tests/known-bugs.tsv`). Its outstation carries out a restart (events discarded, DEVICE_RESTART and NEED_TIME set again), so restart cases run on a fresh outstation.

### For consumers

- Pin the images by digest from the release's `manifest.json`; read `print-fixture baseline` and `print-capabilities` from the image under test.
- The stepfunc image contains the Step Function I/O dnp3 crate, whose licence permits non-commercial, non-production use only: it is for interoperability testing.
- Do not make the first request after starting the stepfunc image a SELECT (see `docs/COMPATIBILITY.md`), or expect NO_SELECT for it.

### Not in this release

- dnp3py as outstation.
- The Triangle MicroWorks adapter, which is on hold.
- UDP, TLS and serial links, file transfer, secure authentication.
