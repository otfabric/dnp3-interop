#!/usr/bin/env bats
# SPDX-License-Identifier: MIT
# Tests of scripts/check-versions.sh on a copy of the build files, changed one
# pin at a time. Run with "make test-scripts".

setup() {
    REPO="${BATS_TEST_DIRNAME}/../.."
    COPY="${BATS_TEST_TMPDIR}/repo"
    mkdir -p "${COPY}/scripts" "${COPY}/conformance"
    cp "${REPO}/versions.yaml" "${REPO}/Makefile" "${COPY}/"
    cp "${REPO}/scripts/check-versions.sh" "${REPO}/scripts/qualify.sh" "${COPY}/scripts/"
    cp -R "${REPO}/adapters" "${REPO}/own" "${COPY}/"
    cp -R "${REPO}/conformance/dnp3c" "${COPY}/conformance/"
}

# change <file> <sed expression>: edit one file of the copy.
change() {
    sed -i.orig -e "$2" "${COPY}/$1"
    ! cmp -s "${COPY}/$1" "${COPY}/$1.orig" || { echo "the expression changed nothing in $1"; return 1; }
    rm "${COPY}/$1.orig"
}

@test "the pins of this revision agree" {
    run "${COPY}/scripts/check-versions.sh"
    [ "$status" -eq 0 ]
    [[ "$output" != *FAIL* ]]
}

@test "a base image digest that is not in versions.yaml fails" {
    change adapters/cbsigma/Dockerfile 's/^ARG GOLANG_DIGEST=sha256:.*/ARG GOLANG_DIGEST=sha256:0000000000000000000000000000000000000000000000000000000000000000/'
    run "${COPY}/scripts/check-versions.sh"
    [ "$status" -eq 1 ]
    [[ "$output" == *"FAIL cbsigma golang digest"* ]]
}

@test "an upstream version that go.mod does not name fails" {
    change adapters/cbsigma/go.mod 's|github.com/cB-Sigma/dnp3 v[0-9.]*|github.com/cB-Sigma/dnp3 v9.9.9|'
    run "${COPY}/scripts/check-versions.sh"
    [ "$status" -eq 1 ]
    [[ "$output" == *"FAIL cB-Sigma dnp3 in Dockerfile and go.mod"* ]]
}

@test "a version in requirements.txt that the Dockerfile does not name fails" {
    change adapters/dnp3py/requirements.txt 's/^dnp3py==[^ ]*/dnp3py==9.9.9/'
    run "${COPY}/scripts/check-versions.sh"
    [ "$status" -eq 1 ]
    [[ "$output" == *"FAIL dnp3py in Dockerfile and requirements.txt"* ]]
}

@test "a revision that versions.yaml does not record fails" {
    change adapters/dscsystems/Dockerfile 's/^ARG UPSTREAM_SHA=.*/ARG UPSTREAM_SHA=1111111111111111111111111111111111111111/'
    run "${COPY}/scripts/check-versions.sh"
    [ "$status" -eq 1 ]
    [[ "$output" == *"FAIL DSC go-dnp3 revision"* ]]
}

@test "a Debian package without a version fails" {
    change adapters/dscsystems/Dockerfile 's/ socat=[^ ]* / socat /'
    run "${COPY}/scripts/check-versions.sh"
    [ "$status" -eq 1 ]
    [[ "$output" == *"FAIL adapters/dscsystems/Dockerfile: Debian package socat has no version"* ]]
}

@test "a Debian package version that versions.yaml does not record fails" {
    change adapters/opendnp3/Dockerfile 's/^      cmake=[^ ]* /      cmake=0.0.1-1 /'
    run "${COPY}/scripts/check-versions.sh"
    [ "$status" -eq 1 ]
    [[ "$output" == *"FAIL adapters/opendnp3/Dockerfile cmake"* ]]
}

@test "a Debian snapshot that versions.yaml does not record fails" {
    change own/go-dnp3/Dockerfile 's/^ARG DEBIAN_SNAPSHOT=.*/ARG DEBIAN_SNAPSHOT=20200101T000000Z/'
    run "${COPY}/scripts/check-versions.sh"
    [ "$status" -eq 1 ]
    [[ "$output" == *"FAIL own/go-dnp3/Dockerfile Debian snapshot"* ]]
}

