# SPDX-License-Identifier: MIT
#
# dnp3-interop adapter for cB-Sigma dnp3.
#
# Build from the repository root:
#   docker build -f adapters/cbsigma/Dockerfile -t dnp3-interop-cbsigma:dev .
#
# cB-Sigma dnp3 and cB-Sigma iotsec are Apache-2.0, goburrow/serial is MIT.
# The adapter is MIT and links them statically; the image carries their
# LICENSE and NOTICE files under /licenses. The pin is below, in go.mod
# (with its checksum in go.sum) and in versions.yaml.

# Where the base images come from. They are pinned by digest, so a mirror of
# Docker Hub serves exactly the same images.
ARG BASE_REGISTRY=docker.io/library
ARG GOLANG_DIGEST=sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9
ARG DEBIAN_DIGEST=sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2
# The snapshot of the Debian archive that debian:trixie-slim at that digest was
# built from (its /etc/apt/sources.list.d/debian.sources names it). Packages
# are installed from that snapshot at pinned versions, so a build gets the
# same packages every time; versions.yaml mirrors them.
ARG DEBIAN_SNAPSHOT=20261104T212339Z

# ---------------------------------------------------------------------------
# Stage 1: build the adapter (the upstream module linked in)
# ---------------------------------------------------------------------------
FROM ${BASE_REGISTRY}/golang:1.27.4-trixie@${GOLANG_DIGEST} AS build

# The release pin. "make candidate-cbsigma CANDIDATE_VERSION=<version>"
# overrides it to try another upstream version without touching the pin.
ARG UPSTREAM_VERSION=v0.3.1
# The commit of the release tag, for print-capabilities.
ARG UPSTREAM_SHA=91f2f33c90e978e48bf67d20bca1022e769ed4d6
ARG ADAPTER_VERSION=dev

ENV CGO_ENABLED=0 GOFLAGS=-mod=mod
WORKDIR /src
COPY adapters/cbsigma/go.mod adapters/cbsigma/go.sum ./

# At the pin the module must match go.sum; anything else is a candidate.
RUN if [ "$(go list -m -f '{{.Version}}' github.com/cB-Sigma/dnp3)" = "${UPSTREAM_VERSION}" ]; then \
         GOFLAGS=-mod=readonly go mod download && go mod verify; \
       else \
         go get "github.com/cB-Sigma/dnp3@${UPSTREAM_VERSION}"; \
       fi

COPY adapters/cbsigma/*.go ./
RUN test -z "$(gofmt -l .)" \
    && go vet ./... \
    && go build -trimpath -ldflags "-s -w \
         -X main.adapterVersion=${ADAPTER_VERSION} \
         -X main.upstreamVersion=$(go list -m -f '{{.Version}}' github.com/cB-Sigma/dnp3) \
         -X main.upstreamRevision=${UPSTREAM_SHA}" \
         -o /out/dnp3-interop . \
    && mkdir -p /out/licenses/dnp3 /out/licenses/iotsec /out/licenses/serial \
    && cp "$(go list -m -f '{{.Dir}}' github.com/cB-Sigma/dnp3)/LICENSE" \
          "$(go list -m -f '{{.Dir}}' github.com/cB-Sigma/dnp3)/NOTICE" /out/licenses/dnp3/ \
    && cp "$(go list -m -f '{{.Dir}}' github.com/cB-Sigma/iotsec)/LICENSE" \
          "$(go list -m -f '{{.Dir}}' github.com/cB-Sigma/iotsec)/NOTICE" /out/licenses/iotsec/ \
    && cp "$(go list -m -f '{{.Dir}}' github.com/goburrow/serial)/LICENSE" /out/licenses/serial/

# ---------------------------------------------------------------------------
# Stage 2: runtime
# ---------------------------------------------------------------------------
FROM ${BASE_REGISTRY}/debian:trixie-slim@${DEBIAN_DIGEST} AS runtime

ARG DEBIAN_SNAPSHOT
SHELL ["/bin/bash", "-o", "pipefail", "-c"]

ARG ADAPTER_VERSION=dev

# socat bridges a pseudo-terminal to TCP for DNP3 over serial (contract 1.1).
RUN sed -i -e "s|^URIs: http://deb.debian.org/debian\$|URIs: http://snapshot.debian.org/archive/debian/${DEBIAN_SNAPSHOT}|" \
              -e "s|^URIs: http://deb.debian.org/debian-security\$|URIs: http://snapshot.debian.org/archive/debian-security/${DEBIAN_SNAPSHOT}|" \
              /etc/apt/sources.list.d/debian.sources \
    && ! grep -q "^URIs: http://deb.debian.org" /etc/apt/sources.list.d/debian.sources \
    && apt-get -o Acquire::Check-Valid-Until=false update && apt-get install -y --no-install-recommends socat=1.8.0.3-1+deb13u1 \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --uid 10001 --no-create-home --shell /usr/sbin/nologin interop \
    && mkdir -p /run/dnp3-interop \
    && chown interop /run/dnp3-interop

COPY --from=build /out/dnp3-interop /usr/local/bin/dnp3-interop
COPY --from=build /out/licenses /licenses
COPY LICENSE /licenses/dnp3-interop/LICENSE
COPY THIRD_PARTY_NOTICES.md /licenses/THIRD_PARTY_NOTICES.md
COPY fixtures /fixtures

LABEL org.opencontainers.image.title="dnp3-interop-cbsigma" \
      org.opencontainers.image.description="DNP3 reference master and outstation built on cB-Sigma dnp3" \
      org.opencontainers.image.source="https://github.com/otfabric/dnp3-interop" \
      org.opencontainers.image.licenses="MIT AND Apache-2.0" \
      org.opencontainers.image.version="${ADAPTER_VERSION}"

USER 10001
EXPOSE 20000

HEALTHCHECK --interval=2s --timeout=2s --start-period=10s --retries=15 \
  CMD ["test", "-f", "/run/dnp3-interop/ready"]

ENTRYPOINT ["/usr/local/bin/dnp3-interop"]
CMD ["server"]
