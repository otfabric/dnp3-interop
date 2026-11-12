# SPDX-License-Identifier: MIT
#
# dnp3-interop-otfabric: OT Fabric's own DNP3 stack behind the container contract.
# Not an independent implementation: see README.md.
#
# Build from the repository root:
#   make -f own/go-dnp3/Makefile image
#
# The build takes the go-dnp3 release as UPSTREAM_VERSION (the released git tag,
# which is the Go module version) and its commit as UPSTREAM_SHA. There is no pin
# in this repository: the workflow passes the latest release. A build never uses
# main. go.mod and go.sum here are only a starting point for local work.

ARG BASE_REGISTRY=docker.io/library
# golang:1.27.4-trixie and debian:trixie-slim, multi-arch indexes
# (linux/amd64 and linux/arm64): the digests of the reference images.
ARG GOLANG_DIGEST=sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9
ARG DEBIAN_DIGEST=sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2
# The snapshot of the Debian archive that debian:trixie-slim at that digest was
# built from (its /etc/apt/sources.list.d/debian.sources names it). Packages
# are installed from that snapshot at pinned versions, so a build gets the
# same packages every time; versions.yaml mirrors them.
ARG DEBIAN_SNAPSHOT=20261104T212339Z

FROM ${BASE_REGISTRY}/golang:1.27.4-trixie@${GOLANG_DIGEST} AS build

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

ARG UPSTREAM_VERSION
ARG UPSTREAM_SHA
ARG ADAPTER_VERSION=dev
ARG GOPROXY=https://proxy.golang.org

ENV CGO_ENABLED=0 GOPROXY=${GOPROXY} GOSUMDB=sum.golang.org GOTOOLCHAIN=local
WORKDIR /src
COPY own/go-dnp3/go.mod own/go-dnp3/go.sum own/go-dnp3/*.go ./

RUN set -eu; \
    test -n "${UPSTREAM_VERSION}" || { echo "UPSTREAM_VERSION is required" >&2; exit 1; }; \
    echo "${UPSTREAM_SHA}" | grep -Eq '^[0-9a-f]{40}$'; \
    go get "github.com/otfabric/go-dnp3@${UPSTREAM_VERSION}"; \
    go mod tidy; \
    built="$(go list -m -f '{{.Version}}' github.com/otfabric/go-dnp3)"; \
    test "${built}" = "${UPSTREAM_VERSION}" || { echo "go.mod has go-dnp3 ${built}, want ${UPSTREAM_VERSION}" >&2; exit 1; }; \
    go mod download && go mod verify; \
    origin="$(go mod download -json "github.com/otfabric/go-dnp3@${UPSTREAM_VERSION}" \
        | sed -nE 's/^[[:space:]]*"Hash": "([0-9a-f]{40})".*/\1/p')"; \
    if [ -n "${origin}" ] && [ "${origin}" != "${UPSTREAM_SHA}" ]; then \
        echo "go-dnp3 ${UPSTREAM_VERSION} is commit ${origin}, not ${UPSTREAM_SHA}" >&2; exit 1; \
    fi

RUN set -eu; \
    test -z "$(gofmt -l .)"; \
    go vet ./...; \
    go test ./...; \
    go build -trimpath -ldflags "-s -w \
        -X main.adapterVersion=${ADAPTER_VERSION} \
        -X main.upstreamVersion=${UPSTREAM_VERSION} \
        -X main.upstreamRevision=${UPSTREAM_SHA}" \
        -o /out/dnp3-interop .; \
    mkdir -p /out/licenses/go; \
    cp "$(go env GOROOT)/LICENSE" /out/licenses/go/LICENSE; \
    go version -m /out/dnp3-interop | awk '$1 == "dep" { print $2, $3 }' > /out/licenses/BUNDLED-MODULES.txt; \
    test -s /out/licenses/BUNDLED-MODULES.txt; \
    while read -r module modversion; do \
        dir="$(go list -m -f '{{.Dir}}' "${module}")"; \
        mkdir -p "/out/licenses/${module}"; \
        for file in "${dir}"/LICENSE* "${dir}"/LICENCE* "${dir}"/COPYING* "${dir}"/NOTICE*; do \
            if [ -f "${file}" ]; then cp "${file}" "/out/licenses/${module}/"; fi; \
        done; \
        test -n "$(ls "/out/licenses/${module}")" || { echo "no licence file in ${module} ${modversion}" >&2; exit 1; }; \
    done < /out/licenses/BUNDLED-MODULES.txt

FROM ${BASE_REGISTRY}/debian:trixie-slim@${DEBIAN_DIGEST} AS runtime

ARG DEBIAN_SNAPSHOT
SHELL ["/bin/bash", "-o", "pipefail", "-c"]

ARG ADAPTER_VERSION=dev
ARG UPSTREAM_VERSION

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

LABEL org.opencontainers.image.title="dnp3-interop-otfabric" \
      org.opencontainers.image.description="OT Fabric go-dnp3 behind the dnp3-interop container contract, for testing. Not an independent implementation." \
      org.opencontainers.image.source="https://github.com/otfabric/dnp3-interop" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${UPSTREAM_VERSION}"

USER 10001
EXPOSE 20000

HEALTHCHECK --interval=2s --timeout=2s --start-period=10s --retries=15 \
  CMD ["test", "-f", "/run/dnp3-interop/ready"]

ENTRYPOINT ["/usr/local/bin/dnp3-interop"]
CMD ["server"]
