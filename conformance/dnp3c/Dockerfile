# dnp3-interop's dnp3c runner: Step Function I/O's dnp3c, the DNP Users
# Group's DNP3 IED Certification Procedure, with an integration plugin that
# drives a device under test through its control port (docs/CONFORMANCE.md).
#
# Build from the repository root:
#   docker build -f conformance/dnp3c/Dockerfile -t dnp3-interop-dnp3c:dev .
#
# dnp3c is GPL-3.0-only, and so is the plugin, which links it. The build
# fetches dnp3c and its dependencies from Maven Central. The image is built
# locally by each user and not published: see THIRD_PARTY_NOTICES.md. The pin
# is DNP3C_VERSION, also in pom.xml and versions.yaml.

# Where the base images come from. They are pinned by digest, so a mirror of
# Docker Hub serves exactly the same images (see the Makefile).
ARG BASE_REGISTRY=docker.io/library
ARG MAVEN_DIGEST=sha256:a0603aab698040d9c94259f379ec0487da1678560748d6c7508483034033c53d
ARG JRE_DIGEST=sha256:39d618b8fda3408cfe792db2b666c0e3db8ea4ae263ab4d0c3805933e5355f45

# ---------------------------------------------------------------------------
# Stage 1: build the plugin and gather dnp3c with its dependencies
# ---------------------------------------------------------------------------
FROM ${BASE_REGISTRY}/maven:3.9.12-eclipse-temurin-17-noble@${MAVEN_DIGEST} AS build

ARG DNP3C_VERSION=0.3.0

WORKDIR /src
COPY conformance/dnp3c/pom.xml ./
# Strict checksums (-C): every artifact must match the checksum Maven Central
# publishes for it. A download from Maven Central that stalls or fails on the
# runner is retried by the resolver (bounded request timeout), and the
# resolution and packaging steps are each tried three times; a real failure (a missing
# artifact, a checksum mismatch) fails all three.
ENV MAVEN_ARGS="-B -C -Daether.connector.connectTimeout=30000 -Daether.connector.requestTimeout=120000 -Daether.connector.http.retryHandler.count=5"
RUN for attempt in 1 2 3; do \
        mvn -q -Ddnp3c.version=${DNP3C_VERSION} dependency:go-offline && exit 0; \
        echo "dependency:go-offline: attempt ${attempt} failed" >&2; sleep 20; \
    done; exit 1
COPY conformance/dnp3c/src ./src
# go-offline does not gather every artifact the build plugins load (slf4j-api
# of the Scala plugin, say), so packaging is online and retried the same way.
RUN for attempt in 1 2 3; do \
        mvn -q -Ddnp3c.version=${DNP3C_VERSION} package \
            dependency:copy-dependencies -DincludeScope=runtime -DoutputDirectory=target/lib && break; \
        [ "${attempt}" -lt 3 ] || exit 1; \
        echo "package: attempt ${attempt} failed" >&2; sleep 20; \
    done \
    && cp target/dnp3-interop-dnp3c-*.jar target/lib/

# ---------------------------------------------------------------------------
# Stage 2: the runtime
# ---------------------------------------------------------------------------
FROM ${BASE_REGISTRY}/eclipse-temurin:17.0.20_8-jre-noble@${JRE_DIGEST}

ARG DNP3C_VERSION=0.3.0
ARG ADAPTER_VERSION=dev
LABEL org.opencontainers.image.title="dnp3-interop-dnp3c" \
      org.opencontainers.image.description="dnp3c (DNP3 IED Certification Procedure) with dnp3-interop's integration plugin; not published" \
      org.opencontainers.image.licenses="GPL-3.0-only" \
      org.opencontainers.image.version="${ADAPTER_VERSION}" \
      io.otfabric.dnp3c.version="${DNP3C_VERSION}"

COPY --from=build /src/target/lib/ /opt/dnp3c/lib/
COPY licenses/GPL-3.0.txt /licenses/GPL-3.0.txt
COPY THIRD_PARTY_NOTICES.md /licenses/THIRD_PARTY_NOTICES.md
COPY conformance/LICENSE /licenses/conformance/LICENSE

# Results go to the working directory: mount a directory there, with the
# device's dnp3c configuration in it.
WORKDIR /work
ENTRYPOINT ["java", "-cp", "/opt/dnp3c/lib/*", "io.otfabric.dnp3interop.conformance.Run"]
