# dnp3-interop Releases

## How a release is made

1. Write the notes of the version below and set `interop_version` in `versions.yaml` to it, in the same change. `make validate` (and CI) fails when RELEASE.md has no notes for `interop_version`.
2. `make ci` on the commit to release.
3. Run the `Release` workflow on `main` from the Actions tab and pick the bump (`patch` by default, `minor` or `major`) that gives `vX.Y.Z`, or push the tag `vX.Y.Z` yourself. The release workflow runs one job on an amd64 runner and one on an arm64 runner: each validates (including the notes of the tag), builds every image natively with `ADAPTER_VERSION` set to the tag, runs the smoke and cross-stack self-tests on those images and pushes exactly those of the published adapters (opendnp3, dscsystems, dnp3py: `PUBLISHED_ADAPTERS` in the Makefile) to GHCR as `<image>:vX.Y.Z-amd64` and `-arm64`. The stepfunc image is built and self-tested but never pushed: the licence of the Step Function I/O dnp3 crate forbids distributing it. A last job joins the two into the multi-arch `<image>:vX.Y.Z` and creates the GitHub release with `manifest.json` (upstream pins, the digests of the multi-arch images, and under `localOnly` the stepfunc pin and how to build it) and `checksums.txt`.
4. The release workflow then runs the **Release qualification** workflow on what it published: the smoke and cross-stack self-tests again, natively on both architectures, against the images in the registry, with the stepfunc image built from the tag; a compatibility report from their documents; a Trivy vulnerability scan and a CycloneDX SBOM of every published image and architecture. It attaches `compatibility-report.md`, `vulnerabilities.md`, `sboms.tar.gz` and `vulnerability-scans.tar.gz` to the release. Run it by hand (Actions → Release qualification) to qualify an earlier release again; vulnerability findings are recorded, not gated on.
5. Consumers pin the digests from `manifest.json`, and build the stepfunc image from the tag (`make image-stepfunc VERSION=vX.Y.Z`).

The notes are the body of the GitHub release, followed by the image digests
and how to build the stepfunc and dnp3c images: everything after the heading
`## vX.Y.Z` up to the next line that is exactly `---`
(`scripts/release-notes.sh vX.Y.Z` prints it). This is the format of every
otfabric repository and the rule of the shared release workflow, so: one
section per version, newest first, each ending with a `---` line, and no
`---` line inside a section.

Tags are immutable. A fix is a new version.

| What changed | Version |
|--------------|---------|
| Contract or fixture format changed incompatibly | major |
| New operation, behaviour, fixture, adapter or document field | minor |
| Upstream pin, base image or bug fix with unchanged contract | patch |

---

## v0.8.0

**Date:** 2026-11-01
**Previous release:** v0.7.2

## Summary

Counters that really change on a running outstation, frozen counter events with
real time stamps, and a declared frozen event policy. Container contract 1.4,
additions only; fixture format 1.0.

## Changes

### Contract 1.4

- **`counter-increment`**, a command run in the server's container, adds to a counter of the running outstation over `server --control-socket`. Feature `counterIncrementServer`, all four adapters. The change goes through the library's own update, so its event is the library's. The opendnp3 adapter holds the counter values itself, as the library has no read of its database, and takes every FREEZE AND CLEAR for one of all counters.
- **`server --frozen-event-policy on-change|always`**, for a stack that can be set (stepfunc); a policy an image does not have is a usage error.
- **Capability document**: `frozenEventPolicies`, `frozenEventRetention`, `unsupportedReasons`, and the features `frozenCounterEventsServer`, `frozenCounterEventTimeClient`.
- **`counters` fixture**: frozen counter events g23v5 and g23v6.

### Self-test

- **`scripts/features.sh`, `[counters]`**: on every pairing, real changes of a counter, the g22 event, the frozen value held until a freeze, a change after a clear; g23v5 and g23v6 with the time of the freeze; under each policy, a freeze of unchanged counters; g21v5 with the time of the newest freeze, with or without an event; retention of events not yet polled.
- **`scripts/interop.sh`**: the frozen event policy must be the one declared, as well as consistent.
- The dnp3py outstation has no frozen counter events: g23v5, g23v6 and the policy are reported unsupported with the reason, and the change itself is tested.

