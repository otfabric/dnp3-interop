# dnp3-interop adapter for nfm-dnp3 (client role only).
#
# Build from the repository root:
#   docker build -f adapters/nfmdnp3/Dockerfile -t dnp3-interop-nfmdnp3:dev .
#
# nfm-dnp3 (https://github.com/fxodell/dnp3py, import name dnp3py) is a pure
# Python DNP3 master; it has no outstation. Its package metadata declares the
# MIT licence; the repository has no LICENSE file (see licenses/NOTICE). It is
# installed from PyPI at the version and hash in requirements.txt, mirrored in
# versions.yaml. The adapter is named nfmdnp3 because the adapter dnp3py is
# another project (craigpnnl/dnp3py).

# Where the base images come from. They are pinned by digest, so a mirror of
# Docker Hub serves exactly the same images; CI uses one to stay clear of
# Docker Hub's pull rate limit (see the Makefile).
ARG BASE_REGISTRY=docker.io/library
ARG PYTHON_DIGEST=sha256:9aaa474097d094040649f2162a627c0049b408f52138d980916788d80f04802f

FROM ${BASE_REGISTRY}/python:3.14.10-slim-trixie@${PYTHON_DIGEST}

# The release pin. "make candidate-nfmdnp3 CANDIDATE_VERSION=<version>" overrides it to try another
# version without touching the pin (installed without a hash then).
ARG UPSTREAM_VERSION=1.0.1
# The commit of the release tag v1.0.1 in https://github.com/fxodell/dnp3py,
# for print-capabilities (PyPI has no commit; requirements.txt has the hash).
ARG UPSTREAM_SHA=8d1dc7f50d319a8dbb90e1c3091754d6ab02d1a6
ARG ADAPTER_VERSION=dev

ENV PYTHONDONTWRITEBYTECODE=1 PIP_DISABLE_PIP_VERSION_CHECK=1 PIP_NO_CACHE_DIR=1 \
    DNP3_INTEROP_ADAPTER_VERSION=${ADAPTER_VERSION} DNP3_INTEROP_UPSTREAM_REVISION=${UPSTREAM_SHA}

COPY adapters/nfmdnp3/requirements.txt /opt/adapter/requirements.txt
COPY adapters/nfmdnp3/licenses/ /licenses/nfm-dnp3/
RUN if grep -q "^nfm-dnp3==${UPSTREAM_VERSION} " /opt/adapter/requirements.txt; then \
         pip install --require-hashes --only-binary=:all: -r /opt/adapter/requirements.txt; \
       else \
         pip install --only-binary=:all: "nfm-dnp3==${UPSTREAM_VERSION}"; \
       fi \
    # pip and setuptools are not needed at run time; removing them keeps their
    # vendored packages (urllib3, msgpack, ...) and their findings out of the image.
    && python -m pip uninstall --yes --quiet pip setuptools wheel \
    && rm -rf /usr/local/lib/python3.14/ensurepip

COPY adapters/nfmdnp3/dnp3_interop.py /opt/adapter/dnp3_interop.py
# Compile-check the adapter, and fail the build if the nfm-dnp3 internals it uses moved.
RUN python -I -c "import sys; sys.path.insert(0, '/opt/adapter'); import dnp3_interop; \
from dnp3py import DNP3Master; \
assert all(hasattr(DNP3Master, n) for n in ('_send_request', '_receive_response'))" \
    && printf '#!/bin/sh\nexec python3 -I /opt/adapter/dnp3_interop.py "$@"\n' > /usr/local/bin/dnp3-interop \
    && chmod 755 /usr/local/bin/dnp3-interop \
    && useradd --uid 10001 --no-create-home --shell /usr/sbin/nologin interop \
    && mkdir -p /run/dnp3-interop \
    && chown interop /run/dnp3-interop

COPY THIRD_PARTY_NOTICES.md /licenses/THIRD_PARTY_NOTICES.md
COPY fixtures /fixtures

LABEL org.opencontainers.image.title="dnp3-interop-nfmdnp3" \
      org.opencontainers.image.description="DNP3 reference master built on nfm-dnp3" \
      org.opencontainers.image.source="https://github.com/otfabric/dnp3-interop" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${ADAPTER_VERSION}"

USER 10001
EXPOSE 20000

# The image has no server, so the readiness file is never written; the check
# is the contract's all the same.
HEALTHCHECK --interval=2s --timeout=2s --start-period=10s --retries=15 \
  CMD ["test", "-f", "/run/dnp3-interop/ready"]

ENTRYPOINT ["/usr/local/bin/dnp3-interop"]
CMD ["server"]
