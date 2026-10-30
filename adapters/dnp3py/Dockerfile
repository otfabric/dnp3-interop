# dnp3-interop adapter for dnp3py (client role only).
#
# Build from the repository root:
#   docker build -f adapters/dnp3py/Dockerfile -t dnp3-interop-dnp3py:dev .
#
# dnp3py is MIT, like the adapter. It is installed from PyPI at the version
# and hash in requirements.txt, mirrored in versions.yaml. See
# THIRD_PARTY_NOTICES.md.

# Where the base images come from. They are pinned by digest, so a mirror of
# Docker Hub serves exactly the same images; CI uses one to stay clear of
# Docker Hub's pull rate limit (see the Makefile).
ARG BASE_REGISTRY=docker.io/library
ARG PYTHON_DIGEST=sha256:fbecd461fc72d21b2143c6eaba25143738079154a6d0ea3da3aec724f50eed4a

FROM ${BASE_REGISTRY}/python:3.14.8-slim-trixie@${PYTHON_DIGEST}

# The release pin. "make candidate-dnp3py CANDIDATE_VERSION=<version>" overrides it to try another
# version without touching the pin (installed without a hash then).
ARG UPSTREAM_VERSION=0.6.0
# The commit of the release tag in https://github.com/craigpnnl/dnp3py, for
# print-capabilities (PyPI has no commit; requirements.txt has the hash).
ARG UPSTREAM_SHA=c00d5df22e779df72f7caa4402c3f34957eb276d
ARG ADAPTER_VERSION=dev

ENV PYTHONDONTWRITEBYTECODE=1 PIP_DISABLE_PIP_VERSION_CHECK=1 PIP_NO_CACHE_DIR=1 \
    DNP3_INTEROP_ADAPTER_VERSION=${ADAPTER_VERSION} DNP3_INTEROP_UPSTREAM_REVISION=${UPSTREAM_SHA}

COPY adapters/dnp3py/requirements.txt /opt/adapter/requirements.txt
RUN if grep -q "^dnp3py==${UPSTREAM_VERSION} " /opt/adapter/requirements.txt; then \
         pip install --require-hashes --only-binary=:all: -r /opt/adapter/requirements.txt; \
       else \
         pip install --only-binary=:all: "dnp3py==${UPSTREAM_VERSION}"; \
       fi \
    && mkdir -p /licenses/dnp3py \
    && cp "$(python -c 'import importlib.metadata as m; d=m.distribution("dnp3py"); print([str(d.locate_file(f)) for f in d.files if f.name == "LICENSE"][0])')" \
          /licenses/dnp3py/LICENSE \
    # pip and setuptools are not needed at run time; removing them keeps their
    # vendored packages (urllib3, msgpack, ...) and their findings out of the image.
    && python -m pip uninstall --yes --quiet pip setuptools wheel \
    && rm -rf /usr/local/lib/python3.14/ensurepip

COPY adapters/dnp3py/dnp3_interop.py /opt/adapter/dnp3_interop.py
# Compile-check the adapter, and fail the build if the dnp3py internals it uses moved.
RUN python -I -c "import sys; sys.path.insert(0, '/opt/adapter'); import dnp3_interop" \
    && printf '#!/bin/sh\nexec python3 -I /opt/adapter/dnp3_interop.py "$@"\n' > /usr/local/bin/dnp3-interop \
    && chmod 755 /usr/local/bin/dnp3-interop \
    && useradd --uid 10001 --no-create-home --shell /usr/sbin/nologin interop \
    && mkdir -p /run/dnp3-interop \
    && chown interop /run/dnp3-interop

COPY THIRD_PARTY_NOTICES.md /licenses/THIRD_PARTY_NOTICES.md
COPY fixtures /fixtures

LABEL org.opencontainers.image.title="dnp3-interop-dnp3py" \
      org.opencontainers.image.description="DNP3 reference master and outstation built on dnp3py" \
      org.opencontainers.image.source="https://github.com/otfabric/dnp3-interop" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${ADAPTER_VERSION}"

USER interop
EXPOSE 20000

HEALTHCHECK --interval=2s --timeout=2s --start-period=10s --retries=15 \
  CMD test -f /run/dnp3-interop/ready

ENTRYPOINT ["/usr/local/bin/dnp3-interop"]
CMD ["server"]