### Docs

- **`docs/COMPATIBILITY.md`**: "Mandatory interoperability contract", apart from normative conformance requirements; generation and retention policy by stack and version. What IEEE 1815 says stays an outstanding research item, not a blocker: what was checked and what is needed are listed, and nothing claims the standard permits or prohibits either behaviour.

---

## v0.7.2

**Date:** 2026-11-01
**Previous release:** v0.7.1

### Changed

- Policy 1.2: build output in `bin/` and `release/`, and shared CI at v3.3.0.

---

## v0.7.1

**Date:** 2026-10-31
**Previous release:** v0.7.0

## Summary

Reclassifies the frozen counter events of DSC go-dnp3 as valid implementation
behaviour and tests the freeze more closely. The images are rebuilt unchanged;
container contract 1.3 and the fixtures are unchanged.

## Changes

### Self-test

- **Removed** the waiver `dscsystems-freeze-unchanged-events`: a freeze that raises frozen counter events for values that did not change is an implementation choice, not a bug. The maintainer's decision; the normative clause of IEEE 1815-2012 has not been verified, see `docs/COMPATIBILITY.md`.
- **`scripts/interop.sh`**: a freeze that changes no frozen value may raise an event for every frozen value or none, but the same across freezes and masters.
- **More hard checks of the freeze**, in every pairing: a second freeze keeps the frozen values and leaves the counters; FREEZE AND CLEAR freezes before it clears; a freeze after the clear updates the frozen values and raises their events; the flags and time stamps of the events; confirmed frozen counter events are not sent again.
- The clear's counter events on the DSC go-dnp3 outstation are now checked: the waiver covered that whole check, so it did not run there before.

### Docs

- **`docs/COMPATIBILITY.md`**: the principle that a difference in behaviour is not automatically a defect, and mandatory freeze semantics against the frozen event policy. `docs/FIXTURES.md` and `COVERAGE.md` follow.

---

## v0.7.0

**Date:** 2026-10-31
**Previous release:** v0.6.0

### Added

- Adds `results.json` in the interop-result-v1 format.
- Policy 1.2: build output in `bin/` and `release/`, and shared CI at v3.3.0.

---

## v0.6.0

**Date:** 2026-10-30
**Previous release:** v0.5.2

## Summary

The stepfunc adapter offers what dnp3 1.8.0 added: its master sends DIRECT
OPERATE NO ACK and its outstation applies ASSIGN CLASS. Container contract
1.3 unchanged; the other images are rebuilt unchanged.

## Changes

### Adapters

- **`stepfunc`**: `operate --mode direct-no-ack` sends DIRECT OPERATE NO ACK with the library's `operate_no_ack`; it succeeds when the request was sent and `commandStatus` is null. `directOperateNoAckClient` is true.
- **`stepfunc`**: the outstation applies ASSIGN CLASS (1.8.0 does by default) and reports each accepted point header as an `assign-class` server event; a header that names all points of a type reports the lowest and highest index of that type in the fixture. `assignClassServer` is true.

### Docs

- CAPABILITIES, COMPATIBILITY, COVERAGE, CONTAINER_CONTRACT and README describe the two capabilities. stepfunc's master still has no freeze without a response, no broadcast, no TLS listener and no group 90; its master still drops g50v4.

---

## v0.5.2

**Date:** 2026-10-30
**Previous release:** v0.5.1

## Summary

Step Function I/O dnp3 1.8.0, DSC go-dnp3 v0.8.2 and current base images.
Container contract 1.3 unchanged. The stepfunc image is still built locally
only and never published.

## Changes

### Upstream pins

