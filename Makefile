# Self-documented Makefile (https://marmelab.com/blog/2016/02/29/auto-documented-makefile.html)
# Run 'make' or 'make help' to list targets.
#
# Pre-push gate:  make ci
# Quick check:    make images smoke

.DEFAULT_GOAL := help

REGISTRY  ?= ghcr.io/otfabric
VERSION   ?= dev
PLATFORMS ?= linux/amd64,linux/arm64

# Registry of the base images (debian, golang, python, maven, eclipse-temurin).
# They are pinned by digest in the Dockerfiles, so any mirror of Docker Hub's
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

# Every adapter, in priority order: built and self-tested everywhere. Override
# to work on one: make smoke ADAPTERS=opendnp3
ADAPTERS ?= opendnp3 dscsystems dnp3py cbsigma nfmdnp3

# The adapters whose images are pushed to the registry. 'override': neither
# the environment nor the command line can change it.
override PUBLISHED_ADAPTERS := opendnp3 dscsystems dnp3py cbsigma nfmdnp3

# Parallel jobs for "make documents" (the smoke tests); the image builds take
# -j on the command line, as the workflows do: make -j4 images
JOBS ?= 4

image = $(REGISTRY)/dnp3-interop-$(1)

export REGISTRY VERSION

# image-%, smoke-% and candidate-% are pattern rules, which make ignores for .PHONY
# targets: they depend on FORCE instead.
.PHONY: help all ci validate documents lint lint-shell lint-docker lint-python lint-go test-scripts test-go fuzz vulncheck images smoke interop features qualify report print-adapters buildx publish buildx-setup print-published-adapters publish-arch publish-manifest digests run-opendnp3 run-dscsystems capabilities clean FORCE
FORCE:

help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z0-9_%-]+:.*?## / {printf "\033[36m%-22s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

all: validate images ## Validate fixtures and build the local images

ci: validate lint test-scripts test-go vulncheck images image-dnp3c documents ## Full local CI: validate, lint, script and Go tests, govulncheck, build, smoke and cross-stack self-test, document schemas
	@echo ""
	@echo "All local CI checks passed."

# ── Validation ───────────────────────────────────────────────────────────────

validate: ## Validate every fixture against the schema and the cross-field rules; check versions.yaml, RELEASE.md and (with GH_TOKEN) that the issues of tests/known-bugs.tsv are open
	@scripts/check-versions.sh
	@scripts/release-notes.sh > /dev/null && echo "ok   RELEASE.md has the notes of interop_version"
	@scripts/check-known-bugs.sh
	@python3 -m unittest discover -q -s scripts -p 'test_*.py'
	@echo "Validating fixtures..."
	@if python3 -c 'import jsonschema' 2>/dev/null; then \
		python3 scripts/validate-fixtures.py --require-schema; \
	else \
		echo "(jsonschema not installed locally: running the validator in a container)"; \
		$(RETRY) docker run --rm -v "$(CURDIR):/work:ro" -w /work $(PYTHON_IMAGE) \
			sh -c '$(PIP_INSTALL) scripts/requirements.txt && python scripts/validate-fixtures.py --require-schema'; \
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
		$(RETRY) docker run --rm -v "$(CURDIR):/work:ro" -w /work $(PYTHON_IMAGE) \
			sh -c '$(PIP_INSTALL) scripts/requirements.txt && python scripts/validate-documents.py reports'; \
	fi

