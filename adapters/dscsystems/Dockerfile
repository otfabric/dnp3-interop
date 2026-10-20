# dnp3-interop adapter for DSC Systems go-dnp3.
#
# Build from the repository root:
#   docker build -f adapters/dscsystems/Dockerfile -t dnp3-interop-dscsystems:dev .
#
# go-dnp3 is GPL-3.0. The adapter links it statically and is licensed
# GPL-3.0-or-later; this image is distributed under GPL-3.0. See
# THIRD_PARTY_NOTICES.md. The pin is below, in go.mod (with its checksum in
# go.sum) and in versions.yaml.

# Where the base images come from. They are pinned by digest, so a mirror of
# Docker Hub serves exactly the same images; CI uses one to stay clear of
# Docker Hub's pull rate limit (see the Makefile).
ARG BASE_REGISTRY=docker.io/library
ARG GOLANG_DIGEST=sha256:47667934874913a449b96faa7ef4777fa285d9ec29126ee80ed61c45b7cccf0e
ARG DEBIAN_DIGEST=sha256:a29215f6a35e51e22adffa17f89e9d2ef06214e64a2bad10d765c46aea49f11f

# ---------------------------------------------------------------------------
# Stage 1: build the adapter (the upstream module linked in)
# ---------------------------------------------------------------------------
FROM ${BASE_REGISTRY}/golang:1.27.2-trixie@${GOLANG_DIGEST} AS build

# The release pin. "make candidate-dscsystems CANDIDATE_VERSION=<version>" overrides it to try another
# upstream version (a tag or a commit) without touching the pin.
ARG UPSTREAM_VERSION=v0.8.1
# The commit of the release tag, for print-capabilities (the module proxy
# serves the tag; go.sum has the checksum).
ARG UPSTREAM_SHA=70ff72bb09755db183d1bd69859adc72cd663aec
ARG ADAPTER_VERSION=dev

ENV CGO_ENABLED=0 GOFLAGS=-mod=mod
WORKDIR /src
COPY adapters/dscsystems/go.mod adapters/dscsystems/go.sum ./

# At the pin the module must match go.sum; anything else is a candidate.
RUN if [ "$(go list -m -f '{{.Version}}' github.com/dscsystems/go-dnp3)" = "${UPSTREAM_VERSION}" ]; then \
         GOFLAGS=-mod=readonly go mod download && go mod verify; \
       else \
         go get "github.com/dscsystems/go-dnp3@${UPSTREAM_VERSION}"; \
       fi

COPY adapters/dscsystems/*.go ./
RUN test -z "$(gofmt -l .)" \
    && go vet ./... \
    && go build -trimpath -ldflags "-s -w \
         -X main.adapterVersion=${ADAPTER_VERSION} \
         -X main.upstreamVersion=$(go list -m -f '{{.Version}}' github.com/dscsystems/go-dnp3) \
         -X main.upstreamRevision=${UPSTREAM_SHA}" \
         -o /out/dnp3-interop . \
    && mkdir -p /out/licenses/go-dnp3 \
    && cp "$(go list -m -f '{{.Dir}}' github.com/dscsystems/go-dnp3)/LICENSE" \
          "$(go list -m -f '{{.Dir}}' github.com/dscsystems/go-dnp3)/NOTICE" /out/licenses/go-dnp3/

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
COPY licenses/GPL-3.0.txt /licenses/GPL-3.0.txt
COPY THIRD_PARTY_NOTICES.md /licenses/THIRD_PARTY_NOTICES.md
COPY fixtures /fixtures

LABEL org.opencontainers.image.title="dnp3-interop-dscsystems" \
      org.opencontainers.image.description="DNP3 reference master and outstation built on DSC Systems go-dnp3" \
      org.opencontainers.image.source="https://github.com/otfabric/dnp3-interop" \
      org.opencontainers.image.licenses="GPL-3.0-or-later" \
      org.opencontainers.image.version="${ADAPTER_VERSION}"

USER interop
EXPOSE 20000

HEALTHCHECK --interval=2s --timeout=2s --start-period=10s --retries=15 \
  CMD test -f /run/dnp3-interop/ready

ENTRYPOINT ["/usr/local/bin/dnp3-interop"]
CMD ["server"]