- **`dscsystems`**: DSC go-dnp3 v0.8.1 → v0.8.2 (commit `1fcec1d7881541d794fe257d2ede3ba46587b500`). The release adds DSC's own interop tests against these images; the library is unchanged, so its five entries in `tests/known-bugs.tsv` move to v0.8.2 as they are.
- **Base images**: new digests of `debian:trixie-slim`, `python:3.14.8-slim-trixie` and `rust:1.99.1-slim-trixie`.
- **`stepfunc`**: dnp3 1.7.0 → 1.8.0 (tag commit `235533ae276cd8f33b669c6db395979a308ef848`). The adapter no longer matches the error variants 1.8.0 deprecates as never returned (`WriteError::IinError`, `TimeSyncError::IinError`, `CommandResponseError::Request`); an IIN2 rejection arrives as `TaskError::RejectedByIin2`, which it already reported as `rejected`. 1.8.0 fixes both of its known bugs, which leave `tests/known-bugs.tsv`: the outstation's NO_SELECT on the first SELECT after start, and the master's decoding of a one-octet signed attribute without its sign.

---

## v0.5.1

**Date:** 2026-10-21
**Previous release:** v0.5.0

## Summary

The release manifest names the dnp3c runner. The images are unchanged.

## Changes

### Release

- **`manifest.json`** lists the dnp3c runner under `localOnly`, next to stepfunc: how to build it from the tag, and its dnp3c version. The release workflow fails if `images` names either one. The release notes say how to build both.

### Docs

- README, THIRD_PARTY_NOTICES: the dnp3c image is pinned by building it from the tag, like the stepfunc one. PLAN: phase 8, the dnp3c runner; every release is qualified.

---

## v0.5.0

**Date:** 2026-10-21
**Previous release:** v0.4.3

## Summary

A dnp3c runner: the DNP Users Group's DNP3 IED Certification Procedure, as
Step Function I/O's dnp3c implements it, against any outstation with a small
control port. Built locally, not published.

## Changes

### Tools

- **`dnp3-interop-dnp3c`** (`make image-dnp3c`, [docs/CONFORMANCE.md](docs/CONFORMANCE.md)): dnp3c 0.2.0 (3.1 rev 1 at subset levels 1 to 3, 2.7 at level 2) with an integration plugin and a runner in `conformance/dnp3c/`. The plugin forwards each step dnp3c cannot do over DNP3 to the device under test's control port; the runner runs every procedure on its own and writes dnp3c's report. dnp3c is GPL-3.0-only and so are the plugin and runner ([conformance/LICENSE](conformance/LICENSE)). The image build fetches dnp3c from Maven Central with strict checksums, and the image is never published (CI checks no publishing target names it). Pinned in `versions.yaml` (`tools.dnp3c`), checked by `scripts/check-versions.sh`.

---

## v0.4.3

**Date:** 2026-10-21
**Previous release:** v0.4.2

## Summary

