# Third-party notices

The images published from this repository contain third-party software.
This file is copied into every image as `/licenses/THIRD_PARTY_NOTICES.md`,
next to the licence texts.

## What is licensed how

| Part | Licence |
|------|---------|
| Repository tooling, fixtures, schemas, scripts, documentation | MIT ([LICENSE](LICENSE)) |
| `adapters/opendnp3/` (links opendnp3) | MIT |
| `adapters/stepfunc/` (links the Step Function I/O dnp3 crate) | MIT |
| `adapters/dscsystems/` (links DSC Systems go-dnp3) | GPL-3.0-or-later |
| `adapters/dnp3py/` (imports dnp3py) | MIT |
| Image `dnp3-interop-opendnp3` | MIT and Apache-2.0 (and the licences of the components below) |
| Image `dnp3-interop-stepfunc` | MIT and the Step Function I/O licence agreement (non-commercial, non-production) |
| Image `dnp3-interop-dscsystems` | GPL-3.0 (it contains go-dnp3) |
| Image `dnp3-interop-dnp3py` | MIT (and the Python licence) |

Each adapter is a separate program in a separate image; no image contains two
implementations. Nothing in this repository is linked into a consumer: a
library that tests against the images talks to them over TCP and reads their
output, and is not affected by their licences.

The dscsystems adapter is a derivative work of the GPL-3.0 go-dnp3 library
and carries `SPDX-License-Identifier: GPL-3.0-or-later`; the others are MIT.

## Image `dnp3-interop-opendnp3`

| Component | Version | Licence | Source |
|-----------|---------|---------|--------|
| opendnp3 (Green Energy Corp, Automatak LLC) | 3.1.2, commit `26b4c01e4839bbbda8866655e086471c4917ee53` | Apache-2.0 | https://github.com/dnp3/opendnp3 |
| asio (Christopher M. Kohlhoff), header-only, compiled into opendnp3 | 1.16.0 | BSL-1.0 | https://github.com/chriskohlhoff/asio |
| exe4cpp (Automatak), compiled into opendnp3 | commit `fb878a4de598ba9d6e4338afebf83f96e03af1b8` | Apache-2.0 | https://github.com/automatak/exe4cpp |
| ser4cpp (Automatak), compiled into opendnp3 | commit `3c449734dc530a8f465eb0982de29165cc4e23d5` | Apache-2.0 | https://github.com/automatak/ser4cpp |
| JSON for Modern C++ (Niels Lohmann), header-only (Debian `nlohmann-json3-dev`) | Debian trixie | MIT | https://github.com/nlohmann/json |
| GNU C++ library, linked statically (Debian `libstdc++`) | Debian trixie | GPL-3.0 with the GCC Runtime Library Exception | https://gcc.gnu.org |
| OpenSSL, linked dynamically for TLS (Debian `libssl3t64`) | Debian trixie | Apache-2.0 | https://www.openssl.org |
| socat (Debian `socat`), for bridging a serial line in tests | Debian trixie | GPL-2.0 | http://www.dest-unreach.org/socat/ |
| Adapter | this repository, `adapters/opendnp3/` | MIT | https://github.com/otfabric/dnp3-interop |
| Base system | Debian trixie-slim | various; see `/usr/share/doc/*/copyright` in the image | https://www.debian.org |

Licence and notice in the image: `/licenses/opendnp3/LICENSE` and
`/licenses/opendnp3/NOTICE`. The opendnp3 repository is archived; 3.1.2 is its
last release.

## Image `dnp3-interop-stepfunc`

