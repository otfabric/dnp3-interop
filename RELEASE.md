# dnp3-interop Releases

## How a release is made

1. Write the notes of the version below and set `interop_version` in `versions.yaml` to it, in the same change. `make validate` (and CI) fails when RELEASE.md has no notes for `interop_version`.
2. `make ci` on the commit to release.
3. Run the `Release` workflow on `main` from the Actions tab and pick the bump (`patch` by default, `minor` or `major`) that gives `vX.Y.Z`, or push the tag `vX.Y.Z` yourself. The release workflow runs one job on an amd64 runner and one on an arm64 runner: each validates (including the notes of the tag), builds every image natively with `ADAPTER_VERSION` set to the tag, runs the smoke and cross-stack self-tests on those images and pushes exactly those of the published adapters (opendnp3, dscsystems, dnp3py, cbsigma, nfmdnp3: `PUBLISHED_ADAPTERS` in the Makefile) to GHCR as `<image>:vX.Y.Z-amd64` and `-arm64`. A last job joins the two into the multi-arch `<image>:vX.Y.Z` and creates the GitHub release with `manifest.json` (upstream pins, the digests of the multi-arch images, and under `localOnly` the dnp3c pin and how to build it) and `checksums.txt`.
4. The release workflow then runs the **Release qualification** workflow on what it published: the smoke and cross-stack self-tests again, natively on both architectures, against the images in the registry; a compatibility report from their documents; a Trivy vulnerability scan and a CycloneDX SBOM of every published image and architecture. It attaches `compatibility-report.md`, `vulnerabilities.md`, `sboms.tar.gz` and `vulnerability-scans.tar.gz` to the release. Run it by hand (Actions → Release qualification) to qualify an earlier release again; vulnerability findings are recorded, not gated on.
5. Consumers pin the digests from `manifest.json`, and build the dnp3c image from the tag (`make image-dnp3c VERSION=vX.Y.Z`).

The notes are the body of the GitHub release, followed by the image digests
and how to build the dnp3c image: everything after the heading
`## vX.Y.Z` up to the next line that is exactly `---`
(`scripts/release-notes.sh vX.Y.Z` prints it). This is the format of every
otfabric repository and the rule of the shared release workflow, so: one
section per version, newest first, each ending with a `---` line, and no
`---` line inside a section.

Tags are immutable. A fix is a new version.

The `dnp3-interop-otfabric` image is not part of these releases. It has its own
manual workflow and no releases (`own/go-dnp3/README.md`).

| What changed | Version |
|--------------|---------|
| Contract or fixture format changed incompatibly | major |
| New operation, behaviour, fixture, adapter or document field | minor |
| Upstream pin, base image or bug fix with unchanged contract | patch |

---

## v0.12.1

**Date:** 2026-11-12
**Previous release:** v0.12.0

Container contract 1.5, fixture format 1.0 and every upstream pin are unchanged. A patch release: every gosec v2.30.0 finding in the Go code (`gosec -tests ./...` in each module) is fixed in the code, with no exclusions and no `#nosec`. Before: cbsigma 74, dscsystems 63, own/go-dnp3 85 (on macOS), and 2 more in own/go-dnp3's Linux-only serial code; after: 0 in each module.

### Added

- **gosec on every Go module** (cbsigma, dscsystems, own/go-dnp3), tests included: `make lint-go`, part of `make lint` and of the CI step **Shellcheck, hadolint, ruff, bandit and gosec**. gosec v2.30.0 runs in the golang image of the adapters, pinned in the Makefile (`GOSEC_VERSION`) and in `versions.yaml` (`tooling.gosec`), which `scripts/check-versions.sh` checks. No exclusions, no `#nosec`.

### Fixed