The stepfunc image is no longer published, at Step Function I/O's request
(#19): the licence of their dnp3 crate (LICENSE.txt §2.2) does not permit
distributing the library, and the image has it compiled in. Interoperability
testing with it is permitted, so the adapter and its Dockerfile stay: build
the image locally. Container contract 1.3 unchanged; no adapter code changed.

## Changes

- Release: every image is still built and self-tested natively on linux/amd64 and linux/arm64, but only the opendnp3, dscsystems and dnp3py images are pushed. The Makefile's `PUBLISHED_ADAPTERS` (fixed, not overridable) drives `publish`, `publish-arch`, `publish-manifest`, `digests` and `qualify`; `ADAPTERS` still has all four for `images`, `smoke`, `interop` and `features`. CI fails if any of those targets names stepfunc, also when asked to on the command line.
- `manifest.json` has no stepfunc image; the stepfunc/dnp3 pin moves to `localOnly.stepfunc` with the command that builds the image. The release notes have no stepfunc digest and say how to build it.
- Release qualification builds the stepfunc image from the release tag for the self-test; the vulnerability scan and the SBOMs cover the published images only, and `scripts/qualify.sh` refuses `stepfunc`.
- `compose.yaml` builds the stepfunc service from the checkout instead of pulling it.
- Docs: README (stepfunc row, quick start with the opendnp3 and dscsystems images, [building the stepfunc image](README.md#building-the-stepfunc-image)), THIRD_PARTY_NOTICES.md (stepfunc not redistributed), REQUIREMENTS.md (R1.2, R5.3, new R7.5), PLAN.md, CONTRIBUTING.md, SECURITY.md, docs/COMPATIBILITY.md, docs/CONTAINER_CONTRACT.md, `adapters/LICENSE`, the header comment of `adapters/stepfunc/Dockerfile`, `versions.yaml` (`interop_version` 0.4.3; stepfunc `published: false`).
- Upgrading: build the stepfunc image yourself at the tag you test against: `make image-stepfunc VERSION=v0.4.3`, or `docker build --build-arg ADAPTER_VERSION=v0.4.3 -f adapters/stepfunc/Dockerfile -t ghcr.io/otfabric/dnp3-interop-stepfunc:v0.4.3 .`; the build fetches the crate from crates.io under Step Function I/O's licence. Do not push it to a registry.
- The stepfunc images of earlier releases (v0.1.0 to v0.4.2) are withdrawn: a consumer that pins `ghcr.io/otfabric/dnp3-interop-stepfunc@sha256:...` from an earlier `manifest.json` builds the image from that release's tag instead. Their other images are unchanged.

---

## v0.4.2

**Date:** 2026-10-21
**Previous release:** v0.4.1

## Summary

The adapters follow the container contract where the adapter, not its
library, decides; container contract 1.3 unchanged. The contract now states
the rules and names only the remaining library differences.

## Changes

- stepfunc: `file-info` refuses `--file-user`/`--file-password` (exit 2); a runtime that cannot be created gives the `failed` result document (the master runs on a current-thread runtime); UDP resolving and opening the socket are bounded by `--connect-timeout-ms`, and either failing is `connect-failed`; FREEZE AT TIME at a time already past is refused without an interval and resumes at the next scheduled time with one; g31v3 and g31v4 carry their time; an extended attribute list is `extendedAttributeList`; `visibleString` attributes are counted in characters, and one over 255 octets of UTF-8 is refused; `print-fixture` refuses `\`; `server` and `client` take their settings as one struct (no clippy suppression).
- opendnp3: TLS files are loaded up front: the client fails with `failed` and its result document (dialling or listening), the server exits 1 before `ready`; a listening master that cannot bind prints its document; `operate-multi` gives `commandStatuses` null on a partial echo; a control whose SELECT was echoed SUCCESS has status SUCCESS, not the library's initial UNDEFINED (a partial select echo was `command-failed` UNDEFINED, now `failed`); `print-fixture` refuses `\`; the capability document lists `fileAppend*`, `fileAuth*` and `deadband*` as false.
- dscsystems: attribute lists have value null; `file-info` refuses `--file-user`/`--file-password`; the master checks the length of `--name` (200 characters); TLS files that cannot be loaded give the complete result document; the server exits 1 without `stopped` when the session ends without SIGTERM/SIGINT; ACTIVATE_CONFIG (31) is refused and not reported as `manage`; a refused open for appending is reported as `append`; `visibleString` attributes are counted in characters, and one over 255 octets of UTF-8 is refused; the master gives the time of g31v3 and g31v4; `operate-multi` on a partial echo gives `commandStatuses` null and fails, `failed` when every echoed control succeeded (the library counted that as a success) and `command-failed` with a refusal among them; a dialling server that cannot open its channel names the address it dials; lint fixes (errcheck).
- dnp3py: accepts `--transport tcp` (`udp` and `serial` are usage errors); `operate-multi` reports the first refusal and the statuses of the last echo, and a partial echo fails with `commandStatuses` null; the server exits 1 without `stopped` when the runner ends without a signal; the capability document lists the file append/auth and deadband features (false) and no longer has `timeSyncOnDemand` in `serverFeatures`; `versions.yaml` gives it both roles; lint fixes (ruff); the client catches the exceptions dnp3py, the channel and the decoders raise instead of every exception.
- `scripts/validate-fixtures.py`: a `visibleString` attribute must also fit in 255 octets of UTF-8; ruff fixes in the scripts. `scripts/interop.sh` and `scripts/features.sh` expand arrays that can be empty in a way macOS's bash 3.2 accepts under `set -u`, and `scripts/features.sh` names its documents with `mktemp`, not `$BASHPID` (bash 4). `scripts/test-pki.sh` writes the EC keys with the named curve: LibreSSL (macOS's openssl) wrote explicit curve parameters, which the stepfunc and dscsystems adapters cannot load.
- Docs: the corrections of 3587453 (every claim reviewed before going public); CONTAINER_CONTRACT.md, CAPABILITIES.md and FIXTURES.md now state the rules the adapters follow.
- Licence label: the dscsystems capability document and `versions.yaml` say `GPL-3.0-or-later` (was `GPL-3.0`), as the image label and THIRD_PARTY_NOTICES.md do.

---

## v0.4.1

**Date:** 2026-10-20
**Previous release:** v0.4.0

## Summary

A fix to the dscsystems adapter; container contract 1.3 unchanged.

## Changes

- dscsystems, `server --outstations N`: the library's multidrop bus routed the frames of a new connection before the outstations' sessions had re-attached, so a request that arrived first was lost (about one in five in a stress test). The adapter now holds the bus's first read of each connection until every outstation is back (#16).
- The multidrop known bug and the "(intermittent)" exemption from the stale-entry check are gone: every known bug has to reproduce again.

---

## v0.4.0

**Date:** 2026-10-20
**Previous release:** v0.3.0

## Summary

Container contract 1.3: device attributes, several controls per request,
freeze at time and analog freeze, analog deadbands, file append and
authentication, event buffer overflow, the link layer, broadcast, management
functions and several outstations on one port. Additions only; documents
still carry `schemaVersion` `"1.0"`.

## Changes

- Device attributes (g0): the `attributes` fixture, `attribute-read`, `attribute-write`, the `attribute-write` event; features `deviceAttributesServer`, `deviceAttributesClient`.
- Several controls in one request: `operate-multi` with `commandStatuses`, the fixture's `maxControlsPerRequest`, the `controls` fixture; features `multipleControlsClient`, `maxControlsServer`.
- Freeze at time and analog freeze: `freeze --at immediately|+MS|EPOCH_MS [--interval-ms N]`, `freeze --group 30`; features `freezeAtTimeServer/Client`, `analogFreezeServer/Client`.
- Analog deadbands: `deadband-write --deadbands I:V,...` (g34v3) and group 34 in object documents; features `deadbandWriteServer/Client`, `deadbandReadServer/Client`.
- File append and AUTHENTICATE_FILE: `file-put --append`, `file-authenticate`, `--file-user`/`--file-password` on the file operations and the server; file events `append`, `authenticate`; features `fileAppendServer/Client`, `fileAuthServer/Client`.
- Event buffer overflow: the fixture's `outstation.eventBufferSize` and the `events` fixture; IIN2.3 in `iin`; feature `eventBufferServer`.
- Link layer: `--link-confirms` (client and server), `client --keep-alive-ms`, the server event `link-status`; features `linkConfirmsServer/Client`, `keepAliveClient`, `linkStatusEventServer`.
- Broadcast: `operate --mode direct-no-ack --broadcast`, IIN1.0 in `iin`, the server event `broadcast`; features `broadcastServer`, `broadcastEventServer`, `broadcastClient`.
- Management functions: `manage --function initialize-data|initialize-application|start-application|stop-application|save-configuration`, the server event `manage`; features `manageServer`, `manageClient`.
- Several outstations: `server --outstations N`; feature `multipleOutstationsServer`.
- Known bugs whose text ends in "(intermittent)" are exempt from the stale-entry check.

---

## v0.3.0

**Date:** 2026-10-20
**Previous release:** v0.2.0

## Summary

Container contract 1.2: more point types, the dual end point, security
statistics, and dnp3py's outstation role. Additions only; documents still
carry `schemaVersion` `"1.0"`.

## Changes

- Point types: the `extended` fixture with frozen analog inputs (g31/g33), command events of the outputs (g13/g43, with the status of the control) and time and interval (g50v4); object documents get `status` and `units`; features `commandEventsServer`, `commandEventsClient`, `frozenAnalogInputsClient`, `timeAndIntervalClient`. Served by dscsystems; read by the stepfunc, opendnp3 and dscsystems masters as each can.
- Dual end point: `server --connect HOST:PORT [--tls-server-name NAME]` (the outstation dials) and `client --listen [--bind-address ADDR]` (the master listens), over TCP and TLS; features `dualEndpointServer`, `dualEndpointClient`, `dualEndpointClientTls`. stepfunc, opendnp3 and dscsystems, both ways; stepfunc's master listens over TCP only.
- Security statistics: with secure authentication on, dscsystems serves g121 and raises g122 events (`--sa-statistics-class`, `--sa-statistics-threshold`); object documents get `association`; features `securityStatisticsServer`, `securityStatisticsClient`.
- dnp3py has the server role: it serves the new `dnp3py` fixture (the baseline within its limits), which every outstation serves; `FIXTURE=dnp3py scripts/interop.sh` (in `make interop`) compares the four. `serverFeatures` in the capability document gives the features of the server role where they differ.
- Known bugs can be scoped to a fixture (fifth column of tests/known-bugs.tsv).

---

## v0.2.0

**Date:** 2026-10-20
**Previous release:** v0.1.0

## Summary

Container contract 1.1: DNP3 over TLS, UDP and serial; file transfer
(group 70); secure authentication v5 (group 120). Every addition is
optional per image and declared in its capability document; contract 1.0
consumers are unaffected (documents still carry `schemaVersion` `"1.0"`).

## Changes

- Transports: `--transport tcp|udp|serial`; TLS with server `--tls-cert`, `--tls-key`, `--tls-ca` and client `--tls`, `--tls-ca`, `--tls-cert`, `--tls-key`, `--tls-server-name` (mutual, TLS 1.2 and 1.3); UDP with server `--udp-peer` and client `--udp-local-port`; serial with `--serial-device`, `--baud`, `--data-bits`, `--parity`, `--stop-bits`. The ready event has `transport`. stepfunc, opendnp3 (now built with OpenSSL) and dscsystems have all three; the images with serial carry socat to bridge a pseudo-terminal to TCP.
- File transfer: the `files` fixture (fixture format: `files`, `directories`); operations `file-get`, `file-put`, `file-ls`, `file-info`, `file-delete`; result fields `file`, `files`, `fileStatus`; error `file-error`; server event `file`; features `fileServer`, `fileClient`, `fileDeleteClient`. dscsystems in both roles, stepfunc as master without delete.
- Secure authentication v5: `--sa-update-key`, `--sa-user`, `--sa-mac`; error `auth-failed`; server events `auth` and `auth-error`; `secureAuthentication` in the capability document (MAC and key-wrap algorithms, critical function codes). dscsystems in both roles, tested against itself only.
- `scripts/features.sh` (`make features`, part of `make documents`): the cross-stack self-test of all of it; `scripts/test-pki.sh` makes the test certificates of a run. The compatibility report includes it.
- Adapters close only after their last confirm (all masters); the opendnp3 master judges a time synchronization by its library's result; dnp3py image without pip and setuptools.
- The release qualification (Trivy scan, SBOMs, compatibility report) attaches non-empty SBOM and scan archives.

---

## v0.1.0

**Date:** 2026-10-20
**Previous release:** none, first release

## Summary

First version: three independent reference implementations as master and as
outstation, and a fourth as master, behind container contract 1.0.

## Changes

### Added

- **Container contract 1.0** — `server` (outstation), `client <operation>` (master), `print-capabilities`, `print-fixture`, `version`; JSON Lines server events; one result document per client operation; exit codes 0..4; readiness file and health check.
- **Fixture format 1.0** with JSON Schema, cross-field validation and the **baseline** fixture: 22 points of eight types in all three event classes, controls on binary and analog outputs with targets, select-required points and ranges, counters with frozen counters, an octet string, restart delays, NEED_TIME at start.
- **`stepfunc` adapter** — Step Function I/O dnp3 1.7.0, Rust.
- **`opendnp3` adapter** — opendnp3 3.1.2 (commit `26b4c01e`), C++.
- **`dnp3py` adapter** — dnp3py 0.6.0, Python, client (master) role only; with DSC go-dnp3 the master that sends DIRECT OPERATE NO ACK, and the one that sends the freezes without acknowledgement.
- **`dscsystems` adapter** — DSC Systems go-dnp3 v0.8.1, Go, experimental upstream. GPL-3.0-or-later.
- **Client operations** — `integrity-poll`, `event-poll`, `read`, `operate` (g12v1, g41v1..v4; direct and select-before-operate), `time-sync` (LAN and non-LAN), `restart` (cold, warm), `enable-unsolicited`, `disable-unsolicited`, `assign-class`, `freeze` (with and without clear), `monitor`.
- **Self-tests** — `make smoke` (32 contract checks per server adapter, 28 for the master-only dnp3py) and `make interop` (48 cases over all twelve client/server pairings requiring identical result documents, plus value checks of controls, events, freezes, unsolicited responses, class assignment and time synchronization, run as one suite per server adapter in parallel: 1114 checks on linux/arm64, none failed, 19 hits of known upstream bugs). CI builds the images and runs the smoke tests in parallel.
- **Schemas** for the capability and client result documents, validated against every document the self-tests produce.
- **`versions.yaml`** with every upstream pin and base image digest, checked against the build files; an upstream candidates workflow that resolves the latest release of every stack, self-tests the adapters whose pin is behind and writes one decision table.

### Upstream notes

- opendnp3 is archived; 3.1.2 is its last release. Its sources need `<cstdint>` included explicitly with GCC 14. Its master cannot send DIRECT OPERATE NO ACK or the `_NR` freezes and synchronizes time only when the outstation sets NEED_TIME; both outstations set PARAMETER_ERROR along with a NOT_SUPPORTED command status, so such a refusal is `rejected` with no command status.
- The stepfunc outstation answers ASSIGN CLASS with NO_FUNC_CODE_SUPPORT, and fails the first SELECT after start with NO_SELECT because the master's confirm of its null unsolicited response arrives between SELECT and OPERATE (listed in `tests/known-bugs.tsv`); its master cannot send DIRECT OPERATE NO ACK or the `_NR` freezes.
- The Step Function I/O dnp3 crate is under a non-commercial, non-production licence; its image is published for interoperability testing only.
- DSC go-dnp3 v0.8.1: FREEZE AND CLEAR clears only counters that have a frozen counter; a freeze raises events for unchanged frozen values; neither its master nor its outstation filters link addresses (all three in `tests/known-bugs.tsv`). Its outstation carries out a restart (events discarded, DEVICE_RESTART and NEED_TIME set again), so restart cases run on a fresh outstation.

### For consumers

- Pin the images by digest from the release's `manifest.json`; read `print-fixture baseline` and `print-capabilities` from the image under test.
- The stepfunc image contains the Step Function I/O dnp3 crate, whose licence permits non-commercial, non-production use only: it is for interoperability testing.
- Do not make the first request after starting the stepfunc image a SELECT (see `docs/COMPATIBILITY.md`), or expect NO_SELECT for it.

### Not in this release

- dnp3py as outstation.
- The Triangle MicroWorks adapter, which is on hold.
- UDP, TLS and serial links, file transfer, secure authentication.

---
