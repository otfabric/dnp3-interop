# Plan

Delivery in phases, each with an acceptance gate. Status as of 2026-10-21.

| Phase | Scope | Status |
|-------|-------|--------|
| 1 | Foundation: contract, fixture format, validation, version manifest, tooling | done |
| 2 | opendnp3 adapter, client and server | done |
| 3 | stepfunc adapter; cross-stack self-test | done |
| 4 | First published images; consumption from a library's tests | done: v0.1.0 to v0.4.2 published; a library pins the current release's digests. From v0.4.3 the stepfunc image is built locally, not published (#19) |
| 5 | dnp3py, DSC Systems go-dnp3, Triangle MicroWorks adapters | DSC go-dnp3 done (both roles); dnp3py done (both roles; the outstation serves the `dnp3py` fixture); Triangle MicroWorks on hold |
| 6 | Expanded coverage: UDP, TLS, serial, file transfer, secure authentication, and more | done as container contract 1.1 (v0.2.0), 1.2 (v0.3.0: point types, dual end point, security statistics) and 1.3 (v0.4.0, fixed in v0.4.1) |
| 7 | Release qualification: image scanning, SBOMs, compatibility report | done: every release from v0.1.0 qualified, results attached to their releases |
| 8 | The DNP3 IED Certification Procedure against any outstation: Step Function I/O's dnp3c with an integration plugin and a control protocol ([docs/CONFORMANCE.md](docs/CONFORMANCE.md)) | done: v0.5.0, image built locally (GPL-3.0-only), not published |

## Phase 1 - Foundation (done)

- Container contract 1.0 ([docs/CONTAINER_CONTRACT.md](docs/CONTAINER_CONTRACT.md)), schemas for the capability and client result documents
- Fixture format 1.0 with schema, cross-field validation and the baseline fixture ([docs/FIXTURES.md](docs/FIXTURES.md))
- `versions.yaml` and a check that it matches the build files
- Makefile, `compose.yaml`, smoke test, cross-stack self-test with its cases, CI, release and candidate workflows

**Gate:** fixtures validate and the repository builds with no reference to any consumer. Met: `make validate` passes the fixture checks; the only reference to a consumer is in the documentation.

## Phase 2 - opendnp3 (done)

- C++ adapter on opendnp3 3.1.2 (archived; its last release), pinned by commit, both roles, all eleven client operations of contract 1.0.
- The master cannot send DIRECT OPERATE NO ACK or the `_NR` freezes and synchronizes time only on NEED_TIME: declared in its capabilities.

**Gate:** client and server operate against each other using opendnp3 alone (`make smoke-opendnp3`, and the self-test with one adapter).

Met: `make smoke-opendnp3` passes 32 of 32 checks; opendnp3 against itself passes every case and value check of `make interop`.

## Phase 3 - stepfunc (done)

- Rust adapter on the dnp3 crate 1.7.0, both roles, all eleven client operations of contract 1.0.
- Its outstation refuses ASSIGN CLASS; its master cannot send DIRECT OPERATE NO ACK or the `_NR` freezes: declared in its capabilities.
- Cross-stack self-test over all four pairings: expected outcome per case, identical result documents across pairings, and the value checks of `scripts/interop.sh`.

**Gate:** both stacks satisfy the baseline contract in every direction (`make smoke-stepfunc`, `make interop`).

Met on linux/arm64 (2026-10-19): `make smoke-stepfunc` 32 of 32; `make interop` 344 checks passed, none failed, 8 known-bug hits, 6 skipped for undeclared capabilities. The known bug is the stepfunc outstation's first SELECT after start, when the master's confirm of the null unsolicited response arrives between SELECT and OPERATE; select-before-operate is checked separately after that and passes. Details in [docs/COMPATIBILITY.md](docs/COMPATIBILITY.md). Since run on linux/amd64 and linux/arm64 by CI as well.

## Phase 4 - First published images (done)

- Done: `v0.1.0` (2026-10-20) through the release workflow: linux/amd64 and linux/arm64, all four images to GHCR, publicly pullable, with `manifest.json` and `checksums.txt` on the [GitHub release](https://github.com/otfabric/dnp3-interop/releases/tag/v0.1.0). The release run self-tested exactly the images it pushed.

  | Image | Digest |
  |-------|--------|
  | `ghcr.io/otfabric/dnp3-interop-stepfunc:v0.1.0` (withdrawn, #19) | `sha256:3ae308559ae9ad423b3e53f5206aa5ccb16ed60c4816fcba8aeff29cb5903f9b` |
  | `ghcr.io/otfabric/dnp3-interop-opendnp3:v0.1.0` | `sha256:461bcec60491d6c94cf51055b75655fd6e8a142c06e73e1eb9e6eca377677ce7` |
  | `ghcr.io/otfabric/dnp3-interop-dscsystems:v0.1.0` | `sha256:bd019730340dc708d45e4e7da6514e551ac8086aace67e5988fec651614c4fe7` |
  | `ghcr.io/otfabric/dnp3-interop-dnp3py:v0.1.0` | `sha256:a8518fa9e98e54ceb82ad67af5d52bed8efb0085c185742dd14c523d7e5f7013` |

- Done: `v0.2.0` (2026-10-20, container contract 1.1), the same way: [release](https://github.com/otfabric/dnp3-interop/releases/tag/v0.2.0).

  | Image | Digest |
  |-------|--------|
  | `ghcr.io/otfabric/dnp3-interop-stepfunc:v0.2.0` (withdrawn, #19) | `sha256:ead1ea3c9a0f48ea363f745846ff2d5df8f077f50b6bb7f20914c3207ba5667e` |
  | `ghcr.io/otfabric/dnp3-interop-opendnp3:v0.2.0` | `sha256:a69ebb5962b334ff9813a1efbb871ccd5d296fad71bd13feb94bec52ef7ff19a` |
  | `ghcr.io/otfabric/dnp3-interop-dscsystems:v0.2.0` | `sha256:d0576cceabf24cbd072b672069226c30486aa84046dc98d813a5427a32dbae3a` |
  | `ghcr.io/otfabric/dnp3-interop-dnp3py:v0.2.0` | `sha256:14598aebcd5698d8f342d6d9beaa98b8618d724c57fbbe34222737678ceff0ef` |

- Done: a library consumes the images as R1.2 intends. otfabric/go-dnp3 pins the current release's images by digest and runs its interop tests against them with no checkout of this repository. Its results are its own (R6.2); nothing here depends on it.

**Gate:** published, digest-pinned images that a library's tests use without this repository. Met.

The stepfunc image was published from v0.1.0 to v0.4.2. Step Function I/O
asked for that to stop ([#19](https://github.com/otfabric/dnp3-interop/issues/19)):
the licence of their dnp3 crate (LICENSE.txt §2.2) does not permit
distributing the library, and the image has it compiled in; interoperability
testing with it is permitted. From v0.4.3 the release workflow builds and
self-tests the stepfunc image on both architectures but pushes only the
opendnp3, dscsystems and dnp3py images (`PUBLISHED_ADAPTERS` in the
Makefile), and the earlier stepfunc images are withdrawn. The adapter and its
Dockerfile stay here; each user builds the image locally (`make
image-stepfunc`), getting the crate from crates.io under that licence. A
library's tests that use stepfunc build it from the release tag instead of
pulling it (see [README.md](README.md#building-the-stepfunc-image)).

## Phase 5 - Additional adapters

### DSC Systems go-dnp3 (done)

Evaluation: both roles, v0.8.1 (GPL-3.0-or-later), an engine of its own; pure Go, so
both architectures build natively. The adapter is GPL-3.0-or-later.

- Go adapter, both roles, the ten client operations of contract 1.0 but
  `assign-class`. Its
  master is the one that sends DIRECT OPERATE NO ACK, which makes
  `directOperateNoAckServer` testable and now declared for every outstation.
- Declared false: all-objects reads and restart delays (master), ASSIGN
  CLASS (master), PARAMETER_ERROR with NOT_SUPPORTED (outstation). File
  transfer and secure authentication came with contract 1.1, in both roles.
- The outstation refuses fixtures with gaps in the indexes or without
  NEED_TIME at start; the adapter's listener lets a new connection replace
  the current one, as the contract requires.

**Gate:** smoke test and cross-stack self-test for the roles it declares. Met
on linux/arm64 (2026-10-20): 32 of 32 smoke checks; the three-adapter run has
789 checks passed, none failed, 14 known-bug hits, 81 skipped. Three upstream
bugs of DSC go-dnp3 v0.8.1 are listed in `tests/known-bugs.tsv` (FREEZE AND CLEAR
of a counter without frozen counter, freeze events for unchanged values, link
address filtering). Details in [docs/COMPATIBILITY.md](docs/COMPATIBILITY.md).

### dnp3py (done, both roles)

Pin dnp3py 0.6.0 (PyPI, commit `c00d5df2`, MIT). Its outstation cannot serve
the baseline fixture (no double-bit inputs, octet strings or per-point
variations; analog inputs always g30v1; no analog output events; the TCP
runner does not send unsolicited responses). It serves the `dnp3py` fixture,
the baseline within those limits, which every other outstation serves too:
`FIXTURE=dnp3py scripts/interop.sh` (part of `make interop`) runs
tests/cases-dnp3py.tsv for every master against all four outstations and
checks that they agree. Two dnp3py behaviours are known bugs there: a range
read returns every point of the type, and a control of a point that does not
exist is answered without PARAMETER_ERROR.

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
19 known-bug hits, 117 skipped (master role, v0.1.0). The outstation role
followed in v0.3.0 with the `dnp3py` fixture.

### Triangle MicroWorks DNP3 SCL (on hold)

**On hold** since 2026-10-20, by decision of the owner of this repository:
nothing is built, published or tested for it, and no work is planned until
the hold is lifted.

The ANSI C DNP Source Code Library
(https://products.trianglemicroworks.com/products/source-code-libraries/dnp-scl-pages/ansi-c-source-code)
is licensed per customer and not public. When the hold is lifted, the
adapter would build from an archive supplied at build time
(`adapters/tmw/vendor/`, already ignored by git and by the Docker build
context), verified against a SHA-256 (to be added to `versions.yaml` when the hold is
lifted). What has to
be settled first is how the archive reaches local builds and CI (a private
repository, a build secret, a self-hosted runner) without putting it in this
public repository or in a public image.

## Phase 6 - Expanded coverage (container contracts 1.1 to 1.3)

Each part enters the contract when at least two adapters can exercise it against
each other, or one adapter in both roles (REQUIREMENTS.md R8.3). Contract 1.1
adds, all as additions to 1.0:

- **TLS**: server `--tls-cert/--tls-key/--tls-ca`, client `--tls` with `--tls-ca/--tls-cert/--tls-key` and `--tls-server-name`; mutual, as every library requires. Test certificates are made per run (`scripts/test-pki.sh`), never baked in. stepfunc, opendnp3 (built with OpenSSL), dscsystems.
- **UDP**: `--transport udp`, server `--udp-peer`, client `--udp-local-port`. stepfunc, opendnp3, dscsystems.
- **Serial**: `--transport serial --serial-device` with line settings; tested over a socat pseudo-terminal line bridged to TCP (socat is in the images). stepfunc, opendnp3, dscsystems.
- **File transfer (g70)**: the `files` fixture; operations `file-get`, `file-put`, `file-ls`, `file-info`, `file-delete`; server event `file`; error `file-error` and `fileStatus`; features `fileServer`, `fileClient`, `fileDeleteClient`. dscsystems both roles, stepfunc master (no delete).
- **Secure authentication v5 (g120)**: `--sa-update-key`, `--sa-user`, `--sa-mac`; server events `auth` and `auth-error`; error `auth-failed`; `secureAuthentication` in the capability document. dscsystems in both roles; a second stack only with Triangle MicroWorks, which is on hold.

**Gate:** `make features` passes for every pairing that declares a feature, and smoke and cross-stack stay green.

**Status:** contract 1.1 done, released as v0.2.0: `make features` 191 passed, 0 failed (27 skipped for features a pairing lacks) locally and in the release qualification on both architectures.

### Contract 1.2: more point types (done, v0.3.0)

- The `extended` fixture with frozen analog inputs (g31/g33), command events of the outputs (g13/g43) and a g50v4 object; object documents with `status` and `units`; features `commandEventsServer`, `commandEventsClient`, `frozenAnalogInputsClient`, `timeAndIntervalClient`.
- Served by DSC go-dnp3 only (no other outstation has them); read by the stepfunc and DSC go-dnp3 masters (g31, g13/g43), the opendnp3 master (g13/g43, g50v4).

**Gate:** `make features` covers each type for every master that declares it, against the DSC go-dnp3 outstation.

### Contract 1.2: dual end point (done, v0.3.0)

- `server --connect HOST:PORT` (the outstation dials) and `client --listen` (the master listens), over TCP and TLS; features `dualEndpointServer`, `dualEndpointClient`, `dualEndpointClientTls`.
- stepfunc, opendnp3 and DSC go-dnp3 in both directions; stepfunc's master listens over TCP only.

**Gate:** `make features` runs an integrity poll and a control with the connection reversed for every pairing that declares it.

### Contract 1.2: security statistics (done, v0.3.0)

- g121/g122 object documents, `--sa-statistics-class` and `--sa-statistics-threshold`, features `securityStatisticsServer` and `securityStatisticsClient`.
- DSC go-dnp3 in both roles (its master decoded by the adapter); no other stack has them.

**Gate:** `make features` reads g121 and the g122 events after a refused key change, and checks the refusal of g121 without secure authentication.

### Contract 1.3: device attributes (done, v0.4.0)

- The `attributes` fixture (attribute set 1), `attribute-read` and `attribute-write`, the `attribute-write` event, features `deviceAttributesServer` and `deviceAttributesClient`.
- stepfunc and DSC go-dnp3 in both roles; opendnp3 and dnp3py have no group 0.

**Gate:** `make features` reads, writes and refuses attributes for every pairing that declares them.

### Contract 1.3: several controls in one request (done, v0.4.0)

- `operate-multi` with `commandStatuses`, the fixture's `maxControlsPerRequest` and the `controls` fixture; features `multipleControlsClient`, `maxControlsServer`.
- Every master sends them, every outstation carries them out; dnp3py's outstation ignores the limit.

**Gate:** `make features` checks success, a partial refusal, select-before-operate and the limit for every pairing.

### Contract 1.3: freeze at time and analog freeze (done, v0.4.0)

- `freeze --at immediately|+MS|EPOCH_MS [--interval-ms N]` (FREEZE AT TIME with g50v2) and `freeze --group 30`; features `freezeAtTimeServer/Client`, `analogFreezeServer/Client`.
- FREEZE AT TIME: stepfunc and DSC go-dnp3 outstations, stepfunc, DSC go-dnp3 and dnp3py masters. Analog freeze: DSC go-dnp3's outstation; stepfunc, opendnp3 and dnp3py masters.

**Gate:** `make features` freezes at once and at a time, and freezes analogs, for every pairing that declares it.

### Contract 1.3: analog deadbands (done, v0.4.0)

- `deadband-write --deadbands INDEX:VALUE,...` (WRITE of g34v3) and group 34 in object documents; features `deadbandWriteServer/Client`, `deadbandReadServer/Client`.
- Write: stepfunc and DSC go-dnp3, both roles. Read: stepfunc, both roles.

**Gate:** `make features` writes a deadband, sees a smaller change held back and a larger one reported, and reads it back, for every pairing that declares it.

### Contract 1.3: file append and authentication (done, v0.4.0)

- `file-put --append`, `file-authenticate`, `--file-user`/`--file-password` on the file operations and on the server; file events `append` and `authenticate`; features `fileAppendServer/Client`, `fileAuthServer/Client`.
- Outstation: DSC go-dnp3 only (the only file server). Masters: stepfunc (both), DSC go-dnp3 (authentication).

**Gate:** `make features` appends and reads back, and authenticates, refuses and accepts, for every pairing that declares it.

### Contract 1.3: event buffer overflow (done, v0.4.0)

- The fixture's `outstation.eventBufferSize` and the `events` fixture; feature `eventBufferServer`. All four outstations.

**Gate:** `make features` overflows the buffer of every outstation and sees IIN2.3 and the events lost.

### Contract 1.3: link layer (done, v0.4.0)

- `--link-confirms` (client and server), `client --keep-alive-ms`, the server event `link-status`; features `linkConfirmsServer/Client`, `keepAliveClient`, `linkStatusEventServer`.
- Confirms: DSC go-dnp3 both roles. Keep-alive: stepfunc, opendnp3 and DSC go-dnp3 masters; DSC go-dnp3's adapter reports them.

**Gate:** `make features` polls with confirms both ways, and keeps a link alive and sees the link status requests, for every pairing that declares it.

### Contract 1.3: broadcast (done, v0.4.0)

- `operate --mode direct-no-ack --broadcast`, the server event `broadcast`, IIN1.0 in `iin`; features `broadcastServer`, `broadcastEventServer`, `broadcastClient`.
- Sent by DSC go-dnp3's master; carried out by the stepfunc, opendnp3 and DSC go-dnp3 outstations.

**Gate:** `make features` broadcasts a control and sees it carried out, IIN1.0 and the server's event, for every outstation that declares it.

### Contract 1.3: management functions (done, v0.4.0)

- `manage --function initialize-data|initialize-application|start-application|stop-application|save-configuration`, the server event `manage`; features `manageServer`, `manageClient`.
- Served by DSC go-dnp3's outstation, sent by opendnp3's master. ACTIVATE CONFIG (g70v8 → g91) is dropped: no reference master sends it.

**Gate:** `make features` sends each function and sees the server's event, and a refusal from an outstation without them.

### Contract 1.3: several outstations (done, v0.4.0, fixed in v0.4.1)

- `server --outstations N`; feature `multipleOutstationsServer`. opendnp3 and DSC go-dnp3. The master listener that picks outstations by link address is dropped: only stepfunc's master can.

**Gate:** `make features` polls each of two outstations on one port by its address, sees their databases apart, and no answer from a third address.

## Phase 7 - Release qualification

- Every check of the smoke and cross-stack self-tests is recorded (`results-*.tsv` beside the documents when `REPORT_DIR` is set); `scripts/compat-report.py` turns them and the capability documents into a compatibility report: adapters, totals, a client × server matrix, failures, known-bug hits, skips by reason, declared features.
- `scripts/qualify.sh` scans every published image of a release from the registry (from v0.4.3 opendnp3, dscsystems and dnp3py; the stepfunc image is not published) with Trivy 0.76.0 (pinned by digest), for linux/amd64 and linux/arm64, and writes a CycloneDX SBOM of each and a summary (`vulnerabilities.md`).
- The **Release qualification** workflow (`.github/workflows/qualify.yml`) runs both against a published release, natively on both architectures (the self-test builds the unpublished stepfunc image from the release tag), and attaches the results to the GitHub release; the release workflow calls it after publishing.

**Gate:** a release carries its compatibility report, scan summary and SBOMs, produced from the published images.

**Status:** done for v0.1.0 (release run [39342042741](https://github.com/otfabric/dnp3-interop/actions/runs/39342042741); the first qualification run attached empty SBOM archives, fixed in the workflow). The v0.1.0 release carries `compatibility-report.md`, `vulnerabilities.md`, `sboms.tar.gz` and `vulnerability-scans.tar.gz`.

- Scan: every image 0 critical and 43 high findings, all in the Debian 13 base and nearly all without a fix; the fixable ones are liblzma5 (waiting for a new `debian:trixie-slim` digest) and, in the dnp3py image only, pip's vendored setuptools, urllib3 and msgpack (pip and setuptools are removed from the image since v0.1.0).
- Self-test of the v0.1.0 images: 1113-1115 checks passed per architecture, with 1-2 failures per run, all in pairings with the dscsystems outstation (`confirmed events are gone`, `time-sync`, `direct operate no ack operates`). They are the adapter defects fixed after v0.1.0 (the masters closed before their last confirm or request was out; the opendnp3 master judged a time synchronization by an empty READ that DSC go-dnp3 refuses; docs/COMPATIBILITY.md). Two consecutive local runs of main pass with no failure. The gate for the next release is a clean qualification run.

**v0.2.0** (contract 1.1; release run [39355077580](https://github.com/otfabric/dnp3-interop/actions/runs/39355077580)) met that gate: qualification on both architectures with 0 failures (smoke 40-43 per adapter, cross-stack 1113-1115 passed with 18-20 known upstream bugs, contract 1.1 features 191 passed). Scan: 0 critical; 43 high (44 for dnp3py), all in the Debian 13 base; 2 fixable per image (liblzma5, waiting for a new `debian:trixie-slim` digest).

**v0.3.0, v0.4.0 and v0.4.1** were qualified the same way by their release
runs ([39408872680](https://github.com/otfabric/dnp3-interop/actions/runs/39408872680),
[39443257730](https://github.com/otfabric/dnp3-interop/actions/runs/39443257730),
[39454037668](https://github.com/otfabric/dnp3-interop/actions/runs/39454037668)).
Each release carries its numbers in `compatibility-report.md`.