| Component | Version | Licence | Source |
|-----------|---------|---------|--------|
| dnp3 (Step Function I/O LLC) | 1.7.0 (crates.io), tag commit `d7ccb9f6c0a8dc2900e90ca8d0a9a9d5336bb8ca` | Step Function I/O licence agreement: non-commercial, non-production use | https://github.com/stepfunc/dnp3 |
| Rust crates the adapter and dnp3 depend on (tokio, serde_json, tracing, chrono, rustls, tokio-serial, ...) | the versions in `adapters/stepfunc/Cargo.lock` | MIT and/or Apache-2.0 | https://crates.io |
| ring (cryptography of rustls) | the version in `adapters/stepfunc/Cargo.lock` | Apache-2.0 AND ISC | https://github.com/briansmith/ring |
| socat (Debian `socat`), for bridging a serial line in tests | Debian trixie | GPL-2.0 | http://www.dest-unreach.org/socat/ |
| Rust standard library | 1.99.1 | MIT or Apache-2.0 | https://www.rust-lang.org |
| Adapter | this repository, `adapters/stepfunc/` | MIT | https://github.com/otfabric/dnp3-interop |
| Base system | Debian trixie-slim | various; see `/usr/share/doc/*/copyright` in the image | https://www.debian.org |

Licence in the image: `/licenses/dnp3/LICENSE.txt`, the licence agreement the
crate is distributed with.

**The dnp3 crate is not open source.** Its licence agreement permits
non-commercial, non-production use only. This image exists for
interoperability testing of other DNP3 implementations and is published for
that purpose only; it is not a product and must not be used in production.
For any other use, obtain a commercial licence from Step Function I/O
(https://stepfunc.io).

## Image `dnp3-interop-dscsystems`

| Component | Version | Licence | Source |
|-----------|---------|---------|--------|
| go-dnp3 (DSC Systems) | v0.8.1, commit `70ff72bb09755db183d1bd69859adc72cd663aec` | GPL-3.0 | https://github.com/dscsystems/go-dnp3 |
| go.bug.st/serial, golang.org/x/sys (linked by go-dnp3's channel package) | the versions in `adapters/dscsystems/go.sum` | BSD-3-Clause | https://github.com/bugst/go-serial, https://go.googlesource.com/sys |
| Go standard library and runtime | 1.27.2 | BSD-3-Clause | https://go.dev |
| socat (Debian `socat`), for bridging a serial line in tests | Debian trixie | GPL-2.0 | http://www.dest-unreach.org/socat/ |
| Adapter | this repository, `adapters/dscsystems/` | GPL-3.0-or-later | https://github.com/otfabric/dnp3-interop |
| Base system | Debian trixie-slim | various; see `/usr/share/doc/*/copyright` in the image | https://www.debian.org |

Licence texts in the image: `/licenses/go-dnp3/LICENSE`,
`/licenses/go-dnp3/NOTICE` and `/licenses/GPL-3.0.txt`.

## Image `dnp3-interop-dnp3py`

| Component | Version | Licence | Source |
|-----------|---------|---------|--------|
| dnp3py | 0.6.0 (PyPI wheel, hash in `adapters/dnp3py/requirements.txt`), tag commit `c00d5df22e779df72f7caa4402c3f34957eb276d` | MIT | https://github.com/craigpnnl/dnp3py |
| CPython and its standard library | 3.14.8 | PSF-2.0 | https://www.python.org |
| Adapter | this repository, `adapters/dnp3py/` | MIT | https://github.com/otfabric/dnp3-interop |
| Base system | Debian trixie-slim (python:3.14.8-slim-trixie) | various; see `/usr/share/doc/*/copyright` in the image | https://www.debian.org |

Licence in the image: `/licenses/dnp3py/LICENSE`.

## Corresponding source

For every published image the complete corresponding source is:

1. this repository at the release tag named by the image's
   `org.opencontainers.image.version` label and by `print-capabilities`
   (`adapterVersion`), which contains the adapter source and the build
   instructions (the Dockerfile); and
2. the upstream sources at exactly the versions pinned by that Dockerfile,
   `adapters/stepfunc/Cargo.lock`, `adapters/dscsystems/go.mod` and
   `adapters/dnp3py/requirements.txt`, recorded in [versions.yaml](versions.yaml)
   and in the `manifest.json` of the release.

`docker build -f adapters/<adapter>/Dockerfile .` at the release tag
reproduces the image from those sources.

Each GitHub release of this repository records the upstream revisions and
image digests it was built from.
