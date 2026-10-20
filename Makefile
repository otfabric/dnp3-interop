# Self-documented Makefile (https://marmelab.com/blog/2016/02/29/auto-documented-makefile.html)
# Run 'make' or 'make help' to list targets.
#
# Pre-push gate:  make ci
# Quick check:    make images smoke

.DEFAULT_GOAL := help

REGISTRY  ?= ghcr.io/otfabric
VERSION   ?= dev
PLATFORMS ?= linux/amd64,linux/arm64

# Registry of the base images (debian, rust). They
# are pinned by digest in the Dockerfiles, so any mirror of Docker Hub's
# official images gives the same build. The workflows set
#   BASE_REGISTRY=mirror.gcr.io/library
# because anonymous pulls from Docker Hub are rate limited per runner address.
BASE_REGISTRY ?= docker.io/library

# Registry of the images the build tooling itself runs: BuildKit, the QEMU
# handlers, shellcheck, Python. The workflows set TOOLS_REGISTRY=mirror.gcr.io.
TOOLS_REGISTRY ?= docker.io

# Everything that pulls from or pushes to a registry goes through this: it
# repeats a command that failed on a rate limit, a 5xx or a timeout, and only
# then. See scripts/retry.sh.
RETRY := scripts/retry.sh

# Name of the docker-container builder "make buildx-setup" creates.
BUILDER ?= dnp3-interop

# Adapters with a published image, in priority order. The self-tests run over
# all of them; override to work on one: make smoke ADAPTERS=opendnp3
ADAPTERS ?= stepfunc opendnp3 dscsystems dnp3py

# Parallel jobs for "make documents" (the smoke tests); the image builds take
# -j on the command line, as the workflows do: make -j4 images
JOBS ?= 4

image = $(REGISTRY)/dnp3-interop-$(1)

export REGISTRY VERSION

# image-%, smoke-% and candidate-% are pattern rules, which make ignores for .PHONY
# targets: they depend on FORCE instead.
.PHONY: help all ci validate documents lint images smoke interop features qualify report print-adapters buildx publish buildx-setup publish-arch publish-manifest digests run-stepfunc run-opendnp3 run-dscsystems capabilities clean FORCE
FORCE:

help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z0-9_%-]+:.*?## / {printf "\033[36m%-22s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

all: validate images ## Validate fixtures and build the local images

ci: validate lint images documents ## Full local CI: validate, lint, build, smoke and cross-stack self-test, document schemas
	@echo ""
	@echo "All local CI checks passed."

# ── Validation ───────────────────────────────────────────────────────────────

validate: ## Validate every fixture against the schema and the cross-field rules; check versions.yaml
	@scripts/check-versions.sh
	@echo "Validating fixtures..."
	@if python3 -c 'import jsonschema' 2>/dev/null; then \
		python3 scripts/validate-fixtures.py --require-schema; \
	else \
		echo "(jsonschema not installed locally: running the validator in a container)"; \
		$(RETRY) docker run --rm -v "$(CURDIR):/work:ro" -w /work $(TOOLS_REGISTRY)/library/python:3.12-slim \
			sh -c 'pip install -q --root-user-action=ignore jsonschema && python scripts/validate-fixtures.py --require-schema'; \
	fi

documents: ## Run the self-tests keeping every document, and validate them against schemas/
	@rm -rf reports qualify && mkdir -p reports
	@REPORT_DIR=reports $(MAKE) --no-print-directory -j$(JOBS) smoke
	@REPORT_DIR=reports $(MAKE) --no-print-directory interop
	@REPORT_DIR=reports $(MAKE) --no-print-directory features
	@echo "Validating documents..."
	@if python3 -c 'import jsonschema' 2>/dev/null; then \
		python3 scripts/validate-documents.py reports; \
	else \
		$(RETRY) docker run --rm -v "$(CURDIR):/work:ro" -w /work $(TOOLS_REGISTRY)/library/python:3.12-slim \
			sh -c 'pip install -q --root-user-action=ignore jsonschema && python scripts/validate-documents.py reports'; \
	fi

lint: ## Shellcheck the scripts (the installed shellcheck, or a container when there is none)
	@echo "Linting scripts..."
	@if command -v shellcheck >/dev/null 2>&1; then \
		shellcheck --severity=warning -x -P scripts scripts/*.sh; \
	else \
		$(RETRY) docker run --rm -v "$(CURDIR):/mnt:ro" $(TOOLS_REGISTRY)/koalaman/shellcheck:stable --severity=warning -x -P scripts scripts/*.sh; \
	fi

# ── Local single-arch images (loaded into Docker; used by smoke and interop) ──

images: $(addprefix image-,$(ADAPTERS)) ## Build every adapter image for the host architecture

image-%: FORCE ## Build one adapter image, e.g. image-opendnp3
	@echo "Building $* image ($(VERSION))..."
	@$(RETRY) docker build \
		--build-arg BASE_REGISTRY=$(BASE_REGISTRY) --build-arg ADAPTER_VERSION=$(VERSION) \
		--tag $(call image,$*):$(VERSION) \
		--file adapters/$*/Dockerfile \
		.