- **Integer conversions (G115)**: flag, fixture and length values reach the protocol fields through checked conversions (`convert.go` in each module) instead of plain conversions. Every value was already bounded by the flag or fixture checks, so no input that worked before fails now; a bound that ever disagreed with a field's width is a usage error (exit 2) for a flag and an invalid fixture (exit 4) for a fixture, instead of a truncated value. A file of more than 4 GiB, which only a master could write, is an error of its size instead of a truncated size (cbsigma: the error of the store; own/go-dnp3: file status FATAL). The control handlers check the 32-bit index of a request against the 16 bits of a fixture index once, before looking the point up; the answers are unchanged. The dscsystems adapter reads the indexes of a start-stop range in 16 bits without wrapping and the 48-bit time of a security statistic directly into an int64; the own image reads a 48-bit attribute time the same way.
- **Paths (G304, G703)**: `print-fixture` opens `<name>/fixture.json` in an `os.Root` of `/fixtures`, which the name cannot leave even through a symbolic link; `--fixture` is cleaned (`filepath.Clean`). The dscsystems adapter writes the fixture's files into its served tree through an `os.Root` of that tree, and removes the tree when that fails. The Go tests read the repository's fixtures through an `os.Root` of `fixtures/`.
- **File modes (G301, G306)**: the readiness file is written with mode 0600 instead of 0644; it is empty and only its existence counts (`test -f`, by the image's own user). The tests' temporary files are 0600. The files and directories the dscsystems outstation serves keep 0644 and 0755, now named constants (`servedFileMode`, `servedDirMode`): the library reports those bits to the master, and the `[files] file-info` check wants `rw-r--r--`. gosec flags a named constant mode in `os.WriteFile` and `os.MkdirAll` as well as a later `os.Chmod`; it does not check the modes of `os.Root`'s methods, through which the tree is written.
- **Unsafe (G103, own/go-dnp3)**: the serial line's termios ioctl goes through `golang.org/x/sys/unix` (`IoctlSetTermios`, BSD-3-Clause, now in THIRD_PARTY_NOTICES.md) instead of `syscall.Syscall` with an `unsafe.Pointer`; the line settings are unchanged.

### Docs

- CONTRIBUTING.md: the Go style rule (gosec-clean, no `#nosec`, checked conversions, `os.Root`).
- README.md, CONTRIBUTING.md, SECURITY.md: gosec in `make lint` and in the static checks of CI.

---

## v0.12.0

**Date:** 2026-11-11
**Previous release:** v0.11.2

Container contract 1.5 and fixture format 1.0 are unchanged. A minor release: the cbsigma adapter moves to a new upstream version, and every image to new base images.

### Changed

- **cbsigma: cB-Sigma dnp3 v0.3.1** (commit `91f2f33c`), with cB-Sigma iotsec v0.2.0 (commit `c84d6be1`). v0.3.1 changes the four behaviours of v0.3.0 the self-tests reported (cB-Sigma/dnp3#2 to #5): FREEZE AND CLEAR now makes counter events; a repeated READ, and a request on a new connection, get a new response instead of the cached one; GET_FILE_INFO answers with the base name of the file; idle keep-alives come at the keep-alive interval. Their four rows of v0.3.0 have left `tests/known-bugs.tsv`. The GET_FILE_INFO report was mistaken: v0.3.0 answered with the name of the request, as IEEE 1815-2012 4.4.18 asks ("filled in with the same data as in the request"), and v0.3.1 deviates from it; that is now a row of v0.3.1 (see Fixed). v0.3.1 adds no API, so the capabilities of the adapter are unchanged. iotsec v0.2.0 only adds implementations of its interfaces; cB-Sigma dnp3 uses none of them.
- **Why the v0.3.1 candidate failed** (Upstream workflow, #77): two rows of `tests/known-bugs.tsv` record behaviour of the outstation that differs from the contract but not, as far as could be established, from IEEE 1815-2012 (FREEZE AT TIME with a time already past is done at once; the class 0 response with secure authentication includes the g121 statistics). A row names one upstream version, and these named v0.3.0, so on v0.3.1, which keeps both behaviours, five feature checks failed. Both rows now name v0.3.1, with links into its source; the notes of the Upstream issue and CONTRIBUTING.md say to carry such rows over with a pin.
- **Base images**: `golang:1.27.4-trixie` (from 1.27.2, whose standard library has GO-2026-6809, in crypto/x509, which govulncheck finds reachable in the cbsigma and dscsystems adapters, and GO-2026-6859, in net/http, which none reaches), `python:3.14.10-slim-trixie` (from 3.14.8) and a new `debian:trixie-slim` digest. govulncheck finds nothing in the three Go modules with Go 1.27.4.
- **Debian packages are pinned**: the Debian stages install from the snapshot of the Debian archive their base image was built from (`DEBIAN_SNAPSHOT`, 20261104T212339Z) at fixed versions, mirrored in `versions.yaml` (`debian_snapshot`, `debian_packages`) and checked by `scripts/check-versions.sh`.
- **Dockerfiles**: `USER 10001` (the uid of the contract) instead of the user name, the HEALTHCHECK in exec form, and `pipefail` for every `RUN` with a pipe. The images behave as before.
- **Own image (`own/go-dnp3`, not in the release): go-dnp3 v1.12.0** (from v1.6.0) in `go.mod`. v1.12.0 adds caller-supplied dialers and a redial jitter and fixes the 32-bit build, with no protocol change; `go fix` modernised the adapter's code (no change in behaviour). The changes below came with v1.11.0. The master turns off the new startup integrity poll (`client.WithStartup(client.Startup{})`) and keeps automatic time synchronization off, so it still does nothing on its own but clear IIN1.7. The outstation's frozen event policy is now `always`: since v1.11.0 a freeze raises a frozen event also for an unchanged value (IEEE 1815-2012 11.9.5.2), and `--frozen-event-policy on-change` is a usage error. `--sa-statistics-threshold` takes 1..65535, the range of IEEE 1815-2012 7.6.1.4.2, which go-dnp3 now enforces. The security statistics carry Association ID 0, as DSC go-dnp3's do, so that permitted difference is gone; GET_FILE_INFO is answered with the requested name (4.4.18), which the `[files] file-info` check now asks for.
- **Policy 1.5**: `.otfabric/policy.yaml`, the policy workflow (`otfabric-policy@v1.5.0`) and AGENTS.md move from otfabric/engineering policy 1.2 to 1.5. For the interoperability profile, 1.3 adds GO-014 (MAY; no golangci-lint here) and the review rule TEST-R01 (SHOULD), 1.4 adds DOC-008 (not applicable to the profile), 1.5 adds API-R03 (not applicable). The checker reports the repository compliant.

### Added

- **Static checks in CI**, every tool pinned by version and digest and listed in `versions.yaml` (`tooling`): hadolint on every Dockerfile, shellcheck on every shell script at its default severity (instead of `warning`), ruff and bandit on the Python files (`make lint`), and govulncheck on the cbsigma, dscsystems and own/go-dnp3 modules in the golang image of the adapters (`make vulncheck`). The Python packages of the workflows and of `make validate` are pinned with their hashes (`scripts/requirements.txt`, `scripts/requirements-lint.txt`), and so is the Python image of the Makefile's fallback.
- **Tests of the tooling**: bats tests of the helpers of `scripts/lib.sh`, of `scripts/check-versions.sh` (each kind of pin changed in a copy of the build files) and of `scripts/check-known-bugs.sh` (`make test-scripts`); Go tests and fuzz targets of the Go adapters' own parsing (fixtures and command lines in both, the link frames the dscsystems adapter sniffs), seeded from `fixtures/` and `testdata/fuzz/` (`make test-go fuzz`). The CI job **Go tests, fuzzing, govulncheck** runs them.
- **Fault checks in the smoke test**: every outstation gets line noise and a frame with a bad header CRC, a frame cut off in its user data, and a connection reset with its answer unread, and must still run and answer an integrity poll afterwards.
- **`scripts/check-known-bugs.sh`**: fails when an upstream issue that `tests/known-bugs.tsv` cites is closed. The CI validate job runs it; `make validate` skips it without `GH_TOKEN`.
- **`EXTRA_KNOWN_BUGS`** for `scripts/interop.sh` and `scripts/features.sh`: a further table of known bugs, in the format of `tests/known-bugs.tsv`, for an image outside the release. `own/go-dnp3/known-bugs.tsv` has the one bug that shows only against the own image: opendnp3's master takes the g52v2 delay that IEEE 1815-2012 4.4.12 asks for in the answer to SAVE CONFIGURATION as a failure (`PerformFunction`, `EmptyResponseTask`). A row in `tests/known-bugs.tsv` would be stale in every release run.

### Fixed

- **`scripts/check-versions.sh`** could stop half-way, now and then: `sed ... | head -1` under `pipefail` ended with SIGPIPE when an `ARG` appears twice in a Dockerfile. sed now stops at the first match itself.
- **Feature checks that asked more than IEEE 1815-2012**, found with the own image on go-dnp3 v1.11.0: the `[sa]` checks leave out the Authentication Failures statistic event that Table 7-6 has the outstation report at every session key change, and no longer require error 11 for an unknown user, whose key status request A.45.5 answers with the key status AUTH_FAIL; they ask for a statistics threshold of 65535, the most 7.6.1.4.2 allows, instead of 1000000. The contract (still 1.5) says so for `--sa-statistics-threshold`.
- **`[files] file-info` follows IEEE 1815-2012 4.4.18**: the filename offset, file name size, request ID and file name octets of the answer are filled in with the same data as in the request, so the check wants `/readme.txt file 43 rw-r--r--`; it took the last path element too. The agreement of the masters of one outstation compares the name as reported, no longer without its leading slash; for that, the cbsigma adapter's master reports the name of a descriptor as the outstation sent it, as the contract asks, instead of its last path element (the slash-stripping hid this), so every master now reports the same name. Two outstations answer with the last path element, now rows of `tests/known-bugs.tsv`: `cbsigma-file-info-base-name` (cB-Sigma dnp3 v0.3.1, `outfile.go`, `fileInfo`; v0.3.0 answered with the requested name) and `dscsystems-file-info-base-name` (DSC go-dnp3 v0.8.2, `outstation/file.go`, `onGetFileInfo`, with the name of its directory handler). The dnp3py outstation serves no files.
- shellcheck findings at the default severity (`scripts/features.sh`: a pattern expansion that was not quoted), ruff findings in the Python scripts and the dnp3py adapter (no change in behaviour); `scripts/latest-upstream.py` fetches https URLs only.

### Tooling

- Trivy 0.76.2 (from 0.76.0) for the release qualification.
- The shared `otfabric/.github` upstream actions v3.6.0 (from v3.1.1; their inputs are unchanged).
- The Upstream workflow's base-image check reports a digest that moved for the same tag, not a newer patch tag of the same minor: that check lives in `otfabric/.github`.

### Docs

- docs/COMPATIBILITY.md: cB-Sigma dnp3 v0.3.1 and what it changed; the own image is built from the latest go-dnp3 release when its workflow runs, not from a version pinned here, with the counts of a local run on go-dnp3 v1.11.0; its permitted differences (the association of security statistics and the GET_FILE_INFO name are no longer differences; two secure authentication behaviours are new); the GET_FILE_INFO name of cB-Sigma v0.3.1 and DSC go-dnp3 as known differences from 4.4.18; the management functions against the own outstation.
- own/go-dnp3/README.md: the master runs no startup poll and no automatic time synchronization; the frozen event policy `always`; the permitted differences; GET_FILE_INFO with the requested name.
- docs/CONTAINER_CONTRACT.md (contract still 1.5): the name in the `file-info` answer is the requested one (IEEE 1815-2012 4.4.18), and a last path element is a known difference; `--sa-statistics-threshold` is 1..65535 (7.6.1.4.2).
- SECURITY.md: v0.12.x supported; the static checks; Trivy sees the Go modules of the dscsystems and cbsigma binaries and the Python packages of the dnp3py and nfmdnp3 images.
- README.md, THIRD_PARTY_NOTICES.md, CONTRIBUTING.md: the new pins, targets and how to move the Debian snapshot.

---

## v0.11.2

**Date:** 2026-11-03
**Previous release:** v0.11.1

### Fixed

Self-test scripts and documentation only. Container contract 1.5, fixture format 1.0, the adapters and the reference stacks are unchanged.

- **`scripts/interop.sh`, unsolicited responses**: the outstation of the unsolicited checks waits 1 s for an application confirm, instead of 5 s. The opendnp3 outstation loses a CONFIRM that arrives together with another request while it is still sending a response. A master that confirms the null unsolicited response and enables unsolicited responses at once then got its events only after the confirm timeout, after the collect window had ended. This was an intermittent failure of dscsystems → opendnp3 (see docs/COMPATIBILITY.md; tracked in #76, fix on a fork as the repository is archived).
- **`scripts/features.sh`, UDP**: when Docker answers "Address already in use", it has already created the named container (`--name`). That container is now removed before the next attempt. Before this fix, the next attempt failed with a name conflict and the check reported `[udp] server did not start`.

---

## v0.11.1

**Date:** 2026-11-03
**Previous release:** v0.11.0

### Fixed

Self-test scripts only. Container contract 1.5, fixture format 1.0, the adapters and the reference stacks are unchanged.

- **`scripts/features.sh`, UDP**: a container with a fixed address (`--ip`) is started again, up to five times, while Docker answers "Address already in use". The address of a container just removed can be released a moment after `docker rm` or `docker run --rm` returns.
- **`scripts/features.sh`, secure authentication**: the refused attempts run in an order that does not depend on how many error messages the outstation still sends. The order is: unknown user; an authenticated read, which changes the session keys; wrong update key; master without authentication. cB-Sigma dnp3 stops sending error messages after two until a session key change succeeds. It answers a master without authentication with an error only while the user has no valid session keys.
- **`scripts/interop.sh`**: the NEED_TIME check reports `no IIN: <error>` for a result without IIN, instead of printing a jq error.

---

## v0.11.0

**Date:** 2026-11-03
**Previous release:** v0.10.0

## Summary

Two more reference stacks: cB-Sigma dnp3 v0.3.0 (Go, Apache-2.0, master and
outstation) and nfm-dnp3 1.0.1 (Python, MIT, master over TCP), both
published. Container contract 1.5 and fixture format 1.0 unchanged; the
existing adapters are unchanged.

## Changes

### Adapters

- **cbsigma** (`dnp3-interop-cbsigma`): both roles over TCP, UDP, serial and TLS, secure authentication v5, file transfer, device attributes (master), freezes and counter increments. Serves the baseline fixture.
- **nfmdnp3** (`dnp3-interop-nfmdnp3`): the master over TCP. Its defects are reported upstream (fxodell/dnp3py#1 to #10) and are known bugs here.
- `versions.yaml`, `scripts/check-versions.sh`, `scripts/latest-upstream.py`, the Release, Upstream and own-stack workflows and THIRD_PARTY_NOTICES.md cover both.

### Self-test

- **Event read of one group**: a READ of g32 returns only the analog input events; the binary input events stay buffered for the next event poll.
- `scripts/smoke.sh`: `SMOKE_SERVER_FIXTURE` picks the fixture of the server (the nfmdnp3 smoke runs against the `dnp3py` fixture); the counter-increment check expects a usage error on an image without `counterIncrementServer`.
- The value checks of `scripts/interop.sh` and the file agreement check of `scripts/features.sh` can be matched by a known bug.
- **Known bugs** (`tests/known-bugs.tsv`): cB-Sigma's cached answer to a repeated request, FREEZE AND CLEAR without counter event, GET_FILE_INFO name and keep-alive interval, and two differences from the contract; nfm-dnp3's defects; the opendnp3 master reading the g52v1 delay as milliseconds; the DSC master ignoring challenges that are not 32 octets.

### Docs

- README, CAPABILITIES, COMPATIBILITY (matrix and findings), COVERAGE, PLAN and THIRD_PARTY_NOTICES describe the five stacks.

---

## v0.10.0

**Date:** 2026-11-02
**Previous release:** v0.9.0

## Summary

Full coverage of what the three reference stacks support, and an image of OT
Fabric's own go-dnp3 for anyone to test against. Container contract 1.5,
additions only; fixture format 1.0 with additions (`pulse`, more attribute
types). The opendnp3, dscsystems and dnp3py adapters expose everything their
upstream supports that the contract covers, and the self-tests check every
declared feature in every pairing that can run it.

## Changes

### Contract 1.5

- **ACTIVATE CONFIG**: `manage --function activate-config`, features `activateConfigServer` and `activateConfigClient`. Served by DSC go-dnp3 (`Config.ActivateConfig`) and go-dnp3; sent by dnp3py and go-dnp3. opendnp3 and dnp3py's outstation answer NO_FUNC_CODE_SUPPORT.
- **OPERATE without SELECT**: `operate --mode operate`, feature `operateWithoutSelectClient`; every outstation that serves select-before-operate answers NO_SELECT and carries nothing out.
- **Confirms withheld**: `--no-confirm` (solicited responses) and `monitor --no-unsolicited-confirm`, features `noConfirmClient` and `noUnsolicitedConfirmClient`; the result field `unsolicitedFragments`.
- **Pulse outputs**: the fixture field `pulse`; PULSE_ON with count, on and off times and PULSE_OFF on output 2 of the `controls` fixture.
- **Attributes**: float, octet string, bit string and time attributes, with the matching `attribute-write` flags.

### Adapters

- **opendnp3**: serves time-and-interval points (g50v4); the master sends freezes without acknowledgement.
- **dnp3py**: the master decodes frozen analog inputs and command events, writes deadbands, sends keep-alives, broadcasts, management functions 15 to 19, device attributes, file transfer with append, and dual end point (not over TLS: dnp3py has no TLS channel).
- **dscsystems**: answers ACTIVATE CONFIG; `--confirm-timeout-ms` also sets the unsolicited confirm timeout.

### Self-test

- File append; FREEZE AT TIME with an interval, without acknowledgement and at a time already past; select timeout and NO_SELECT; events and unsolicited responses resent when not confirmed; DEVICE_RESTART and the restart, command and assign-class events; a control from another master address; a second connection replacing the first; a TLS certificate of another name; a non-default UDP local port and serial settings; an AES-256 update key; DISABLE UNSOLICITED by its effect; value checks on every fixture.
- **Known bugs** (`tests/known-bugs.tsv`, evidence pinned to commits): opendnp3's TLS master does not check the server name; DSC go-dnp3 and dnp3py carry out a control from another master address; dnp3py answers NO_SELECT, not TIMEOUT, after the select timer, and removes events when the response is built rather than on the confirm.

### Own stack

- **`dnp3-interop-otfabric`** (`own/go-dnp3`, go-dnp3 v1.6.0) declares every feature of contract 1.5. It is built by its own manual workflow from the latest go-dnp3 and is not part of this release; go-dnp3 runs its Self-interop workflow against it. Its permitted differences from the references (relative time while unsynchronized, the association of security statistics, the order of attributes) are in docs/COMPATIBILITY.md.

---

## v0.9.0

**Date:** 2026-11-02
**Previous release:** v0.8.0

## Summary

Removes the Step Function I/O dnp3 (stepfunc) adapter. Removed interop test
functionality against this stack, as requested by the maintainer of this
stack. Container contract 1.4 and fixture format 1.0 unchanged; the opendnp3,
dscsystems and dnp3py adapters and the dnp3c runner are unchanged.

## Changes

- **Removed**: the adapter's directory under `adapters/`, its Makefile targets and `compose.yaml` service, its entries in `versions.yaml`, `manifest.json` and the release notes, and its handling in the CI, release, qualification and Upstream workflows and in `scripts/`.
- **Self-test**: `ADAPTERS` is `opendnp3 dscsystems dnp3py`. The broadcast check of `scripts/features.sh` reads with the opendnp3 master.
- **Docs**: README, CAPABILITIES, COMPATIBILITY, COVERAGE, CONTAINER_CONTRACT, FIXTURES, CONFORMANCE, REQUIREMENTS, PLAN, CONTRIBUTING, SECURITY, THIRD_PARTY_NOTICES and AGENTS describe the remaining adapters; footnotes renumbered.

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

- **`counter-increment`**, a command run in the server's container, adds to a counter of the running outstation over `server --control-socket`. Feature `counterIncrementServer`, every adapter. The change goes through the library's own update, so its event is the library's. The opendnp3 adapter holds the counter values itself, as the library has no read of its database, and takes every FREEZE AND CLEAR for one of all counters.
- **`server --frozen-event-policy on-change|always`**, for a stack that can be set; a policy an image does not have is a usage error.
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

Two adapter capabilities: `directOperateNoAckClient` and `assignClassServer`.
Container contract 1.3 unchanged.

## Changes

### Docs

- CAPABILITIES, COMPATIBILITY, COVERAGE, CONTAINER_CONTRACT and README describe the two capabilities.

---

## v0.5.2

**Date:** 2026-10-30
**Previous release:** v0.5.1

## Summary

DSC go-dnp3 v0.8.2 and current base images. Container contract 1.3
unchanged.

## Changes

### Upstream pins

- **`dscsystems`**: DSC go-dnp3 v0.8.1 → v0.8.2 (commit `1fcec1d7881541d794fe257d2ede3ba46587b500`). The release adds DSC's own interop tests against these images; the library is unchanged, so its five entries in `tests/known-bugs.tsv` move to v0.8.2 as they are.
- **Base images**: new digests, among them `debian:trixie-slim` and `python:3.14.8-slim-trixie`.

---

## v0.5.1

**Date:** 2026-10-21
**Previous release:** v0.5.0

## Summary

The release manifest names the dnp3c runner. The images are unchanged.

## Changes

### Release

- **`manifest.json`** lists the dnp3c runner under `localOnly`: how to build it from the tag, and its dnp3c version. The release workflow fails if `images` names it. The release notes say how to build it.

### Docs

- README, THIRD_PARTY_NOTICES: the dnp3c image is pinned by building it from the tag. PLAN: phase 8, the dnp3c runner; every release is qualified.

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

The release publishes the images the Makefile's `PUBLISHED_ADAPTERS` lists.
Container contract 1.3 unchanged; no adapter code changed.

## Changes

- Release: every image is built and self-tested natively on linux/amd64 and linux/arm64; the opendnp3, dscsystems and dnp3py images are pushed. The Makefile's `PUBLISHED_ADAPTERS` (fixed, not overridable) drives `publish`, `publish-arch`, `publish-manifest`, `digests` and `qualify`; `ADAPTERS` drives `images`, `smoke`, `interop` and `features`.
- Release qualification: the vulnerability scan and the SBOMs cover the published images only.
- Docs: README (quick start with the opendnp3 and dscsystems images), THIRD_PARTY_NOTICES.md, REQUIREMENTS.md (R1.2, R5.3, new R7.5), PLAN.md, CONTRIBUTING.md, SECURITY.md, docs/COMPATIBILITY.md, docs/CONTAINER_CONTRACT.md, `adapters/LICENSE`, `versions.yaml` (`interop_version` 0.4.3).

---

## v0.4.2

**Date:** 2026-10-21
**Previous release:** v0.4.1

## Summary

The adapters follow the container contract where the adapter, not its
library, decides; container contract 1.3 unchanged. The contract now states
the rules and names only the remaining library differences.

## Changes

- opendnp3: TLS files are loaded up front: the client fails with `failed` and its result document (dialling or listening), the server exits 1 before `ready`; a listening master that cannot bind prints its document; `operate-multi` gives `commandStatuses` null on a partial echo; a control whose SELECT was echoed SUCCESS has status SUCCESS, not the library's initial UNDEFINED (a partial select echo was `command-failed` UNDEFINED, now `failed`); `print-fixture` refuses `\`; the capability document lists `fileAppend*`, `fileAuth*` and `deadband*` as false.
- dscsystems: attribute lists have value null; `file-info` refuses `--file-user`/`--file-password`; the master checks the length of `--name` (200 characters); TLS files that cannot be loaded give the complete result document; the server exits 1 without `stopped` when the session ends without SIGTERM/SIGINT; ACTIVATE_CONFIG (31) is refused and not reported as `manage`; a refused open for appending is reported as `append`; `visibleString` attributes are counted in characters, and one over 255 octets of UTF-8 is refused; the master gives the time of g31v3 and g31v4; `operate-multi` on a partial echo gives `commandStatuses` null and fails, `failed` when every echoed control succeeded (the library counted that as a success) and `command-failed` with a refusal among them; a dialling server that cannot open its channel names the address it dials; lint fixes (errcheck).
- dnp3py: accepts `--transport tcp` (`udp` and `serial` are usage errors); `operate-multi` reports the first refusal and the statuses of the last echo, and a partial echo fails with `commandStatuses` null; the server exits 1 without `stopped` when the runner ends without a signal; the capability document lists the file append/auth and deadband features (false) and no longer has `timeSyncOnDemand` in `serverFeatures`; `versions.yaml` gives it both roles; lint fixes (ruff); the client catches the exceptions dnp3py, the channel and the decoders raise instead of every exception.
- `scripts/validate-fixtures.py`: a `visibleString` attribute must also fit in 255 octets of UTF-8; ruff fixes in the scripts. `scripts/interop.sh` and `scripts/features.sh` expand arrays that can be empty in a way macOS's bash 3.2 accepts under `set -u`, and `scripts/features.sh` names its documents with `mktemp`, not `$BASHPID` (bash 4). `scripts/test-pki.sh` writes the EC keys with the named curve: LibreSSL (macOS's openssl) wrote explicit curve parameters, which the dscsystems adapter cannot load.
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

- Point types: the `extended` fixture with frozen analog inputs (g31/g33), command events of the outputs (g13/g43, with the status of the control) and time and interval (g50v4); object documents get `status` and `units`; features `commandEventsServer`, `commandEventsClient`, `frozenAnalogInputsClient`, `timeAndIntervalClient`. Served by dscsystems; read by the masters as each can.
- Dual end point: `server --connect HOST:PORT [--tls-server-name NAME]` (the outstation dials) and `client --listen [--bind-address ADDR]` (the master listens), over TCP and TLS; features `dualEndpointServer`, `dualEndpointClient`, `dualEndpointClientTls`. opendnp3 and dscsystems, both ways.
- Security statistics: with secure authentication on, dscsystems serves g121 and raises g122 events (`--sa-statistics-class`, `--sa-statistics-threshold`); object documents get `association`; features `securityStatisticsServer`, `securityStatisticsClient`.
- dnp3py has the server role: it serves the new `dnp3py` fixture (the baseline within its limits), which every outstation serves; `FIXTURE=dnp3py scripts/interop.sh` (in `make interop`) compares them. `serverFeatures` in the capability document gives the features of the server role where they differ.
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

- Transports: `--transport tcp|udp|serial`; TLS with server `--tls-cert`, `--tls-key`, `--tls-ca` and client `--tls`, `--tls-ca`, `--tls-cert`, `--tls-key`, `--tls-server-name` (mutual, TLS 1.2 and 1.3); UDP with server `--udp-peer` and client `--udp-local-port`; serial with `--serial-device`, `--baud`, `--data-bits`, `--parity`, `--stop-bits`. The ready event has `transport`. opendnp3 (now built with OpenSSL) and dscsystems have all three; the images with serial carry socat to bridge a pseudo-terminal to TCP.
- File transfer: the `files` fixture (fixture format: `files`, `directories`); operations `file-get`, `file-put`, `file-ls`, `file-info`, `file-delete`; result fields `file`, `files`, `fileStatus`; error `file-error`; server event `file`; features `fileServer`, `fileClient`, `fileDeleteClient`. dscsystems in both roles.
- Secure authentication v5: `--sa-update-key`, `--sa-user`, `--sa-mac`; error `auth-failed`; server events `auth` and `auth-error`; `secureAuthentication` in the capability document (MAC and key-wrap algorithms, critical function codes). dscsystems in both roles, tested against itself only.
- `scripts/features.sh` (`make features`, part of `make documents`): the cross-stack self-test of all of it; `scripts/test-pki.sh` makes the test certificates of a run. The compatibility report includes it.
- Adapters close only after their last confirm (all masters); the opendnp3 master judges a time synchronization by its library's result; dnp3py image without pip and setuptools.
- The release qualification (Trivy scan, SBOMs, compatibility report) attaches non-empty SBOM and scan archives.

---

## v0.1.0

**Date:** 2026-10-20
**Previous release:** none, first release

## Summary

First version: independent reference implementations as master and as
outstation behind container contract 1.0, dnp3py as master only.

## Changes

### Added

- **Container contract 1.0** — `server` (outstation), `client <operation>` (master), `print-capabilities`, `print-fixture`, `version`; JSON Lines server events; one result document per client operation; exit codes 0..4; readiness file and health check.
- **Fixture format 1.0** with JSON Schema, cross-field validation and the **baseline** fixture: 22 points of eight types in all three event classes, controls on binary and analog outputs with targets, select-required points and ranges, counters with frozen counters, an octet string, restart delays, NEED_TIME at start.
- **`opendnp3` adapter** — opendnp3 3.1.2 (commit `26b4c01e`), C++.
- **`dnp3py` adapter** — dnp3py 0.6.0, Python, client (master) role only; with DSC go-dnp3 the master that sends DIRECT OPERATE NO ACK, and the one that sends the freezes without acknowledgement.
- **`dscsystems` adapter** — DSC Systems go-dnp3 v0.8.1, Go, experimental upstream. GPL-3.0-or-later.
- **Client operations** — `integrity-poll`, `event-poll`, `read`, `operate` (g12v1, g41v1..v4; direct and select-before-operate), `time-sync` (LAN and non-LAN), `restart` (cold, warm), `enable-unsolicited`, `disable-unsolicited`, `assign-class`, `freeze` (with and without clear), `monitor`.
- **Self-tests** — `make smoke` (32 contract checks per server adapter, 28 for the master-only dnp3py) and `make interop` (48 cases over all client/server pairings requiring identical result documents, plus value checks of controls, events, freezes, unsolicited responses, class assignment and time synchronization, run as one suite per server adapter in parallel: 1114 checks on linux/arm64, none failed, 19 hits of known upstream bugs). CI builds the images and runs the smoke tests in parallel.
- **Schemas** for the capability and client result documents, validated against every document the self-tests produce.
- **`versions.yaml`** with every upstream pin and base image digest, checked against the build files; an upstream candidates workflow that resolves the latest release of every stack, self-tests the adapters whose pin is behind and writes one decision table.

### Upstream notes

- opendnp3 is archived; 3.1.2 is its last release. Its sources need `<cstdint>` included explicitly with GCC 14. Its master cannot send DIRECT OPERATE NO ACK or the `_NR` freezes and synchronizes time only when the outstation sets NEED_TIME; its outstation sets PARAMETER_ERROR along with a NOT_SUPPORTED command status, so such a refusal is `rejected` with no command status.
- DSC go-dnp3 v0.8.1: FREEZE AND CLEAR clears only counters that have a frozen counter; a freeze raises events for unchanged frozen values; neither its master nor its outstation filters link addresses (all three in `tests/known-bugs.tsv`). Its outstation carries out a restart (events discarded, DEVICE_RESTART and NEED_TIME set again), so restart cases run on a fresh outstation.

### For consumers

- Pin the images by digest from the release's `manifest.json`; read `print-fixture baseline` and `print-capabilities` from the image under test.

### Not in this release

- dnp3py as outstation.
- The Triangle MicroWorks adapter, which is on hold.
- UDP, TLS and serial links, file transfer, secure authentication.

---