# The linters, the test runner and the Python of the validators, pinned by
# version and digest (multi-arch indexes), pulled through TOOLS_REGISTRY. The
# Python packages are pinned with their hashes in scripts/requirements*.txt.
SHELLCHECK_IMAGE ?= $(TOOLS_REGISTRY)/koalaman/shellcheck:v0.11.0@sha256:61862eba1fcf09a484ebcc6feea46f1782532571a34ed51fedf90dd25f925a8d
HADOLINT_IMAGE   ?= $(TOOLS_REGISTRY)/hadolint/hadolint:v2.15.1@sha256:32dac94127fd60b7b7e3fbfc65e1383b9b5e25c9bfd7b8536de7a539fe68a12d
PYTHON_IMAGE     ?= $(TOOLS_REGISTRY)/library/python:3.12.16-slim@sha256:5e2bdb1b0d542d5039cffb8181c65930093949194a65c365dfb1b0fe13a4fa5b
BATS_IMAGE       ?= $(TOOLS_REGISTRY)/bats/bats:1.14.0@sha256:5322b877351fda0cc435de8c6116de7d0a2ec79d7c680132a0ef329a633bc66f
PIP_INSTALL      := pip install -q --root-user-action=ignore --disable-pip-version-check --require-hashes --only-binary=:all: -r

# What is under version control (no build output, no other worktree).
SHELL_SCRIPTS = $(shell git ls-files '*.sh' '*.bats')
DOCKERFILES   = $(shell git ls-files '*Dockerfile')
PYTHON_FILES  = $(shell git ls-files '*.py')

lint: lint-shell lint-docker lint-python lint-go ## Lint the shell scripts, the Dockerfiles, the Python files and the Go modules (pinned linters in containers)

lint-shell: ## Shellcheck every shell script and test, at the default severity
	@echo "Linting shell scripts..."
	@$(RETRY) docker run --rm -v "$(CURDIR):/mnt:ro" -w /mnt $(SHELLCHECK_IMAGE) -x -P scripts $(SHELL_SCRIPTS)

lint-docker: ## Hadolint every Dockerfile
	@echo "Linting Dockerfiles..."
	@for f in $(DOCKERFILES); do \
		$(RETRY) docker run --rm -i $(HADOLINT_IMAGE) hadolint --file-path-in-report "$$f" - < "$$f" || exit 1; \
	done

lint-python: ## Ruff and bandit on every Python file
	@echo "Linting Python..."
	@$(RETRY) docker run --rm -v "$(CURDIR):/work:ro" -w /work $(PYTHON_IMAGE) sh -c '\
		$(PIP_INSTALL) scripts/requirements-lint.txt \
		&& ruff check --no-cache $(PYTHON_FILES) \
		&& bandit -q -c bandit.yaml $(PYTHON_FILES)'

# gosec on every Go module, tests included, with the Go of the images. No
# exclusions and no #nosec: a finding is fixed in the code.
lint-go: ## gosec on every Go module, tests included (in the golang image of the adapters)
	@for m in $(GO_MODULES); do \
		echo "gosec $$m..."; \
		$(call go_run,$$m,go run github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION) -quiet -tests ./...) || exit 1; \
	done

test-scripts: ## Unit tests of the shell helpers and check-versions.sh (bats, in a container)
	@echo "Testing scripts..."
	@$(RETRY) docker run --rm -v "$(CURDIR):/code:ro" -w /code $(BATS_IMAGE) tests/scripts

# ── Go modules: tests, fuzzing, vulnerabilities ─────────────────────────────
# In the golang image the adapters are built with (the digest of the
# Dockerfiles; scripts/check-versions.sh checks it), so the tests and
# govulncheck see the Go of the images. The module cache is a Docker volume.

GO_IMAGE ?= $(BASE_REGISTRY)/golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9
GOVULNCHECK_VERSION ?= v1.9.0
GOSEC_VERSION ?= v2.30.0
GO_MODULES := adapters/cbsigma adapters/dscsystems own/go-dnp3
# The modules with fuzz targets, and how long each target runs.
FUZZ_MODULES := adapters/cbsigma adapters/dscsystems
FUZZTIME ?= 30s
go_run = $(RETRY) docker run --rm -v "$(CURDIR):/src" -v dnp3-interop-gomod:/go/pkg/mod -w /src/$(1) \
	-e GOFLAGS=-mod=readonly -e GOTOOLCHAIN=local $(3) $(GO_IMAGE) sh -c '$(2)'

