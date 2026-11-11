#!/usr/bin/env bats
# SPDX-License-Identifier: MIT
# Unit tests of the helpers of scripts/lib.sh that need neither Docker nor jq.
# Run with "make test-scripts".

setup() {
    unset REGISTRY VERSION RUN_ID RESULTS_FILE VERBOSE SERVER_TAG
    # shellcheck source=scripts/lib.sh
    source "${BATS_TEST_DIRNAME}/../../scripts/lib.sh"
}

@test "image_of names the release image of an adapter" {
    run image_of opendnp3
    [ "$status" -eq 0 ]
    [ "$output" = "ghcr.io/otfabric/dnp3-interop-opendnp3:dev" ]
}

@test "image_of follows REGISTRY and VERSION" {
    REGISTRY=example.test/org VERSION=v1.2.3
    run image_of cbsigma
    [ "$output" = "example.test/org/dnp3-interop-cbsigma:v1.2.3" ]
}

@test "ADAPTER_IMAGE_<adapter> overrides the image of that adapter only" {
    export ADAPTER_IMAGE_otfabric=example.test/own:ci
    run image_of otfabric
    [ "$output" = "example.test/own:ci" ]
    run image_of dnp3py
    [ "$output" = "ghcr.io/otfabric/dnp3-interop-dnp3py:dev" ]
}

@test "the network and server names carry the run id" {
    RUN_ID=4242
    source "${BATS_TEST_DIRNAME}/../../scripts/lib.sh"
    [ "${NETWORK}" = "dnp3-interop-4242" ]
    run server_name dscsystems
    [ "$output" = "srv-dscsystems-one-4242" ]
    SERVER_TAG=two
    run server_name dscsystems
    [ "$output" = "srv-dscsystems-two-4242" ]
}

@test "ok and fail count and record one line each" {
    RESULTS_FILE="${BATS_TEST_TMPDIR}/results.tsv"
    ok "a check that passed"
    fail "a check that failed" 2>/dev/null
    ok "another"
    [ "${PASS}" -eq 2 ]
    [ "${FAIL}" -eq 1 ]
    run cat "${RESULTS_FILE}"
    [ "${lines[0]}" = "$(printf 'ok\ta check that passed')" ]
    [ "${lines[1]}" = "$(printf 'fail\ta check that failed')" ]
    [ "${#lines[@]}" -eq 3 ]
}

@test "record writes nothing without RESULTS_FILE" {
    cd "${BATS_TEST_TMPDIR}"
    run record ok "nothing to write to"
    [ "$status" -eq 0 ]
    [ -z "$(ls -A)" ]
}

@test "fail logs the check under the script's name" {
    run fail "the outstation did not answer"
    [ "$status" -eq 0 ]
    [[ "$output" == *"FAIL: the outstation did not answer"* ]]
}

@test "finish fails a run with a failure" {
    ok "one"
    fail "two" 2>/dev/null
    run finish
    [ "$status" -ne 0 ]
    [[ "$output" == *"1 passed, 1 failed"* ]]
}

@test "finish fails a run that passed nothing" {
    run finish
    [ "$status" -ne 0 ]
    [[ "$output" == *"0 passed, 0 failed"* ]]
}

@test "finish passes a run with passes only, and reports known bugs and skips" {
    ok "one"
    KNOWN=2 SKIPPED=3
    run finish
    [ "$status" -eq 0 ]
    [[ "$output" == *"1 passed, 0 failed, 2 known upstream bugs, 3 skipped (capability not declared)"* ]]
}

@test "waiver_id finds the waiver of a known bug's description" {
    WORK="${BATS_TEST_TMPDIR}"
    printf 'cbsigma\tv0.3.1\tthe description\tcbsigma-some-id\n' > "${WORK}/active"
    printf 'dnp3py\t0.6.0\tanother one\tdnp3py-other-id\n' >> "${WORK}/active"
    run waiver_id "another one"
    [ "$output" = "dnp3py-other-id" ]
    run waiver_id "not listed"
    [ -z "$output" ]
}

@test "at_pair splits client->server" {
    at_pair "smoke.integrity-poll" "opendnp3->cbsigma"
    [ "${RESULT_ID}" = "smoke.integrity-poll" ]
    [ "${RESULT_CLIENT}" = "opendnp3" ]
    [ "${RESULT_SERVER}" = "cbsigma" ]
}
