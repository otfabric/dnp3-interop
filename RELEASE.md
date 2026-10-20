# dnp3-interop Releases

## How a release is made

1. `make ci` on the commit to release.
2. Update `interop_version` in `versions.yaml` and the notes below.
3. Tag `vX.Y.Z` and push the tag. The release workflow runs one job on an amd64 runner and one on an arm64 runner: each validates, builds every image natively with `ADAPTER_VERSION` set to the tag, runs the smoke and cross-stack self-tests on those images and pushes exactly them to GHCR as `<image>:vX.Y.Z-amd64` and `-arm64`. A last job joins the two into the multi-arch `<image>:vX.Y.Z` and creates the GitHub release with `manifest.json` (upstream pins and the digests of the multi-arch images) and `checksums.txt`.
4. The release workflow then runs the **Release qualification** workflow on what it published: the smoke and cross-stack self-tests again, natively on both architectures, against the images in the registry; a compatibility report from their documents; a Trivy vulnerability scan and a CycloneDX SBOM of every image and architecture. It attaches `compatibility-report.md`, `vulnerabilities.md`, `sboms.tar.gz` and `vulnerability-scans.tar.gz` to the release. Run it by hand (Actions → Release qualification) to qualify an earlier release again; vulnerability findings are recorded, not gated on.
5. Consumers pin the digests from `manifest.json`.

Tags are immutable. A fix is a new version.

| What changed | Version |
|--------------|---------|
| Contract or fixture format changed incompatibly | major |
| New operation, behaviour, fixture, adapter or document field | minor |
| Upstream pin, base image or bug fix with unchanged contract | patch |

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
