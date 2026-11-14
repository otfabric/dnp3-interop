# Contributing to dnp3-interop

## Ground rules

- **Nothing here may know about a library under test.** No imports, no build steps, no expectations, no names from `otfabric/go-dnp3` or any other consumer. See [REQUIREMENTS.md](REQUIREMENTS.md) R1, and `own/` for the one exception.
- **The contract is the product.** Adapters differ inside; their commands, documents and exit codes are identical. A change to [docs/CONTAINER_CONTRACT.md](docs/CONTAINER_CONTRACT.md) or [docs/FIXTURES.md](docs/FIXTURES.md) lands in every published adapter in the same change.
- **Declare only what is tested.** A capability is `true` when the self-tests exercise it for that adapter.
- **Pin everything.** Upstreams by commit or exact version, base images by digest, Debian packages by version from a snapshot of the archive, linters and test tools by version and digest, Python packages by hash; all mirrored in [versions.yaml](versions.yaml) or `scripts/requirements*.txt`.

## Setup

Docker (with buildx), `jq`, GNU make and Python 3. Nothing else: compilers, Maven and the Go and Python toolchains run inside the image builds, and the linters (shellcheck, hadolint, ruff, bandit), bats, the Go tests and govulncheck run in containers pinned in the Makefile.

```sh
make ci     # validate, lint, build, smoke, cross-stack and feature self-tests, document schemas
```

Run `make` for all targets.

## Changing an adapter

1. Edit under `adapters/<adapter>/`.
2. `make image-<adapter> smoke-<adapter>`; for the Go adapters also `make test-go fuzz`. Code that parses input of its own (fixtures, command lines, sniffed frames) has a fuzz target in `fuzz_test.go`, seeded from `fixtures/` and `testdata/fuzz/`.
3. `make interop`: the result documents of all pairings must stay identical. If two stacks disagree, find out which one is right by the standard before touching the expectation.
4. If behaviour visible to consumers changed, update the contract or fixture documentation in the same change.

Keep the `SPDX-License-Identifier` header of each adapter source: `MIT`, except `adapters/dscsystems/`, which links a GPL-3.0-or-later library and is `GPL-3.0-or-later`.

## Moving an upstream pin

1. Change the pin in the Dockerfile (and `requirements.txt`, or `go.mod` with `go mod tidy`) and in `versions.yaml`; `scripts/check-versions.sh` must pass. A row of `tests/known-bugs.tsv` names one upstream version: carry the rows whose behaviour the new version keeps over to it, and drop those it fixes.
2. `make ci`.
3. Record the new version, and anything it changed, in [docs/COMPATIBILITY.md](docs/COMPATIBILITY.md), [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) and [RELEASE.md](RELEASE.md).

The rule is the latest upstream release. A newer release that fails the self-tests is not adopted as the pin, but it is not dropped either: list its bug in [tests/known-bugs.tsv](tests/known-bugs.tsv), build it as a candidate (`make candidate-<adapter> CANDIDATE_VERSION=<version>`) and record in [docs/COMPATIBILITY.md](docs/COMPATIBILITY.md) what the older pin costs. When the candidate run reports that the known bug no longer reproduces, move the pin.

An entry may also cover a pinned version, when no adapter can avoid the bug and another check covers the behaviour it breaks. An entry that no longer reproduces is stale and fails the run, and so does, in the CI validate job, an entry whose upstream issue is closed (`scripts/check-known-bugs.sh`, which skips without `GH_TOKEN`). The columns are described in the header of [tests/known-bugs.tsv](tests/known-bugs.tsv).

Every Dockerfile names its pin `ARG UPSTREAM_VERSION` and `ARG UPSTREAM_SHA`. The **Upstream** workflow (`.github/workflows/upstream.yml`) resolves the latest release of every stack every day with `scripts/latest-upstream.py` and checks whether the tag of every digest-pinned base image has moved (a newer patch tag, such as `golang:1.27.5-trixie` for a pin on `1.27.4-trixie`, is not reported: that check lives in the shared `otfabric/.github` actions). It builds and self-tests each adapter whose pin is behind (once per pin and latest version; later runs reuse that result), writes one pin-vs-latest table and keeps a tracking issue open while a pin is behind. `make candidate-<adapter> CANDIDATE_VERSION=<version>` (opendnp3: also `CANDIDATE_SHA=<commit>`, since it is pinned by commit) runs the same locally. opendnp3 is archived: a commit after its last tag is not a release and is never a candidate.

## Base images and Debian packages

A base image moves with its digest in every Dockerfile that pins it and in `versions.yaml`. The Debian stages install their packages from the snapshot of the Debian archive the `debian:trixie-slim` image was built from (its `/etc/apt/sources.list.d/debian.sources` names it), at pinned versions, so that a build installs the same packages every time. With a new `debian:trixie-slim` digest, move `DEBIAN_SNAPSHOT` to the date in its `debian.sources` and the package versions to those of that snapshot (`apt-cache policy <package>` in the image after the snapshot `apt-get update`), in the Dockerfiles and in `versions.yaml`.

## Registries and retries

CI does not pull from Docker Hub: its anonymous rate limit and its token service fail too often on shared runners. The workflows set `BASE_REGISTRY=mirror.gcr.io/library` (base images, which the Dockerfiles pin by digest, so the mirror cannot change what is built) and `TOOLS_REGISTRY=mirror.gcr.io` (BuildKit, QEMU handlers, the linters, bats, Python). Locally both default to Docker Hub.