@test "a tool image of the Makefile that versions.yaml does not record fails" {
    change Makefile 's|hadolint/hadolint:v[0-9.]*@sha256:[0-9a-f]*|hadolint/hadolint:latest|'
    run "${COPY}/scripts/check-versions.sh"
    [ "$status" -eq 1 ]
    [[ "$output" == *"FAIL Makefile HADOLINT_IMAGE"* ]]
}

@test "a Trivy of qualify.sh that versions.yaml does not record fails" {
    change scripts/qualify.sh 's|trivy:[0-9.]*@sha256:[0-9a-f]*|trivy:0.0.1@sha256:0000|'
    run "${COPY}/scripts/check-versions.sh"
    [ "$status" -eq 1 ]
    [[ "$output" == *"FAIL qualify.sh Trivy"* ]]
}

@test "a base image digest of own/ that versions.yaml does not record fails" {
    change own/go-dnp3/Dockerfile 's/^ARG GOLANG_DIGEST=sha256:.*/ARG GOLANG_DIGEST=sha256:0000000000000000000000000000000000000000000000000000000000000000/'
    run "${COPY}/scripts/check-versions.sh"
    [ "$status" -eq 1 ]
    [[ "$output" == *"FAIL own/go-dnp3/Dockerfile base image"* ]]
}

@test "a base image whose used_by does not name an image that pins it fails" {
    change versions.yaml 's/^    used_by: \[dscsystems, cbsigma, otfabric\]$/    used_by: [dscsystems, cbsigma]/'
    run "${COPY}/scripts/check-versions.sh"
    [ "$status" -eq 1 ]
    [[ "$output" == *"FAIL own/go-dnp3/Dockerfile: the used_by of the base image"*"does not name otfabric"* ]]
}

@test "a used_by that names an image which does not pin that base image fails" {
    change versions.yaml 's/^    used_by: \[dnp3py, nfmdnp3\]$/    used_by: [dnp3py, nfmdnp3, opendnp3]/'
    run "${COPY}/scripts/check-versions.sh"
    [ "$status" -eq 1 ]
    [[ "$output" == *"used_by names opendnp3, whose Dockerfile does not pin it"* ]]
}

@test "an implementation without published fails" {
    change versions.yaml '/^    published: false  *# on hold/d'
    run "${COPY}/scripts/check-versions.sh"
    [ "$status" -eq 1 ]
    [[ "$output" == *"FAIL tmw: no published: true or false"* ]]
}

@test "published that disagrees with PUBLISHED_ADAPTERS fails" {
    change Makefile 's/^override PUBLISHED_ADAPTERS := \(.*\) nfmdnp3$/override PUBLISHED_ADAPTERS := \1 tmw/'
    run "${COPY}/scripts/check-versions.sh"
    [ "$status" -eq 1 ]
    [[ "$output" == *"FAIL nfmdnp3: published: true, but not in PUBLISHED_ADAPTERS"* ]]
    [[ "$output" == *"FAIL tmw: published: false, but in PUBLISHED_ADAPTERS"* ]]
}

@test "a wheel hash that requirements.txt does not pin fails" {
    change adapters/dnp3py/requirements.txt 's/--hash=sha256:[0-9a-f]*/--hash=sha256:0000000000000000000000000000000000000000000000000000000000000000/'
    run "${COPY}/scripts/check-versions.sh"
    [ "$status" -eq 1 ]
    [[ "$output" == *"FAIL dnp3py wheel_sha256 and requirements.txt"* ]]
}

@test "a module sum that go.sum does not record fails" {
    change adapters/dscsystems/go.sum 's|^github.com/dscsystems/go-dnp3 \(v[0-9.]*\) h1:.*$|github.com/dscsystems/go-dnp3 \1 h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=|'
    run "${COPY}/scripts/check-versions.sh"
    [ "$status" -eq 1 ]
    [[ "$output" == *"FAIL dscsystems module_sum and go.sum"* ]]
}

@test "an implementation without a source hash fails" {
    change versions.yaml '/^    source_sha256: /d'
    run "${COPY}/scripts/check-versions.sh"
    [ "$status" -eq 1 ]
    [[ "$output" == *"FAIL opendnp3: no wheel_sha256, module_sum or source_sha256"* ]]
}
