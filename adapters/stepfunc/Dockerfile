# dnp3-interop adapter for the Step Function I/O dnp3 crate.
#
# Build from the repository root:
#   docker build -f adapters/stepfunc/Dockerfile -t dnp3-interop-stepfunc:dev .
#
# The dnp3 crate is distributed by Step Function I/O under a non-commercial,
# non-production licence that forbids distributing it: this image is built
# locally by each user, for interoperability testing only, and is never
# published or pushed. Its licence is copied into the image. The adapter itself is MIT. See
# THIRD_PARTY_NOTICES.md. The pin is in Cargo.toml and Cargo.lock (with the
# crate checksums) and mirrored in versions.yaml.

# Where the base images come from. They are pinned by digest, so a mirror of
# Docker Hub serves exactly the same images; CI uses one to stay clear of
# Docker Hub's pull rate limit (see the Makefile).
ARG BASE_REGISTRY=docker.io/library
ARG RUST_DIGEST=sha256:4df2ae8cfdfebdfc13a65da7f253b73d68983dc0339f2719bc17a279c913b13d
ARG DEBIAN_DIGEST=sha256:a29215f6a35e51e22adffa17f89e9d2ef06214e64a2bad10d765c46aea49f11f

# ---------------------------------------------------------------------------
# Stage 1: build the adapter (the dnp3 crate linked in)
# ---------------------------------------------------------------------------
FROM ${BASE_REGISTRY}/rust:1.99.1-slim-trixie@${RUST_DIGEST} AS build

# The release pin. "make candidate-stepfunc CANDIDATE_VERSION=<version>" overrides it to try another
# crate version without touching the pin.
ARG UPSTREAM_VERSION=1.7.0
# The commit of the release tag in https://github.com/stepfunc/dnp3, for
# print-capabilities (crates.io has no commit; Cargo.lock has the checksum).
ARG UPSTREAM_SHA=d7ccb9f6c0a8dc2900e90ca8d0a9a9d5336bb8ca
ARG ADAPTER_VERSION=dev

RUN rustup component add rustfmt clippy

WORKDIR /src/adapters/stepfunc
COPY adapters/stepfunc/Cargo.toml adapters/stepfunc/Cargo.lock ./
COPY adapters/stepfunc/src ./src
COPY fixtures /src/fixtures

# At the pin the dependencies must match Cargo.lock; anything else is a
# candidate.
RUN if grep -q "^dnp3 = { version = \"=${UPSTREAM_VERSION}\"" Cargo.toml; then \
         cargo fetch --locked; \
       else \
         sed -i "s/^dnp3 = { version = \"=[^\"]*\"/dnp3 = { version = \"=${UPSTREAM_VERSION}\"/" Cargo.toml \
         && cargo update -p dnp3; \
       fi

RUN cargo fmt --check \
    && ADAPTER_VERSION="${ADAPTER_VERSION}" UPSTREAM_VERSION="${UPSTREAM_VERSION}" UPSTREAM_REVISION="${UPSTREAM_SHA}" \
       cargo clippy --release --offline -- -D warnings \
    && ADAPTER_VERSION="${ADAPTER_VERSION}" UPSTREAM_VERSION="${UPSTREAM_VERSION}" UPSTREAM_REVISION="${UPSTREAM_SHA}" \
       cargo test --release --offline \
    && ADAPTER_VERSION="${ADAPTER_VERSION}" UPSTREAM_VERSION="${UPSTREAM_VERSION}" UPSTREAM_REVISION="${UPSTREAM_SHA}" \
       cargo build --release --offline \
    && mkdir -p /out/licenses/dnp3 \
    && cp target/release/dnp3-interop /out/dnp3-interop \
    && cp "$(find "${CARGO_HOME:-/usr/local/cargo}/registry/src" -maxdepth 2 -type d -name "dnp3-${UPSTREAM_VERSION}")/LICENSE.txt" \
          /out/licenses/dnp3/LICENSE.txt

# ---------------------------------------------------------------------------
# Stage 2: runtime
# ---------------------------------------------------------------------------
FROM ${BASE_REGISTRY}/debian:trixie-slim@${DEBIAN_DIGEST} AS runtime

ARG ADAPTER_VERSION=dev

# socat bridges a pseudo-terminal to TCP for DNP3 over serial (contract 1.1).
RUN apt-get update && apt-get install -y --no-install-recommends socat \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --uid 10001 --no-create-home --shell /usr/sbin/nologin interop \
    && mkdir -p /run/dnp3-interop \
    && chown interop /run/dnp3-interop

COPY --from=build /out/dnp3-interop /usr/local/bin/dnp3-interop
COPY --from=build /out/licenses /licenses
COPY THIRD_PARTY_NOTICES.md /licenses/THIRD_PARTY_NOTICES.md
COPY fixtures /fixtures

LABEL org.opencontainers.image.title="dnp3-interop-stepfunc" \
      org.opencontainers.image.description="DNP3 reference master and outstation built on the Step Function I/O dnp3 crate (non-commercial licence; for interoperability testing only)" \
      org.opencontainers.image.source="https://github.com/otfabric/dnp3-interop" \
      org.opencontainers.image.licenses="MIT AND LicenseRef-StepFunction-NonCommercial" \
      org.opencontainers.image.version="${ADAPTER_VERSION}"

USER interop
EXPOSE 20000

HEALTHCHECK --interval=2s --timeout=2s --start-period=10s --retries=15 \
  CMD test -f /run/dnp3-interop/ready

ENTRYPOINT ["/usr/local/bin/dnp3-interop"]
CMD ["server"]