Every command that talks to a registry runs through `scripts/retry.sh`, which repeats it with a growing delay when the output shows a rate limit, a 5xx or a timeout, and returns any other failure at once. Use it for new registry or download steps instead of an action that cannot retry; `make publish-arch`, `make publish-manifest` and `make digests` are what the release workflow calls in place of the Docker push actions.

Both architectures are built and self-tested natively, on an amd64 and an arm64 runner; nothing in CI runs under emulation. `make buildx-setup` and `make buildx` remain for checking the other architecture from one machine.

## Adding a client operation or a server behaviour

1. Specify it first: the operation, its flags, when it succeeds and its result fields in the contract; the outstation behaviour in [docs/FIXTURES.md](docs/FIXTURES.md).
2. Implement it in every adapter whose upstream can do it; the others declare the feature false.
3. Add cases to [tests/cases.tsv](tests/cases.tsv) with the expected outcome, and a value check in `scripts/interop.sh` if the outcome line cannot show what matters; or, for a transport or optional feature, a check in `scripts/features.sh`.
4. Add the feature to the capability documents and to [docs/CAPABILITIES.md](docs/CAPABILITIES.md) and [COVERAGE.md](COVERAGE.md).
5. Extend the schemas in `schemas/` if documents gained fields.

Additive changes bump the contract's minor version; anything a consumer could break on bumps the major version.

## Proprietary upstream source

The Triangle MicroWorks adapter is on hold (see [PLAN.md](PLAN.md#triangle-microworks-dnp3-scl-on-hold)); when it resumes, it builds from a source archive supplied at build time. Never commit that source, a part of it or anything derived from it beyond the adapter's own code; `adapters/tmw/vendor/` is ignored by git and by the Docker build context for that reason.

## Adding a fixture

See [docs/FIXTURES.md](docs/FIXTURES.md#adding-a-fixture).

## Adding an adapter

1. Evaluate the upstream: does it build from a pinned source on both architectures, which roles does it provide, what is its licence, and is its protocol engine its own? Write the answers into [PLAN.md](PLAN.md) before writing code.
2. Create `adapters/<name>/` with a Dockerfile built from the repository root, following the existing ones: pinned build stage, minimal runtime stage, non-root user, `/fixtures`, `/licenses`, the readiness file, the health check, `ENTRYPOINT ["/usr/local/bin/dnp3-interop"]`.
3. Implement the contract. For a role the upstream does not support, the command exits 2 with a message and `roles` says `false`. What the upstream cannot do within a role is a feature declared `false`, and the cases that need it name it in their `needs` column: never fake a behaviour the library does not have.
4. `scripts/smoke.sh <name>` must pass, then `scripts/interop.sh opendnp3 dscsystems dnp3py <name>`.
5. Add it to `ADAPTERS` in the Makefile, to the workflows, `versions.yaml`, `scripts/check-versions.sh`, the notices and the documentation tables. Add it to `PUBLISHED_ADAPTERS` as well only if its upstream's licence permits distributing the library in an image; otherwise it is built and self-tested like the others but never pushed, and the README says how to build it.

Do not build a shared adapter framework. Adapters in different languages share a contract, not code.

## Style

- Shell: `bash`, `set -uo pipefail`, shellcheck-clean (`make lint`). No associative arrays or other bash 4+ features in code paths macOS's `/bin/bash` could run.
- C++: C++17, `-Wall -Wextra -Werror` on the adapter's own sources.
- Go: `gofmt`, `go vet`. gosec findings are fixed in the code. gosec does not run in this repository's public lint or CI (otfabric/engineering policy GO-014, ADR-0011): before each release a maintainer scans on their own machine with the `sast` target of the otfabric/engineering Makefile, run in that repository with `REPO=dnp3-interop`, and accepted findings are kept in a private repository. A finding is fixed in the code: no `#nosec`, no exclusions. Fixture, flag and length integers reach a protocol field through the checked conversions of `convert.go`, which fail with the contract's exit code (2 for a flag, 4 for a fixture) instead of truncating; a fixture name, or a file name of a fixture, is opened through an `os.Root`, which it cannot leave.
- Python: `python -I`, standard library only besides the upstream package.
- Documentation states what was verified. If something is assumed or untested, say so.

## License

By contributing you agree that your contributions are licensed as the part of the repository they touch: MIT, or GPL-3.0-or-later under `adapters/dscsystems/`.

## When CI runs

| Event | Workflow | What runs |
|---|---|---|
| Pull request | `pr.yml` | `policy.yml` (otfabric-policy) always; `ci.yml` unless every changed file is documentation; then `OT Fabric Required Checks` |
| Push to a branch | none | nothing; dispatch `ci.yml` to test a branch without a pull request |
| Push to `main` | `policy.yml` | the light checks; the merged commit was tested as the pull request's merge commit |
| Schedule | `ci.yml`, `upstream.yml` | the scheduled runs, on `main` |
| Release | `release.yml` | the release workflow tests the release commit again |

Documentation means an added or modified `*.md` file outside `.github/`, `testdata/` and `vendor/`, other than `AGENTS.md` and `CLAUDE.md`. A deleted or renamed document, or any other file, runs everything. `OT Fabric Required Checks` fails unless every job that applies succeeded; its summary lists them.
