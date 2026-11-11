# dnp3-interop adapter for opendnp3.
#
# Build from the repository root:
#   docker build -f adapters/opendnp3/Dockerfile -t dnp3-interop-opendnp3:dev .
#
# opendnp3 is Apache-2.0; the adapter is MIT. The adapter links opendnp3
# statically; its NOTICE and licence are copied into the image. See
# THIRD_PARTY_NOTICES.md. Upstream pins are mirrored in versions.yaml.

# Where the base images come from. They are pinned by digest, so a mirror of
# Docker Hub serves exactly the same images; CI uses one to stay clear of
# Docker Hub's pull rate limit (see the Makefile).
ARG BASE_REGISTRY=docker.io/library
ARG DEBIAN_DIGEST=sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2
# The snapshot of the Debian archive that debian:trixie-slim at that digest was
# built from (its /etc/apt/sources.list.d/debian.sources names it). Packages
# are installed from that snapshot at pinned versions, so a build gets the
# same packages every time; versions.yaml mirrors them.
ARG DEBIAN_SNAPSHOT=20261104T212339Z

# ---------------------------------------------------------------------------
# Stage 1: opendnp3 from a pinned commit
# ---------------------------------------------------------------------------
FROM ${BASE_REGISTRY}/debian:trixie-slim@${DEBIAN_DIGEST} AS opendnp3-build

ARG DEBIAN_SNAPSHOT
SHELL ["/bin/bash", "-o", "pipefail", "-c"]

ARG OPENDNP3_REPO=https://github.com/dnp3/opendnp3.git
ARG UPSTREAM_VERSION=3.1.2
ARG UPSTREAM_SHA=26b4c01e4839bbbda8866655e086471c4917ee53

