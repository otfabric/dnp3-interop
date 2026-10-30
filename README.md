# dnp3-interop — DNP3 (IEEE 1815) Interoperability Testing with Docker

A Docker-based interoperability testing framework for DNP3 (IEEE 1815),
providing independent master and outstation implementations, deterministic
fixtures, and automated protocol compatibility testing.
Test DNP3 libraries against established implementations including Step
Function I/O dnp3 and opendnp3. Reference implementations are independently
containerized, enabling reproducible testing across platforms without
integrating third-party protocol stacks into production applications.

| Adapter | Implementation | Language | Server (outstation) | Client (master) | Image |
|---------|----------------|----------|:------:|:------:|-------|
| `stepfunc` | [Step Function I/O dnp3](https://github.com/stepfunc/dnp3) 1.8.0 | Rust | yes | yes | not published: build it locally, `make image-stepfunc` ² |
| `opendnp3` | [opendnp3](https://github.com/dnp3/opendnp3) 3.1.2 | C++ | yes | yes | `ghcr.io/otfabric/dnp3-interop-opendnp3` |
| `dscsystems` | [DSC Systems go-dnp3](https://github.com/dscsystems/go-dnp3) v0.8.2 (experimental) | Go | yes | yes | `ghcr.io/otfabric/dnp3-interop-dscsystems` |
| `dnp3py` | [dnp3py](https://github.com/craigpnnl/dnp3py) 0.6.0 | Python | yes ¹ | yes | `ghcr.io/otfabric/dnp3-interop-dnp3py` |

² The stepfunc image is not published, at Step Function I/O's request: the
licence of the Step Function I/O dnp3 crate forbids distributing it, and the
image has the crate compiled in. Build it yourself from this repository
(`make image-stepfunc`, or
`docker build -f adapters/stepfunc/Dockerfile -t ghcr.io/otfabric/dnp3-interop-stepfunc:dev .`);
the build fetches the crate from crates.io, under its licence, for you. See
[Building the stepfunc image](#building-the-stepfunc-image).

The stacks do not all do the same: the opendnp3 master sends no `_NR`
function, opendnp3's master synchronizes time only when the outstation asks for
it, DSC go-dnp3's master reads only ranges and classes, and dnp3py's
outstation (¹) serves only the `dnp3py` fixture, the baseline within its
limits. Each image says what it supports (`print-capabilities`); see
[docs/CAPABILITIES.md](docs/CAPABILITIES.md).

Container contract 1.0 covers DNP3 over TCP. Contract 1.1 adds transports,
file transfer and secure authentication:

| | stepfunc | opendnp3 | dscsystems | dnp3py |
|---|:-:|:-:|:-:|:-:|
| TLS (mutual), UDP, serial | yes | yes | yes | no |
| File transfer (g70) | master (no delete) | no | both roles | no |
| Secure authentication v5 (g120) | no | no | both roles | no |

¹ dnp3py's outstation serves the `dnp3py` fixture only.

Contract 1.2 adds frozen analog inputs, command events, time and interval
objects, dual end point (the outstation dials) and security statistics.
Contract 1.3, the current one, adds device attributes, several controls in
one request, freeze at time and analog freeze, analog deadbands, file append
and authentication, event buffer overflow, link-layer confirms and
keep-alives, broadcast, management functions and several outstations on one
channel. Which adapter has which is in
[docs/CAPABILITIES.md](docs/CAPABILITIES.md).

The Step Function I/O dnp3 crate is distributed under a non-commercial,
non-production licence that does not permit redistributing it. This
repository holds only the MIT-licensed stepfunc adapter and its Dockerfile;
no stepfunc image is published, and each user builds one locally, getting the
crate from crates.io under that licence. See
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

On hold: the Triangle MicroWorks DNP3
Source Code Library (C), whose licensed source has no agreed way into the
builds yet. See [PLAN.md](PLAN.md).

**Docs:** [docs/CONTAINER_CONTRACT.md](docs/CONTAINER_CONTRACT.md) (commands, JSON documents, exit codes) · [docs/FIXTURES.md](docs/FIXTURES.md) (the simulated outstation and its behaviour) · [docs/CAPABILITIES.md](docs/CAPABILITIES.md) · [docs/COMPATIBILITY.md](docs/COMPATIBILITY.md) (pins, cross-stack matrix, findings) · [COVERAGE.md](COVERAGE.md) · [REQUIREMENTS.md](REQUIREMENTS.md) · [PLAN.md](PLAN.md) · [RELEASE.md](RELEASE.md)

## Table of contents

- [dnp3-interop — DNP3 (IEEE 1815) Interoperability Testing with Docker](#dnp3-interop--dnp3-ieee-1815-interoperability-testing-with-docker)
  - [Table of contents](#table-of-contents)
  - [What this repository is, and is not](#what-this-repository-is-and-is-not)
  - [Quick start](#quick-start)
  - [Building the stepfunc image](#building-the-stepfunc-image)
  - [The dnp3c runner](#the-dnp3c-runner)
  - [Using the images from a library's tests](#using-the-images-from-a-librarys-tests)
  - [Repository layout](#repository-layout)
  - [Development](#development)
  - [Security](#security)
  - [License](#license)

## What this repository is, and is not

It owns the reference implementations, their adapters, the fixtures, the
container contract, the images and the self-tests that show the references
agree with each other.

It does not contain, import, build or know about any library under test.
[otfabric/go-dnp3](https://github.com/otfabric/go-dnp3) is a consumer: it
pulls the images and owns its own scenarios, assertions and compatibility
claims. A passing self-test here says the *references* interoperate; it says
nothing about a consumer.

The fixtures describe what an outstation is; the adapters make each
implementation behave that way; the consumer decides what to assert.

## Quick start

Needs Docker and `jq`.

```sh
make images      # build the adapter images for this machine (stepfunc included)
make smoke       # container contract of each image
make interop     # every master against every outstation
```

A reference outstation, and a reference master in its network namespace
(both images built by `make images`, tagged `dev`; with a release tag
instead of `dev` the opendnp3 and dscsystems images are pulled from GHCR):

```sh
docker run -d --rm --name ref ghcr.io/otfabric/dnp3-interop-opendnp3:dev server
until docker exec ref test -f /run/dnp3-interop/ready; do sleep 0.1; done
docker run --rm --network container:ref \
  ghcr.io/otfabric/dnp3-interop-dscsystems:dev \
  client integrity-poll --host 127.0.0.1 --port 20000 | jq .
docker stop ref
```

```json
{
  "schemaVersion": "1.0",
  "adapter": "dscsystems",
  "operation": "integrity-poll",
  "ok": true,
  "error": null,
  "connected": true,
  "iin": ["NEED_TIME"],
  "objects": [
    { "group": 1, "variation": 2, "index": 0, "value": true, "flags": ["ONLINE"], "unsolicited": false },
    { "group": 30, "variation": 5, "index": 0, "value": 230.5, "flags": ["ONLINE"], "unsolicited": false }
  ]
}
```

(abridged). `make run-stepfunc`, `make run-opendnp3` and
`make run-dscsystems` run an outstation on 127.0.0.1:20000, 20001 and 20002
(baseline fixture, Ctrl-C to stop); `make run-stepfunc` needs the locally
built stepfunc image (below). dnp3py has no such target and serves its
own fixture:

```sh
docker run --rm -p 127.0.0.1:20003:20000 ghcr.io/otfabric/dnp3-interop-dnp3py:dev \
  server --fixture /fixtures/dnp3py/fixture.json
```

Every image has the same commands:

```text
server               an outstation serving a fixture
client <operation>   a master running one of: integrity-poll, event-poll, read,
                     operate, operate-multi, time-sync, restart,
                     enable-unsolicited, disable-unsolicited, assign-class,
                     freeze, monitor, file-get, file-put, file-ls, file-info,
                     file-delete, file-authenticate, attribute-read,
                     attribute-write, deadband-write, manage
                     (see docs/CONTAINER_CONTRACT.md; not every image has all)
print-capabilities   what this adapter supports, as JSON
print-fixture        the fixture baked into the image
version              adapter and upstream versions
```

## Building the stepfunc image

The stepfunc image is not published, at Step Function I/O's request: the
licence of their dnp3 crate does not permit distributing it, and the image
has it compiled in. Interoperability testing with it is allowed, so build it
yourself; the build fetches the crate from crates.io, under its licence, for
you. At the release tag you test against (for example `v0.5.1`):

```sh
git clone --branch v0.5.1 https://github.com/otfabric/dnp3-interop.git
cd dnp3-interop
make image-stepfunc VERSION=v0.5.1     # ghcr.io/otfabric/dnp3-interop-stepfunc:v0.5.1, local only
```

or without make:

```sh
docker build --build-arg ADAPTER_VERSION=v0.5.1 \
  -f adapters/stepfunc/Dockerfile -t ghcr.io/otfabric/dnp3-interop-stepfunc:v0.5.1 .
```

Without `VERSION`, `make image-stepfunc` builds the `dev` tag that
`make smoke`, `make interop` and `make run-stepfunc` use. The tag names the
registry, but the image stays on your machine: nothing in this repository
pushes or pulls it. Do not push it to a registry yourself either; that would
distribute the crate.

## The dnp3c runner

`make image-dnp3c` builds `dnp3-interop-dnp3c`, which runs the DNP Users
Group's DNP3 IED Certification Procedure against an outstation. It uses Step
Function I/O's [dnp3c](https://github.com/stepfunc/dnp3c) 0.2.0 (3.1 rev 1 at
subset levels 1 to 3, and 2.7). The outstation under test implements a small
control protocol for the steps dnp3c cannot do over DNP3, such as restarts,
input changes and checks of outputs; no adapter here implements it. dnp3c and
the runner are GPL-3.0-only. The build fetches dnp3c from Maven Central, and
the image is not published. See [docs/CONFORMANCE.md](docs/CONFORMANCE.md).
The results are not a certification.

## Using the images from a library's tests

A consumer needs Docker, the image references and its own test code. It does
not clone this repository, except to build the stepfunc image
([above](#building-the-stepfunc-image)).

**Library as master, reference as outstation.** Start the server, wait for the
readiness file (never for a delay), connect to link address 1024 as master 1:

```sh
docker run -d --name ref -p 127.0.0.1:20000:20000 ghcr.io/otfabric/dnp3-interop-opendnp3:<tag> server
until docker exec ref test -f /run/dnp3-interop/ready; do sleep 0.1; done
```

The server's stdout is a JSON Lines stream of what it received (`command`,
`time-sync`, `restart`, ...), for asserting what the library actually put on
the wire.

**Library as outstation, reference as master.** Serve the fixture with the
library, run a client operation, assert on the result document and exit code:

```sh
docker run --rm --add-host=host.docker.internal:host-gateway \
  ghcr.io/otfabric/dnp3-interop-opendnp3:<tag> \
  client operate --type g12v1 --index 0 --code LATCH_ON --mode sbo \
  --host host.docker.internal --port <port of the library's outstation>
```

This is for Docker Desktop. On Linux use `--network host` and
`--host 127.0.0.1` instead of `--add-host` and `host.docker.internal`.

**Expected values** come from the image: `print-fixture baseline` prints the
fixture it was built with, so expectations cannot drift from the image.

**What a stack supports** comes from the image too: `print-capabilities`.
Skip, do not fail, a scenario that needs a feature an image declares false.

**Pin by digest** in release qualification
(`ghcr.io/otfabric/dnp3-interop-opendnp3@sha256:...`); the digests of the
published images are in each release's `manifest.json`. The stepfunc and
dnp3c images have no published digest: pin them by building them from the
release tag, whose upstream pins `manifest.json` records under `localOnly`
(dnp3c from v0.5.1).

## Repository layout

```text
dnp3-interop/
├── adapters/
│   ├── opendnp3/        C++ adapter: Dockerfile, CMakeLists.txt, src/
│   ├── stepfunc/        Rust adapter: Dockerfile, Cargo.toml, Cargo.lock, src/ (image built locally, never published)
│   ├── dscsystems/      Go adapter: Dockerfile, go.mod, *.go
│   ├── dnp3py/          Python adapter: Dockerfile, requirements.txt, dnp3_interop.py
│   └── LICENSE          MIT, except adapters/dscsystems/ (GPL-3.0-or-later)
├── conformance/
│   ├── dnp3c/           the dnp3c runner: Dockerfile, pom.xml, src/ (GPL-3.0-only; image built locally, never published)
│   └── LICENSE          why conformance/dnp3c/ is GPL-3.0-only
├── fixtures/
│   ├── schema/          JSON Schema of the fixture format
│   ├── baseline/        the baseline outstation
│   ├── dnp3py/          what dnp3py's outstation can serve
│   └── extended/, files/, attributes/, controls/, events/   fixtures of contract 1.1..1.3 features
├── schemas/             JSON Schemas of the capability and client result documents, interop-result-v1
├── tests/cases.tsv      self-test cases: operation, expected outcome, features needed
├── tests/cases-dnp3py.tsv the cases against the dnp3py fixture
├── tests/known-bugs.tsv known bugs of upstream versions
├── scripts/             validate, smoke, cross-stack self-test
├── docs/                contract, fixtures, capabilities, compatibility
├── licenses/            licence texts the adapters refer to
├── versions.yaml        every upstream pin and base image digest
├── compose.yaml         the reference outstations on loopback
└── Makefile
```

## Development

```sh
make help        # all targets
make ci          # validate, lint, build, smoke, cross-stack and feature self-tests, document schemas
make features    # cross-stack self-test of the contract 1.1..1.3 features (TLS, UDP, serial, files, SAv5, ...)
make validate    # fixtures against the schema and the cross-field rules
make buildx      # check the other architecture locally, under emulation (CI builds both natively)
scripts/latest-upstream.py dnp3py                  # the pin and the latest upstream release
make candidate-dnp3py CANDIDATE_VERSION=0.7.0      # self-test an adapter on another upstream release
make documents report                              # self-tests with every document kept, then the compatibility report
VERSION=v0.5.1 make qualify                        # Trivy scan and SBOMs of a release's published images, into qualify/
```

The **Upstream** workflow resolves the latest release of every stack daily
(the dnp3 crate on crates.io, opendnp3's tags, DSC go-dnp3 on the Go module
proxy, dnp3py on PyPI) and the digest of every pinned base image, builds and
self-tests the adapters whose pin is behind, reports pin versus latest in one
table in its job summary and keeps a tracking issue open while a pin is
behind.

`make documents` also writes `reports/results.json`, the organisation's
[interop-result-v1](schemas/interop-result-v1.schema.json), of `make interop` and
`make features`. A run with no pass, or with a known bug that no longer
reproduces, fails.

`make smoke ADAPTERS=opendnp3` and `VERBOSE=1 make interop` narrow and
expand what is run and shown; `DNP3_INTEROP_DEBUG=1` in a container's
environment logs the frames on stderr. See [CONTRIBUTING.md](CONTRIBUTING.md)
for adding an adapter, a fixture or a client operation.

## Security

The servers are unauthenticated DNP3 outstations that accept controls,
restarts and time writes from anyone who can reach them. They are test
fixtures: `compose.yaml` and the `run-*` targets publish them on loopback
only, and they must never be attached to a plant network. See
[SECURITY.md](SECURITY.md).

## License

The repository's own code and documentation, and the adapter sources, are
MIT-licensed, except `adapters/dscsystems/`, which links a GPL-3.0-or-later
library and is GPL-3.0-or-later. The images contain the upstream libraries
under their own licences: opendnp3 is Apache-2.0, DSC go-dnp3 is
GPL-3.0-or-later and dnp3py is MIT. The Step Function I/O dnp3 crate is under
a non-commercial, non-production licence agreement that does not permit
redistribution: its image is not published, and whoever builds it locally
gets the crate from crates.io under that agreement. See [LICENSE](LICENSE) and
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
