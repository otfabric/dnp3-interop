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

## Unreleased (planned v0.1.0)

## Summary

First version: two independent reference implementations, as master and as
outstation, behind container contract 1.0.

## Changes

### Added

- **Container contract 1.0** — `server` (outstation), `client <operation>` (master), `print-capabilities`, `print-fixture`, `version`; JSON Lines server events; one result document per client operation; exit codes 0..4; readiness file and health check.
- **Fixture format 1.0** with JSON Schema, cross-field validation and the **baseline** fixture: 22 points of eight types in all three event classes, controls on binary and analog outputs with targets, select-required points and ranges, counters with frozen counters, an octet string, restart delays, NEED_TIME at start.
- **`stepfunc` adapter** — Step Function I/O dnp3 1.7.0, Rust.
- **`opendnp3` adapter** — opendnp3 3.1.2 (commit `26b4c01e`), C++.
- **Client operations** — `integrity-poll`, `event-poll`, `read`, `operate` (g12v1, g41v1..v4; direct and select-before-operate), `time-sync` (LAN and non-LAN), `restart` (cold, warm), `enable-unsolicited`, `disable-unsolicited`, `assign-class`, `freeze` (with and without clear), `monitor`.
- **Self-tests** — `make smoke` (contract checks per adapter) and `make interop` (cases over all client/server pairings requiring identical result documents, plus value checks of controls, events, freezes, unsolicited responses, class assignment and time synchronization).
- **Schemas** for the capability and client result documents, validated against every document the self-tests produce.
- **`versions.yaml`** with every upstream pin and base image digest, checked against the build files; an upstream candidates workflow for the dnp3 crate.

### Upstream notes

- opendnp3 is archived; 3.1.2 is its last release. Its sources need `<cstdint>` included explicitly with GCC 14. Its master cannot send DIRECT OPERATE NO ACK or the `_NR` freezes and synchronizes time only when the outstation sets NEED_TIME; both outstations set PARAMETER_ERROR along with a NOT_SUPPORTED command status, so such a refusal is `rejected` with no command status.
- The stepfunc outstation answers ASSIGN CLASS with NO_FUNC_CODE_SUPPORT, and fails the first SELECT after start with NO_SELECT because the master's confirm of its null unsolicited response arrives between SELECT and OPERATE (listed in `tests/known-bugs.tsv`); its master cannot send DIRECT OPERATE NO ACK or the `_NR` freezes.
- The Step Function I/O dnp3 crate is under a non-commercial, non-production licence; its image is published for interoperability testing only.

### Not in this release

- dnp3py, DSC Systems go-dnp3 and Triangle MicroWorks adapters.
- UDP, TLS and serial links, file transfer, secure authentication.
