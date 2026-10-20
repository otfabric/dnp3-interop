# Requirements

What this repository must do and must not do. [PLAN.md](PLAN.md) says in
which order; [docs/CONTAINER_CONTRACT.md](docs/CONTAINER_CONTRACT.md) and
[docs/FIXTURES.md](docs/FIXTURES.md) are the specifications that follow from
it.

## Purpose

Independent, reproducible interoperability validation for DNP3 (IEEE 1815)
libraries: established third-party implementations, packaged as containers
that all behave the same way, so that a library can be tested against real
implementation diversity without owning any of it.

It is not another DNP3 implementation and not a general protocol-testing
framework.

## R1 - Repository isolation

- R1.1 This repository does not import, build, link against or depend on any
  library under test, and contains no test logic or expectation specific to
  one. In particular it knows nothing about `otfabric/go-dnp3`.
- R1.2 A consumer depends on published images and the documented contract
  only. It does not need a checkout of this repository to run its tests.
- R1.3 No third-party code from here is copied into a consumer, and the
  contract does not shape a consumer's public API.

| Responsibility | Here | Consumer |
|----------------|:----:|:--------:|
| Third-party implementations, adapters, Dockerfiles | yes | no |
| Fixtures, fixture schema and validation | yes | no |
| Container contract, capability declarations | yes | no |
| Image builds and publishing, licence compliance | yes | no |
| Self-tests of the reference stacks | yes | no |
| Scenarios and assertions about a library | no | yes |
| Compatibility claims and release qualification of a library | no | yes |

## R2 - Reference implementations

- R2.1 Exactly these five are in scope, in this order of priority:

  | Priority | Implementation | Language | Licence | Independent engine |
  |----------|----------------|----------|---------|:------------------:|
  | P0 | Step Function I/O dnp3 (`stepfunc/dnp3`) | Rust | non-commercial licence | yes |
  | P0 | opendnp3 (`dnp3/opendnp3`, archived) | C++ | Apache-2.0 | yes |
  | P1 | dnp3py (`craigpnnl/dnp3py`) | Python | MIT | yes |
  | P1 | DSC Systems go-dnp3 (`dscsystems/go-dnp3`), experimental | Go | GPL-3.0 | yes |
  | P2 | Triangle MicroWorks DNP3 Source Code Library | C | commercial | yes |

  P0 adapters are mandatory, P1 strongly recommended (dnp3py) or recommended
  (DSC go-dnp3), P2 is industry validation. The P2 adapter is on hold (see
  [PLAN.md](PLAN.md)).

- R2.2 Each is its own image. No image contains two implementations.
- R2.3 An adapter that wraps another adapter's protocol engine says so
  (`upstream.independentEngine: false`) and does not count as an additional
  independent implementation.
- R2.4 An adapter is published only when it builds from pinned sources and
  passes the smoke test and the cross-stack self-test for the roles it
  declares. An upstream that cannot be made to do so is documented and left
  out; it does not block the others.
- R2.5 Source that is not public (the Triangle MicroWorks library, on hold)
  is supplied at build time and never committed to this repository; the
  repository holds the adapter, the build instructions and the checksum of
  the source it was verified with.

## R3 - Container contract

- R3.1 Every image has the same entry points: `server` (an outstation),
  `client` (a master running one operation), `print-capabilities`,
  `print-fixture`, `version`, with the same flags and semantics.
- R3.2 Servers signal readiness when they actually accept DNP3 connections,
  by a file and an event. Nothing relies on delays.
- R3.3 Machine-readable output is JSON on stdout with stable field names;
  diagnostics go to stderr; exit codes have fixed meanings.
- R3.4 Every operation is bounded by a configurable timeout.
- R3.5 Servers stop cleanly on SIGTERM and exit non-zero when they cannot
  start.
- R3.6 Capabilities are machine-readable and declared only for behaviour the
  self-tests exercise.

## R4 - Fixtures

- R4.1 Fixtures describe the simulated outstation and its observable
  behaviour, independent of any implementation: link addresses, the point
  database (types, indexes, values, flags, classes, variations), what
  controls do to it and which events they cause.
- R4.2 Fixtures contain no assertions, no vendor callback names and no names
  from any library under test.
- R4.3 Dynamic behaviour is deterministic and triggered by requests, not by
  timers: an event exists because a request caused it.
- R4.4 Fixtures are validated against a schema, in CI and by each adapter at
  start.
- R4.5 A type or variation an adapter cannot serve is not silently replaced
  by another. Every adapter declares the point types and variations it serves
  (`print-capabilities`) and refuses, before it listens, a fixture that needs
  more. The baseline fixture uses only what both P0 adapters serve.

## R5 - Reproducibility

- R5.1 Every upstream is pinned to a commit or exact version, every base
  image to a digest. `versions.yaml` records all of them and is checked
  against the build files.
- R5.2 Releases are immutable tags; images are published with their digests.
  Nothing a release depends on floats.
- R5.3 Images are published for linux/amd64 and linux/arm64. An adapter that
  cannot support one documents it.

## R6 - Self-tests

- R6.1 The reference stacks are tested against each other in every supported
  direction; the results are identical documents, not just successes.
- R6.2 These results are about the references and are never presented as
  results for a consumer.
- R6.3 Malformed frames, wrong CRCs, out-of-sequence fragments and
  timer-boundary cases are out of scope: reference implementations are not
  misused as fault injectors. That testing belongs in a library's own unit,
  integration and fuzz tests.

## R7 - Licensing and safety

- R7.1 Licence notices of all bundled software are preserved in the images;
  source obligations (GPL, Apache-2.0 notices) are met by pinned, recorded
  sources.
- R7.2 Third-party code stays in the adapter images and never reaches a
  consumer's code: a consumer talks to an image over the network.
- R7.3 Test services are exposed on loopback or private networks by default.
- R7.4 The images exist for interoperability testing only. Licences that
  restrict use (non-commercial, commercial) are recorded with the image and
  in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

## R8 - Scope discipline

- R8.1 Adapters are small and explicit. No generic interoperability
  framework, no protocol-independent abstraction without a concrete need.
- R8.2 Structure is added when an implementation requires it, not before.
- R8.3 The full DNP3 scope (TCP, UDP, TLS, serial; file transfer; secure
  authentication v5) is the target. A part enters the contract when at least
  two adapters can exercise it against each other, or one adapter in both
  roles; until then it is listed as open in [PLAN.md](PLAN.md).