test-go: ## Vet and test the Go modules (the fuzz targets' seeds included)
	@for m in $(GO_MODULES); do \
		echo "Testing $$m..."; \
		$(call go_run,$$m,test -z "$$(gofmt -l .)" && go vet ./... && go test -count=1 ./...) || exit 1; \
	done

fuzz: ## Run every fuzz target of the Go adapters for FUZZTIME each
	@for m in $(FUZZ_MODULES); do \
		for t in $$(grep -ho '^func Fuzz[A-Za-z0-9]*' $$m/*_test.go | sed 's/^func //'); do \
			echo "Fuzzing $$m $$t for $(FUZZTIME)..."; \
			$(call go_run,$$m,go test -run "^$$" -fuzz "^$$FUZZ\$$" -fuzztime $(FUZZTIME) .,-e FUZZ=$$t) || exit 1; \
		done; \
	done

vulncheck: ## govulncheck every Go module, with the Go of the images
	@for m in $(GO_MODULES); do \
		echo "govulncheck $$m..."; \
		$(call go_run,$$m,go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...) || exit 1; \
	done

# ── Local single-arch images (loaded into Docker; used by smoke and interop) ──

images: $(addprefix image-,$(ADAPTERS)) ## Build every adapter image for the host architecture

image-%: FORCE ## Build one adapter image, e.g. image-opendnp3
	@echo "Building $* image ($(VERSION))..."
	@$(RETRY) docker build \
		--build-arg BASE_REGISTRY=$(BASE_REGISTRY) --build-arg ADAPTER_VERSION=$(VERSION) \
		--tag $(call image,$*):$(VERSION) \
		--file adapters/$*/Dockerfile \
		.

# The dnp3c runner (docs/CONFORMANCE.md): not an adapter, never published.
# An explicit rule, which takes precedence over image-%.
image-dnp3c: FORCE ## Build the dnp3c runner image (DNP3 IED Certification Procedure; not published)
	@echo "Building dnp3c image ($(VERSION))..."
	@$(RETRY) docker build \
		--build-arg BASE_REGISTRY=$(BASE_REGISTRY) --build-arg ADAPTER_VERSION=$(VERSION) \
		--tag $(call image,dnp3c):$(VERSION) \
		--file conformance/dnp3c/Dockerfile \
		.

# ── Self-tests (images must be built) ─────────────────────────────────────────

smoke: $(addprefix smoke-,$(ADAPTERS)) ## Container-contract smoke test of every adapter

smoke-%: FORCE ## Smoke test one adapter, e.g. smoke-opendnp3
	@scripts/smoke.sh $*

# nfm-dnp3 1.0.1 cannot poll an outstation that sends a confirmed null
# unsolicited response after it starts (tests/known-bugs.tsv,
# nfmdnp3-confirm-link-service): its smoke test polls the dnp3py fixture,
# which has unsolicited responses off.
smoke-nfmdnp3: export SMOKE_SERVER_FIXTURE := dnp3py

interop: ## Cross-stack self-test: every client against every server, on the baseline and the dnp3py fixture
	@scripts/interop.sh $(ADAPTERS)
	@FIXTURE=dnp3py scripts/interop.sh $(ADAPTERS)

features: ## Cross-stack self-test of contracts 1.1 to 1.5: TLS, UDP, serial, file transfer, secure authentication, ...
	@scripts/features.sh $(ADAPTERS)

# ── Candidates ────────────────────────────────────────────────────────────────
# Builds an adapter on another upstream version than the release pin and runs
# the self-tests on it against the other adapters at their pins. Bugs of that
# version listed in tests/known-bugs.tsv are reported as known, so the run
# shows whether everything else still works. scripts/latest-upstream.py prints
# the latest release of an adapter's upstream; the Upstream workflow runs
# this for every adapter whose pin is behind.

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

qualify: ## Scan the published images of VERSION from the registry with Trivy and write SBOMs into qualify/ (scripts/qualify.sh)
	@scripts/qualify.sh qualify $(PUBLISHED_ADAPTERS)

report: ## Write the compatibility report of the documents in reports/ (run make documents first)
	@python3 scripts/compat-report.py reports --title "dnp3-interop $(VERSION) compatibility report"

print-adapters: ## Print the adapters (ADAPTERS), one line
	@echo $(ADAPTERS)

print-published-adapters: ## Print the adapters whose images are published (PUBLISHED_ADAPTERS), one line
	@echo $(PUBLISHED_ADAPTERS)

capabilities: ## Print the capability document of every adapter
	@for a in $(ADAPTERS); do docker run --rm $(call image,$$a):$(VERSION) print-capabilities; done

# ── Run a reference server on the host ────────────────────────────────────────

OPENDNP3_PORT ?= 20001
DSCSYSTEMS_PORT ?= 20002

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

publish: ## Build and push the published multi-arch images under emulation, from one machine (requires VERSION=vX.Y.Z; the release workflow uses publish-arch and publish-manifest instead)
	@if ! echo "$(VERSION)" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+$$'; then \
		echo "Error: VERSION must be in the form v<major>.<minor>.<patch>"; exit 1; fi
	@for a in $(PUBLISHED_ADAPTERS); do \
		echo "Publishing $$a $(VERSION) for $(PLATFORMS)..."; \
		$(RETRY) docker buildx build $(builder_flag) --platform $(PLATFORMS) --push \
			--build-arg BASE_REGISTRY=$(BASE_REGISTRY) --build-arg ADAPTER_VERSION=$(VERSION) \
			--tag $(call image,$$a):$(VERSION) \
			--file adapters/$$a/Dockerfile . || exit 1; \
	done

# ── Multi-arch publish from native runners ────────────────────────────────────
# The release workflow builds every adapter on an amd64 and on an arm64 runner
# (no emulation), self-tests there and pushes what it tested of the published
# adapters (PUBLISHED_ADAPTERS) under <image>:<version>-<arch>.
# A last job joins the two into <image>:<version>.

ARCH ?= $(shell uname -m | sed -e 's/^x86_64$$/amd64/' -e 's/^aarch64$$/arm64/')
ARCHS ?= amd64 arm64

publish-arch: ## Push the local images of the published adapters as <image>:$(VERSION)-$(ARCH) (build and self-test them first)
	@if ! echo "$(VERSION)" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+$$'; then \
		echo "Error: VERSION must be in the form v<major>.<minor>.<patch>"; exit 1; fi
	@for a in $(PUBLISHED_ADAPTERS); do \
		got=$$(docker image inspect --format '{{.Architecture}}' $(call image,$$a):$(VERSION)) || exit 1; \
		if [ "$$got" != "$(ARCH)" ]; then echo "Error: $$a:$(VERSION) is $$got, not $(ARCH)"; exit 1; fi; \
		echo "Pushing $$a $(VERSION) for linux/$(ARCH)..."; \
		docker tag $(call image,$$a):$(VERSION) $(call image,$$a):$(VERSION)-$(ARCH) || exit 1; \
		$(RETRY) docker push --quiet $(call image,$$a):$(VERSION)-$(ARCH) || exit 1; \
	done

publish-manifest: ## Join the per-architecture images of the published adapters into the multi-arch <image>:$(VERSION)
	@for a in $(PUBLISHED_ADAPTERS); do \
		echo "Creating $$a $(VERSION) from: $(ARCHS)"; \
		$(RETRY) docker buildx imagetools create --tag $(call image,$$a):$(VERSION) \
			$(foreach arch,$(ARCHS),$(call image,$$a):$(VERSION)-$(arch)) || exit 1; \
		for arch in $(ARCHS); do \
			$(RETRY) docker buildx imagetools inspect $(call image,$$a):$(VERSION) --format '{{json .Manifest}}' \
				| tr -d ' \n' | grep -q "\"architecture\":\"$$arch\"" || { echo "Error: $$a:$(VERSION) has no linux/$$arch image"; exit 1; }; \
		done; \
	done

digests: ## Print "<adapter> <digest>" of the published multi-arch images of VERSION
	@for a in $(PUBLISHED_ADAPTERS); do \
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
