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
ARG DEBIAN_DIGEST=sha256:a29215f6a35e51e22adffa17f89e9d2ef06214e64a2bad10d765c46aea49f11f

# ---------------------------------------------------------------------------
# Stage 1: opendnp3 from a pinned commit
# ---------------------------------------------------------------------------
FROM ${BASE_REGISTRY}/debian:trixie-slim@${DEBIAN_DIGEST} AS opendnp3-build

ARG OPENDNP3_REPO=https://github.com/dnp3/opendnp3.git
ARG OPENDNP3_VERSION=3.1.2
ARG OPENDNP3_SHA=26b4c01e4839bbbda8866655e086471c4917ee53

RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates \
      cmake \
      g++ \
      git \
      make \
    && rm -rf /var/lib/apt/lists/*

# Fetched by commit, so the pin is the hash and not a tag that could move.
# OPENDNP3_VERSION is the name of that commit, for print-capabilities.
WORKDIR /src/opendnp3
RUN git init -q . \
    && git remote add origin "${OPENDNP3_REPO}" \
    && git fetch -q --depth 1 origin "${OPENDNP3_SHA}" \
    && git checkout -q --detach FETCH_HEAD \
    && test "$(git rev-parse HEAD)" = "${OPENDNP3_SHA}"

# Configuring fetches the dependencies opendnp3 pins itself in deps/*.cmake
# (asio 1.16.0, exe4cpp and ser4cpp at fixed commits). The sources of 2022
# rely on <cstdint> arriving through other headers, which GCC 14 no longer
# does: it is included explicitly. TLS, tests and examples are off.
WORKDIR /src/opendnp3/build
RUN cmake .. \
      -DCMAKE_BUILD_TYPE=Release \
      -DCMAKE_INSTALL_PREFIX=/opt/opendnp3 \
      -DCMAKE_CXX_FLAGS="-include cstdint" \
      -DDNP3_STATIC_LIBS=ON \
      -DDNP3_TLS=OFF \
      -DDNP3_TESTS=OFF \
      -DDNP3_EXAMPLES=OFF \
    && make -j"$(nproc)" \
    && make install

# ---------------------------------------------------------------------------
# Stage 2: the adapter
# ---------------------------------------------------------------------------
FROM ${BASE_REGISTRY}/debian:trixie-slim@${DEBIAN_DIGEST} AS adapter-build

ARG OPENDNP3_VERSION=3.1.2
ARG OPENDNP3_SHA=26b4c01e4839bbbda8866655e086471c4917ee53
ARG ADAPTER_VERSION=dev

RUN apt-get update && apt-get install -y --no-install-recommends \
      cmake \
      g++ \
      make \
      nlohmann-json3-dev \
    && rm -rf /var/lib/apt/lists/*

COPY --from=opendnp3-build /opt/opendnp3 /opt/opendnp3
COPY adapters/opendnp3/CMakeLists.txt /src/adapter/CMakeLists.txt
COPY adapters/opendnp3/src /src/adapter/src

WORKDIR /src/adapter/build
RUN cmake .. \
      -DCMAKE_BUILD_TYPE=Release \
      -DADAPTER_VERSION="${ADAPTER_VERSION}" \
      -DUPSTREAM_VERSION="${OPENDNP3_VERSION}" \
      -DUPSTREAM_REVISION="${OPENDNP3_SHA}" \
    && make -j"$(nproc)"

# ---------------------------------------------------------------------------
# Stage 3: runtime
# ---------------------------------------------------------------------------
FROM ${BASE_REGISTRY}/debian:trixie-slim@${DEBIAN_DIGEST} AS runtime

ARG ADAPTER_VERSION=dev

RUN useradd --uid 10001 --no-create-home --shell /usr/sbin/nologin interop \
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

USER interop
EXPOSE 20000

HEALTHCHECK --interval=2s --timeout=2s --start-period=10s --retries=15 \
  CMD test -f /run/dnp3-interop/ready

ENTRYPOINT ["/usr/local/bin/dnp3-interop"]
CMD ["server"]