# ── Self-tests (images must be built) ─────────────────────────────────────────

smoke: $(addprefix smoke-,$(ADAPTERS)) ## Container-contract smoke test of every adapter

smoke-%: FORCE ## Smoke test one adapter, e.g. smoke-stepfunc
	@scripts/smoke.sh $*

interop: ## Cross-stack self-test: every client against every server, on the baseline and the dnp3py fixture
	@scripts/interop.sh $(ADAPTERS)
	@FIXTURE=dnp3py scripts/interop.sh $(ADAPTERS)

features: ## Cross-stack self-test of contract 1.1: TLS, UDP, serial, file transfer, secure authentication
	@scripts/features.sh $(ADAPTERS)

# ── Upstream candidates ───────────────────────────────────────────────────────
# Builds an adapter on another upstream version than the release pin and runs
# the self-tests on it against the other adapters at their pins. Bugs of that
# version listed in tests/known-bugs.tsv are reported as known, so the run
# shows whether everything else still works. scripts/latest-upstream.py prints
# the latest release of an adapter's upstream; the Upstream candidates
# workflow runs this for every adapter whose pin is behind.

CANDIDATE_VERSION ?=
# opendnp3 is pinned by commit: give the commit of CANDIDATE_VERSION.
CANDIDATE_SHA ?=

candidate-%: FORCE ## Build one adapter on CANDIDATE_VERSION (opendnp3: and CANDIDATE_SHA) and self-test it, e.g. candidate-dnp3py CANDIDATE_VERSION=0.7.0
	@test -n "$(CANDIDATE_VERSION)" || { echo "Error: CANDIDATE_VERSION=<version> is required"; exit 1; }
	@if [ "$*" = opendnp3 ] && [ -z "$(CANDIDATE_SHA)" ]; then echo "Error: opendnp3 is pinned by commit: CANDIDATE_SHA=<commit> is required"; exit 1; fi
	@echo "Building $* candidate on $(CANDIDATE_VERSION) $(CANDIDATE_SHA)..."
	@$(RETRY) docker build \
		--build-arg BASE_REGISTRY=$(BASE_REGISTRY) --build-arg ADAPTER_VERSION=candidate \
		--build-arg UPSTREAM_VERSION=$(CANDIDATE_VERSION) --build-arg UPSTREAM_SHA=$(or $(CANDIDATE_SHA),candidate) \
		--tag $(call image,$*):candidate \
		--file adapters/$*/Dockerfile .
	@for a in $(filter-out $*,$(ADAPTERS)); do docker tag $(call image,$$a):$(VERSION) $(call image,$$a):candidate || exit 1; done
	@VERSION=candidate scripts/smoke.sh $*
	@VERSION=candidate scripts/interop.sh $(ADAPTERS)
	@VERSION=candidate FIXTURE=dnp3py scripts/interop.sh $(ADAPTERS)
	@VERSION=candidate scripts/features.sh $(ADAPTERS)

# ── Release qualification ─────────────────────────────────────────────────────

qualify: ## Scan the images of VERSION from the registry with Trivy and write SBOMs into qualify/ (scripts/qualify.sh)
	@scripts/qualify.sh qualify $(ADAPTERS)

report: ## Write the compatibility report of the documents in reports/ (run make documents first)
	@python3 scripts/compat-report.py reports --title "dnp3-interop $(VERSION) compatibility report"

print-adapters: ## Print the adapters (ADAPTERS), one line
	@echo $(ADAPTERS)

capabilities: ## Print the capability document of every adapter
	@for a in $(ADAPTERS); do docker run --rm $(call image,$$a):$(VERSION) print-capabilities; done

# ── Run a reference server on the host ────────────────────────────────────────

STEPFUNC_PORT ?= 20000
OPENDNP3_PORT ?= 20001
DSCSYSTEMS_PORT ?= 20002

run-stepfunc: ## Run the stepfunc outstation on 127.0.0.1:$(STEPFUNC_PORT)
	docker run --rm -p 127.0.0.1:$(STEPFUNC_PORT):20000 $(call image,stepfunc):$(VERSION) server

run-opendnp3: ## Run the opendnp3 outstation on 127.0.0.1:$(OPENDNP3_PORT)
	docker run --rm -p 127.0.0.1:$(OPENDNP3_PORT):20000 $(call image,opendnp3):$(VERSION) server

run-dscsystems: ## Run the DSC go-dnp3 outstation on 127.0.0.1:$(DSCSYSTEMS_PORT)
	docker run --rm -p 127.0.0.1:$(DSCSYSTEMS_PORT):20000 $(call image,dscsystems):$(VERSION) server

# ── Multi-arch build and publish ──────────────────────────────────────────────

buildx-setup: ## Prepare multi-arch builds: QEMU handlers and a docker-container builder (pulled through TOOLS_REGISTRY)
	@echo "Installing QEMU handlers and creating builder $(BUILDER)..."
	@$(RETRY) docker run --privileged --rm $(TOOLS_REGISTRY)/tonistiigi/binfmt:latest --install arm64,amd64 >/dev/null
	@docker buildx rm $(BUILDER) >/dev/null 2>&1 || true
	@$(RETRY) docker buildx create --name $(BUILDER) --driver docker-container \
		--driver-opt image=$(TOOLS_REGISTRY)/moby/buildkit:buildx-stable-1 --bootstrap >/dev/null
	@docker buildx inspect $(BUILDER) | grep -E '^(Name|Driver|Platforms)' || true

