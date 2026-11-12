#!/usr/bin/env bats
# SPDX-License-Identifier: MIT
# scripts/check-known-bugs.sh without a token: it skips, counting the issues
# it would look up. With a token it asks the GitHub API (the validate job).

setup() {
    unset GH_TOKEN GITHUB_TOKEN
    SCRIPT="${BATS_TEST_DIRNAME}/../../scripts/check-known-bugs.sh"
}

@test "without a token it skips and succeeds" {
    run "${SCRIPT}"
    [ "$status" -eq 0 ]
    [[ "$output" == "skip known-bugs.tsv issues: no GH_TOKEN or GITHUB_TOKEN"* ]]
}

@test "only issue and pull request links are counted, once each" {
    tsv="${BATS_TEST_TMPDIR}/known-bugs.tsv"
    {
        printf '# a comment\thttps://github.com/a/b/issues/1\n'
        printf 'x\tv1\t.\tbug\tbaseline\tid-1\thttps://github.com/a/b/issues/1\n'
        printf 'x\tv1\t.\tbug\tfeatures\tid-2\thttps://github.com/a/b/issues/1\n'
        printf 'x\tv1\t.\tbug\tbaseline\tid-3\thttps://github.com/a/b/pull/7\n'
        printf 'x\tv1\t.\tbug\tbaseline\tid-4\thttps://github.com/a/b/blob/0123/x.go#L1\n'
    } > "${tsv}"
    run "${SCRIPT}" "${tsv}"
    [ "$status" -eq 0 ]
    [[ "$output" == *"(2 to check)" ]]
}
