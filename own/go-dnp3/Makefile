# The dnp3-interop-otfabric image, from the repository root:
#   make -f own/go-dnp3/Makefile image smoke interop features
#
# Apart from the top-level Makefile on purpose: this image is not in ADAPTERS
# and not in a release of the harness.

IMAGE ?= ghcr.io/otfabric/dnp3-interop-otfabric:dev
# The harness release of the reference images to test against.
VERSION ?= $(shell git tag -l 'v[0-9]*.[0-9]*.[0-9]*' --sort=-v:refname | head -1)
REFERENCES ?= opendnp3 dscsystems dnp3py
BASE_REGISTRY ?= docker.io/library

# The latest go-dnp3 release, as the workflow builds it.
UPSTREAM_VERSION ?= $(shell git ls-remote --tags --refs https://github.com/otfabric/go-dnp3.git 'v[0-9]*' | sed 's,.*refs/tags/,,' | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$$' | sort -V | tail -1)
UPSTREAM_SHA ?= $(shell git ls-remote --tags https://github.com/otfabric/go-dnp3.git 'refs/tags/$(UPSTREAM_VERSION)' 'refs/tags/$(UPSTREAM_VERSION)^{}' | tail -1 | cut -f1)

export VERSION
export ADAPTER_IMAGE_otfabric = $(IMAGE)

.PHONY: image smoke interop features

image:
	docker build --build-arg BASE_REGISTRY=$(BASE_REGISTRY) --build-arg UPSTREAM_VERSION=$(UPSTREAM_VERSION) --build-arg UPSTREAM_SHA=$(UPSTREAM_SHA) --build-arg ADAPTER_VERSION=$(UPSTREAM_VERSION) --tag $(IMAGE) --file own/go-dnp3/Dockerfile .

smoke:
	scripts/smoke.sh otfabric

# The own image against itself and the reference images, in both roles; the
# dnp3py outstation serves the dnp3py fixture only.
interop:
	scripts/interop.sh otfabric $(REFERENCES)
	FIXTURE=dnp3py scripts/interop.sh otfabric $(REFERENCES)

features:
	scripts/features.sh otfabric $(REFERENCES)
