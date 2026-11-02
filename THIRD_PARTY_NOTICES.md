# Third-party notices

The images built from this repository contain third-party software.
This file is copied into every image as `/licenses/THIRD_PARTY_NOTICES.md`,
next to the licence texts. The opendnp3, dscsystems and dnp3py images are
published; the dnp3c runner image is not (see
[Image `dnp3-interop-dnp3c`](#image-dnp3-interop-dnp3c)).

## What is licensed how

| Part | Licence |
|------|---------|
| Repository tooling, fixtures, schemas, scripts, documentation | MIT ([LICENSE](LICENSE)) |
| `adapters/opendnp3/` (links opendnp3) | MIT |
| `adapters/dscsystems/` (links DSC Systems go-dnp3) | GPL-3.0-or-later |
| `adapters/dnp3py/` (imports dnp3py) | MIT |
| `conformance/dnp3c/` (links Step Function I/O dnp3c) | GPL-3.0-only |
| Image `dnp3-interop-opendnp3` | MIT and Apache-2.0 (and the licences of the components below) |
| Image `dnp3-interop-dscsystems` | GPL-3.0-or-later (it contains DSC go-dnp3) |
| Image `dnp3-interop-dnp3py` | MIT (and the Python licence) |
| Image `dnp3-interop-dnp3c` (not published; built locally by each user) | GPL-3.0-only (it contains dnp3c), and the licences of the components below |

Each adapter is a separate program in a separate image; no image contains two
implementations. Nothing in this repository is linked into a consumer: a
library that tests against the images talks to them over TCP and reads their
output, and is not affected by their licences.

The dscsystems adapter is a derivative work of the GPL-3.0-or-later DSC go-dnp3 library
and carries `SPDX-License-Identifier: GPL-3.0-or-later`; the others are MIT.
The dnp3c runner (`conformance/dnp3c/`) links the GPL-3.0-only dnp3c and
carries `SPDX-License-Identifier: GPL-3.0-only`
([conformance/LICENSE](conformance/LICENSE)). A device under test talks to it
over TCP and is not affected by its licence.

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

## Image `dnp3-interop-dscsystems`

| Component | Version | Licence | Source |
|-----------|---------|---------|--------|
| DSC go-dnp3 (DSC Systems) | v0.8.2, commit `1fcec1d7881541d794fe257d2ede3ba46587b500` | GPL-3.0-or-later | https://github.com/dscsystems/go-dnp3 |
| go.bug.st/serial, golang.org/x/sys (linked by DSC go-dnp3's channel package) | the versions in `adapters/dscsystems/go.sum` | BSD-3-Clause | https://github.com/bugst/go-serial, https://go.googlesource.com/sys |
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

## Image `dnp3-interop-dnp3c`

**Not published.** This repository distributes the runner's source in
`conformance/dnp3c/` and its Dockerfile, not dnp3c and not an image
containing it. Whoever builds the image (`make image-dnp3c`) gets dnp3c and
its dependencies from Maven Central during the build. dnp3c's licence,
GPL-3.0-only, would allow publishing the image with its corresponding
source; it is kept local until that is decided.

| Component | Version | Licence | Source |
|-----------|---------|---------|--------|
| dnp3c (Step Function I/O) | 0.2.0 (Maven Central `io.stepfunc:dnp3c`), tag commit `881209943b670cc59b16301346dffa7d5523ab72` | GPL-3.0-only | https://github.com/stepfunc/dnp3c |
| Scala 3 library, Scala library | 3.9.0 | Apache-2.0 | https://www.scala-lang.org |
| Apache Commons CLI | 1.11.0 | Apache-2.0 | https://commons.apache.org/proper/commons-cli/ |
| Jansi | 2.4.3 | Apache-2.0 | https://github.com/fusesource/jansi |
| JNA | 5.3.1 | Apache-2.0 or LGPL-2.1 | https://github.com/java-native-access/jna |
| jSerialComm | 2.11.4 | Apache-2.0 or LGPL-3.0 | https://github.com/Fazecast/jSerialComm |
| Pcap4J (core, static packet factory) | 1.8.2 | MIT | https://github.com/kaitoy/pcap4j |
| SLF4J API, SLF4J NOP | 1.7.26, 2.0.20 | MIT | https://www.slf4j.org |
| Runner | this repository, `conformance/dnp3c/` | GPL-3.0-only | https://github.com/otfabric/dnp3-interop |
| Eclipse Temurin JRE | 17.0.20+8 | GPL-2.0 with the Classpath Exception | https://adoptium.net |
| Base system | Ubuntu noble (eclipse-temurin:17.0.20_8-jre-noble) | various; see `/usr/share/doc/*/copyright` in the image | https://ubuntu.com |

Licence texts in the image: `/licenses/GPL-3.0.txt`,
`/licenses/conformance/LICENSE`; the dependencies' own licences are in their
jars under `/opt/dnp3c/lib/`.

## Corresponding source

For every published image (opendnp3, dscsystems, dnp3py) the complete
corresponding source is:

1. this repository at the release tag named by the image's
   `org.opencontainers.image.version` label and by `print-capabilities`
   (`adapterVersion`), which contains the adapter source and the build
   instructions (the Dockerfile); and
2. the upstream sources at exactly the versions pinned by that Dockerfile,
   `adapters/dscsystems/go.mod` and
   `adapters/dnp3py/requirements.txt`, recorded in [versions.yaml](versions.yaml)
   and in the `manifest.json` of the release.

`docker build -f adapters/<adapter>/Dockerfile .` at the release tag
reproduces the image from those sources. The same command with
`conformance/dnp3c/Dockerfile` builds the unpublished dnp3c runner from the
pins in `conformance/dnp3c/pom.xml`.

Each GitHub release of this repository records the upstream revisions and
image digests it was built from; from v0.5.1 the dnp3c pin is under
`localOnly` in its `manifest.json`, with no image.
