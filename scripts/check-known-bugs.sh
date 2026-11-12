#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Fails when an upstream issue that tests/known-bugs.tsv cites is closed: the
# bug may be fixed, and the row (and maybe the pin) needs a look.
#
#   scripts/check-known-bugs.sh [known-bugs.tsv]
#
# Asks the GitHub API with GH_TOKEN or GITHUB_TOKEN. Without either it skips,
# successfully: a local "make validate" needs no network and no token. Links
# to code (blob/...) and commits are evidence, not issues, and are not looked
# up. GITHUB_API (default https://api.github.com) is for the tests.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FILE="${1:-${ROOT}/tests/known-bugs.tsv}"
API="${GITHUB_API:-https://api.github.com}"
TOKEN="${GH_TOKEN:-${GITHUB_TOKEN:-}}"

# The issue and pull request URLs of column 7, once each.
urls="$(awk -F'\t' '!/^#/ && NF >= 7 { print $7 }' "${FILE}" \
    | grep -E '^https://github\.com/[^/]+/[^/]+/(issues|pull)/[0-9]+$' | sort -u || true)"

if [[ -z "${TOKEN}" ]]; then
    echo "skip known-bugs.tsv issues: no GH_TOKEN or GITHUB_TOKEN ($(grep -c . <<<"${urls}") to check)"
    exit 0
fi

status=0
while read -r url; do
    [[ -n "${url}" ]] || continue
    path="${url#https://github.com/}"            # owner/repo/issues/N
    repo="${path%/*/*}" number="${path##*/}"
    if ! state="$(curl -fsS --retry 3 --max-time 30 \
            -H "Authorization: Bearer ${TOKEN}" -H "Accept: application/vnd.github+json" \
            "${API}/repos/${repo}/issues/${number}" | jq -r '.state')"; then
        echo "FAIL ${url}: the GitHub API did not answer"
        status=1
        continue
    fi
    case "${state}" in
        open) echo "ok   ${url}: open" ;;
        closed)
            echo "FAIL ${url}: closed upstream; check whether the bug is fixed and update tests/known-bugs.tsv"
            status=1 ;;
        *)  echo "FAIL ${url}: unexpected state '${state}'"; status=1 ;;
    esac
done <<< "${urls}"
exit "${status}"
