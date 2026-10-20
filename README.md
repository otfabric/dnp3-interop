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
| `stepfunc` | [Step Function I/O dnp3](https://github.com/stepfunc/dnp3) 1.7.0 | Rust | yes | yes | `ghcr.io/otfabric/dnp3-interop-stepfunc` |
| `opendnp3` | [opendnp3](https://github.com/dnp3/opendnp3) 3.1.2 | C++ | yes | yes | `ghcr.io/otfabric/dnp3-interop-opendnp3` |
| `dscsystems` | [DSC Systems go-dnp3](https://github.com/dscsystems/go-dnp3) v0.8.1 (experimental) | Go | yes | yes | `ghcr.io/otfabric/dnp3-interop-dscsystems` |
| `dnp3py` | [dnp3py](https://github.com/craigpnnl/dnp3py) 0.6.0 | Python | no | yes | `ghcr.io/otfabric/dnp3-interop-dnp3py` |

The stacks do not all do the same: the stepfunc outstation refuses ASSIGN
CLASS, opendnp3's master synchronizes time only when the outstation asks for
it, go-dnp3's master reads only ranges and classes, and dnp3py serves as a
master only. Each image says what it supports (`print-capabilities`); see
[docs/CAPABILITIES.md](docs/CAPABILITIES.md).

The Step Function I/O dnp3 crate is distributed under a non-commercial,
non-production licence. Its image exists for interoperability testing only;
see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

Planned: dnp3py's outstation role. On hold: the Triangle MicroWorks DNP3
Source Code Library (C), whose licensed source has no agreed way into the
builds yet. See [PLAN.md](PLAN.md).

**Docs:** [docs/CONTAINER_CONTRACT.md](docs/CONTAINER_CONTRACT.md) (commands, JSON documents, exit codes) · [docs/FIXTURES.md](docs/FIXTURES.md) (the simulated outstation and its behaviour) · [docs/CAPABILITIES.md](docs/CAPABILITIES.md) · [docs/COMPATIBILITY.md](docs/COMPATIBILITY.md) (pins, cross-stack matrix, findings) · [COVERAGE.md](COVERAGE.md) · [REQUIREMENTS.md](REQUIREMENTS.md) · [PLAN.md](PLAN.md) · [RELEASE.md](RELEASE.md)

## Table of contents

- [dnp3-interop — DNP3 (IEEE 1815) Interoperability Testing with Docker](#dnp3-interop--dnp3-ieee-1815-interoperability-testing-with-docker)
  - [Table of contents](#table-of-contents)
  - [What this repository is, and is not](#what-this-repository-is-and-is-not)
  - [Quick start](#quick-start)
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
make images      # build the adapter images for this machine
make smoke       # container contract of each image
make interop     # every master against every outstation
```

A reference outstation on loopback, and a reference master against it:

```sh
make run-opendnp3        # 127.0.0.1:20001, baseline fixture, Ctrl-C to stop
```

```sh
docker run --rm --add-host=host.docker.internal:host-gateway \
  ghcr.io/otfabric/dnp3-interop-stepfunc:dev \
  client integrity-poll --host host.docker.internal --port 20001 | jq .
```

```json
{
  "schemaVersion": "1.0",
  "adapter": "stepfunc",
  "operation": "integrity-poll",
  "ok": true,
  "error": null,
  "connected": true,
  "iin": ["NEED_TIME", "DEVICE_RESTART"],
  "objects": [
    { "group": 1, "variation": 2, "index": 0, "value": true, "flags": ["ONLINE"], "unsolicited": false },
    { "group": 30, "variation": 5, "index": 0, "value": 230.5, "flags": ["ONLINE"], "unsolicited": false }
  ]
}
```

(abridged). Every image has the same commands (dnp3py, a master only, refuses `server`):

```text
server               an outstation serving a fixture
client <operation>   a master running one of: integrity-poll, event-poll, read,
                     operate, time-sync, restart, enable-unsolicited,
                     disable-unsolicited, assign-class, freeze, monitor
print-capabilities   what this adapter supports, as JSON
print-fixture        the fixture baked into the image
version              adapter and upstream versions
```

## Using the images from a library's tests

A consumer needs Docker, the image references and its own test code. It does
not clone this repository.

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
  ghcr.io/otfabric/dnp3-interop-stepfunc:<tag> \
  client operate --type g12v1 --index 0 --code LATCH_ON --mode sbo \
  --host host.docker.internal --port <port of the library's outstation>
```

**Expected values** come from the image: `print-fixture baseline` prints the
fixture it was built with, so expectations cannot drift from the image.

**What a stack supports** comes from the image too: `print-capabilities`.
Skip, do not fail, a scenario that needs a feature an image declares false.

**Pin by digest** in release qualification
(`ghcr.io/otfabric/dnp3-interop-opendnp3@sha256:...`); the digests are in
each release's `manifest.json`.

## Repository layout

```text
dnp3-interop/
├── adapters/
│   ├── opendnp3/        C++ adapter: Dockerfile, CMakeLists.txt, src/
│   ├── stepfunc/        Rust adapter: Dockerfile, Cargo.toml, Cargo.lock, src/
│   ├── dscsystems/      Go adapter: Dockerfile, go.mod, *.go
│   └── dnp3py/          Python adapter (master only): Dockerfile, requirements.txt, dnp3_interop.py
├── fixtures/
│   ├── schema/          JSON Schema of the fixture format
│   └── baseline/        the baseline outstation
├── schemas/             JSON Schemas of the capability and client result documents
├── tests/cases.tsv      self-test cases: operation, expected outcome, features needed
├── tests/known-bugs.tsv bugs of upstream versions that are built as candidates
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
make ci          # validate, lint, build, smoke, cross-stack self-test, document schemas
make validate    # fixtures against the schema and the cross-field rules
make buildx      # check the other architecture locally, under emulation (CI builds both natively)
scripts/latest-upstream.py dnp3py                  # the pin and the latest upstream release
make candidate-dnp3py CANDIDATE_VERSION=0.7.0      # self-test an adapter on another upstream release
make documents report                              # self-tests with every document kept, then the compatibility report
VERSION=v0.1.0 make qualify                        # Trivy scan and SBOMs of a published release, into qualify/
```

The **Upstream candidates** workflow resolves the latest release of every
stack weekly (the dnp3 crate on crates.io, opendnp3's tags, go-dnp3 on the
Go module proxy, dnp3py on PyPI), builds and self-tests the adapters whose
pin is behind, reports pin versus latest in one table in its job summary and
keeps a tracking issue open while a pin is behind.

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
MIT-licensed, except `adapters/dscsystems/`, which links a GPL-3.0 library and
is GPL-3.0-or-later. The images contain the upstream libraries under their
own licences: opendnp3 is Apache-2.0, go-dnp3 is GPL-3.0, dnp3py is MIT, and the Step
Function I/O dnp3 crate is under a non-commercial, non-production licence
agreement. See [LICENSE](LICENSE) and
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