RUN sed -i -e "s|^URIs: http://deb.debian.org/debian\$|URIs: http://snapshot.debian.org/archive/debian/${DEBIAN_SNAPSHOT}|" \
              -e "s|^URIs: http://deb.debian.org/debian-security\$|URIs: http://snapshot.debian.org/archive/debian-security/${DEBIAN_SNAPSHOT}|" \
              /etc/apt/sources.list.d/debian.sources \
    && ! grep -q "^URIs: http://deb.debian.org" /etc/apt/sources.list.d/debian.sources \
    && apt-get -o Acquire::Check-Valid-Until=false update && apt-get install -y --no-install-recommends \
      ca-certificates=20250419+deb13u1 \
      cmake=3.31.6-2 \
      g++=4:14.2.0-1 \
      git=1:2.47.3-0+deb13u1 \
      libssl-dev=3.5.7-1~deb13u3 \
      make=4.4.1-2 \
    && rm -rf /var/lib/apt/lists/*

# Fetched by commit, so the pin is the hash and not a tag that could move.
# UPSTREAM_VERSION is the name of that commit, for print-capabilities. "make
# candidate-opendnp3 CANDIDATE_VERSION=<tag> CANDIDATE_SHA=<commit>" overrides
# both to try another commit.
WORKDIR /src/opendnp3
RUN git init -q . \
    && git remote add origin "${OPENDNP3_REPO}" \
    && git fetch -q --depth 1 origin "${UPSTREAM_SHA}" \
    && git checkout -q --detach FETCH_HEAD \
    && test "$(git rev-parse HEAD)" = "${UPSTREAM_SHA}"

# Configuring fetches the dependencies opendnp3 pins itself in deps/*.cmake
# (asio 1.16.0, exe4cpp and ser4cpp at fixed commits). The sources of 2022
# rely on <cstdint> arriving through other headers, which GCC 14 no longer
# does: it is included explicitly. TLS is on (OpenSSL 3 of the base image);
# tests and examples are off.
WORKDIR /src/opendnp3/build
RUN cmake .. \
      -DCMAKE_BUILD_TYPE=Release \
      -DCMAKE_INSTALL_PREFIX=/opt/opendnp3 \
      -DCMAKE_CXX_FLAGS="-include cstdint" \
      -DDNP3_STATIC_LIBS=ON \
      -DDNP3_TLS=ON \
      -DDNP3_TESTS=OFF \
      -DDNP3_EXAMPLES=OFF \
    && make -j"$(nproc)" \
    && make install

# ---------------------------------------------------------------------------
# Stage 2: the adapter
# ---------------------------------------------------------------------------
FROM ${BASE_REGISTRY}/debian:trixie-slim@${DEBIAN_DIGEST} AS adapter-build

ARG DEBIAN_SNAPSHOT
SHELL ["/bin/bash", "-o", "pipefail", "-c"]

ARG UPSTREAM_VERSION=3.1.2
ARG UPSTREAM_SHA=26b4c01e4839bbbda8866655e086471c4917ee53
ARG ADAPTER_VERSION=dev

RUN sed -i -e "s|^URIs: http://deb.debian.org/debian\$|URIs: http://snapshot.debian.org/archive/debian/${DEBIAN_SNAPSHOT}|" \
              -e "s|^URIs: http://deb.debian.org/debian-security\$|URIs: http://snapshot.debian.org/archive/debian-security/${DEBIAN_SNAPSHOT}|" \
              /etc/apt/sources.list.d/debian.sources \
    && ! grep -q "^URIs: http://deb.debian.org" /etc/apt/sources.list.d/debian.sources \
    && apt-get -o Acquire::Check-Valid-Until=false update && apt-get install -y --no-install-recommends \
      cmake=3.31.6-2 \
      g++=4:14.2.0-1 \
      libssl-dev=3.5.7-1~deb13u3 \
      make=4.4.1-2 \
      nlohmann-json3-dev=3.11.3-2.1 \
    && rm -rf /var/lib/apt/lists/*

COPY --from=opendnp3-build /opt/opendnp3 /opt/opendnp3
COPY adapters/opendnp3/CMakeLists.txt /src/adapter/CMakeLists.txt
COPY adapters/opendnp3/src /src/adapter/src

WORKDIR /src/adapter/build
RUN cmake .. \
      -DCMAKE_BUILD_TYPE=Release \
      -DADAPTER_VERSION="${ADAPTER_VERSION}" \
      -DUPSTREAM_VERSION="${UPSTREAM_VERSION}" \
      -DUPSTREAM_REVISION="${UPSTREAM_SHA}" \
    && make -j"$(nproc)"

# ---------------------------------------------------------------------------
# Stage 3: runtime
# ---------------------------------------------------------------------------
FROM ${BASE_REGISTRY}/debian:trixie-slim@${DEBIAN_DIGEST} AS runtime

ARG DEBIAN_SNAPSHOT
SHELL ["/bin/bash", "-o", "pipefail", "-c"]

ARG ADAPTER_VERSION=dev

# OpenSSL for TLS; socat bridges a pseudo-terminal to TCP for DNP3 over
# serial (contract 1.1).
RUN sed -i -e "s|^URIs: http://deb.debian.org/debian\$|URIs: http://snapshot.debian.org/archive/debian/${DEBIAN_SNAPSHOT}|" \
              -e "s|^URIs: http://deb.debian.org/debian-security\$|URIs: http://snapshot.debian.org/archive/debian-security/${DEBIAN_SNAPSHOT}|" \
              /etc/apt/sources.list.d/debian.sources \
    && ! grep -q "^URIs: http://deb.debian.org" /etc/apt/sources.list.d/debian.sources \
    && apt-get -o Acquire::Check-Valid-Until=false update && apt-get install -y --no-install-recommends \
      libssl3t64=3.5.7-1~deb13u3 \
      socat=1.8.0.3-1+deb13u1 \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --uid 10001 --no-create-home --shell /usr/sbin/nologin interop \
    && mkdir -p /run/dnp3-interop /licenses/opendnp3 \
    && chown interop /run/dnp3-interop

COPY --from=adapter-build /src/adapter/build/dnp3-interop /usr/local/bin/dnp3-interop
COPY --from=opendnp3-build /src/opendnp3/LICENSE /src/opendnp3/NOTICE /licenses/opendnp3/
COPY THIRD_PARTY_NOTICES.md /licenses/THIRD_PARTY_NOTICES.md
COPY fixtures /fixtures

LABEL org.opencontainers.image.title="dnp3-interop-opendnp3" \
      org.opencontainers.image.description="DNP3 reference master and outstation built on opendnp3" \
      org.opencontainers.image.source="https://github.com/otfabric/dnp3-interop" \
      org.opencontainers.image.licenses="MIT AND Apache-2.0" \
      org.opencontainers.image.version="${ADAPTER_VERSION}"

USER 10001
EXPOSE 20000

HEALTHCHECK --interval=2s --timeout=2s --start-period=10s --retries=15 \
  CMD ["test", "-f", "/run/dnp3-interop/ready"]

ENTRYPOINT ["/usr/local/bin/dnp3-interop"]
CMD ["server"]