# Uses the builder of buildx-setup when there is one, the current builder otherwise.
builder_flag = $(shell docker buildx inspect $(BUILDER) >/dev/null 2>&1 && echo --builder $(BUILDER))

buildx: ## Build every image for $(PLATFORMS) under emulation, without pushing (local check; CI builds natively on both)
	@for a in $(ADAPTERS); do \
		echo "Building $$a for $(PLATFORMS)..."; \
		$(RETRY) docker buildx build $(builder_flag) --platform $(PLATFORMS) \
			--build-arg BASE_REGISTRY=$(BASE_REGISTRY) --build-arg ADAPTER_VERSION=$(VERSION) \
			--file adapters/$$a/Dockerfile . || exit 1; \
	done

publish: ## Build and push multi-arch images under emulation, from one machine (requires VERSION=vX.Y.Z; the release workflow uses publish-arch and publish-manifest instead)
	@if ! echo "$(VERSION)" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+$$'; then \
		echo "Error: VERSION must be in the form v<major>.<minor>.<patch>"; exit 1; fi
	@for a in $(ADAPTERS); do \
		echo "Publishing $$a $(VERSION) for $(PLATFORMS)..."; \
		$(RETRY) docker buildx build $(builder_flag) --platform $(PLATFORMS) --push \
			--build-arg BASE_REGISTRY=$(BASE_REGISTRY) --build-arg ADAPTER_VERSION=$(VERSION) \
			--tag $(call image,$$a):$(VERSION) \
			--file adapters/$$a/Dockerfile . || exit 1; \
	done

# ── Multi-arch publish from native runners ────────────────────────────────────
# The release workflow builds on an amd64 and on an arm64 runner (no
# emulation), self-tests there and pushes what it tested under
# <image>:<version>-<arch>. A last job joins the two into <image>:<version>.

ARCH ?= $(shell uname -m | sed -e 's/^x86_64$$/amd64/' -e 's/^aarch64$$/arm64/')
ARCHS ?= amd64 arm64

publish-arch: ## Push the local images of this machine as <image>:$(VERSION)-$(ARCH) (build and self-test them first)
	@if ! echo "$(VERSION)" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+$$'; then \
		echo "Error: VERSION must be in the form v<major>.<minor>.<patch>"; exit 1; fi
	@for a in $(ADAPTERS); do \
		got=$$(docker image inspect --format '{{.Architecture}}' $(call image,$$a):$(VERSION)) || exit 1; \
		if [ "$$got" != "$(ARCH)" ]; then echo "Error: $$a:$(VERSION) is $$got, not $(ARCH)"; exit 1; fi; \
		echo "Pushing $$a $(VERSION) for linux/$(ARCH)..."; \
		docker tag $(call image,$$a):$(VERSION) $(call image,$$a):$(VERSION)-$(ARCH) || exit 1; \
		$(RETRY) docker push --quiet $(call image,$$a):$(VERSION)-$(ARCH) || exit 1; \
	done

publish-manifest: ## Join the per-architecture images of VERSION into the multi-arch <image>:$(VERSION)
	@for a in $(ADAPTERS); do \
		echo "Creating $$a $(VERSION) from: $(ARCHS)"; \
		$(RETRY) docker buildx imagetools create --tag $(call image,$$a):$(VERSION) \
			$(foreach arch,$(ARCHS),$(call image,$$a):$(VERSION)-$(arch)) || exit 1; \
		for arch in $(ARCHS); do \
			$(RETRY) docker buildx imagetools inspect $(call image,$$a):$(VERSION) --format '{{json .Manifest}}' \
				| tr -d ' \n' | grep -q "\"architecture\":\"$$arch\"" || { echo "Error: $$a:$(VERSION) has no linux/$$arch image"; exit 1; }; \
		done; \
	done

digests: ## Print "<adapter> <digest>" of the published multi-arch images of VERSION
	@for a in $(ADAPTERS); do \
		d=$$($(RETRY) docker buildx imagetools inspect $(call image,$$a):$(VERSION) --format '{{.Manifest.Digest}}') || exit 1; \
		echo "$$a $$d"; \
	done

# ── Clean ─────────────────────────────────────────────────────────────────────

clean: ## Remove test containers, networks, local images and reports
	@docker ps -aq --filter "label=dnp3-interop.run" | xargs -r docker rm -f >/dev/null 2>&1 || true
	@docker network ls --format '{{.Name}}' | grep '^dnp3-interop-' | xargs -r docker network rm >/dev/null 2>&1 || true
	@docker compose down --remove-orphans 2>/dev/null || true
	@for a in $(ADAPTERS); do docker rmi $(call image,$$a):$(VERSION) 2>/dev/null || true; done
	@rm -rf reports
