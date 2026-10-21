#!/usr/bin/env bash
# Release qualification of published images: a vulnerability scan and an SBOM
# per image and platform, with Trivy, and a Markdown summary of the scan.
#
# Usage: qualify.sh <output directory> <adapter> [<adapter> ...]
#
# The adapters are the published ones (make print-published-adapters); the
# stepfunc image is never published, so it cannot be scanned from the registry.
#
# Scans $(REGISTRY)/dnp3-interop-<adapter>:$(VERSION) from the registry (no
# local image needed) for linux/amd64 and linux/arm64. Writes, per adapter and
# architecture:
#   sbom-<adapter>-<arch>.cdx.json    CycloneDX SBOM
#   vulns-<adapter>-<arch>.json       Trivy scan result
# and vulnerabilities.md, the summary. Findings do not fail the script: the
# images are test fixtures, and the findings are recorded, not gated on
# (SECURITY.md). A scan that cannot run fails it.
#
# Environment: REGISTRY, VERSION (the image tag; required), TRIVY_IMAGE.

set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/lib.sh
source "${SCRIPT_DIR}/lib.sh"

OUT="${1:?usage: qualify.sh <output directory> <adapter> [<adapter> ...]}"
shift
[[ $# -ge 1 ]] || { echo "usage: qualify.sh <output directory> <adapter> [<adapter> ...]" >&2; exit 2; }
for adapter in "$@"; do
    [[ "${adapter}" != stepfunc ]] || { echo "qualify.sh: the stepfunc image is not published; scan a local build of it yourself" >&2; exit 2; }
done
# Trivy, pinned by version and digest. Its vulnerability database is not
# pinned: it is the point of a scan to use the current one, and its version
# is recorded in the summary.
TRIVY_IMAGE="${TRIVY_IMAGE:-ghcr.io/aquasecurity/trivy:0.76.0@sha256:2416d1f74045b57274df1a340eee6b510cacd73c66a6ccb8da092b117eb7d222}"
ARCHS=(amd64 arm64)

mkdir -p "${OUT}"
OUT="$(cd "${OUT}" && pwd)"
CACHE="dnp3-interop-trivy-cache"
docker volume create "${CACHE}" >/dev/null

trivy() {
    docker run --rm -v "${CACHE}:/root/.cache" -v "${OUT}:/out" "${TRIVY_IMAGE}" "$@"
}

status=0
for adapter in "$@"; do
    ref="$(image_of "${adapter}")"
    for arch in "${ARCHS[@]}"; do
        log "${ref} linux/${arch}"
        if ! trivy image --quiet --image-src remote --platform "linux/${arch}" --scanners vuln \
                --format json --output "/out/vulns-${adapter}-${arch}.json" "${ref}"; then
            log "FAIL: scan of ${ref} linux/${arch}"; status=1; continue
        fi
        if ! trivy image --quiet --image-src remote --platform "linux/${arch}" \
                --format cyclonedx --output "/out/sbom-${adapter}-${arch}.cdx.json" "${ref}"; then
            log "FAIL: SBOM of ${ref} linux/${arch}"; status=1
        fi
    done
done

db=$(trivy version --format json 2>/dev/null | jq -r '.VulnerabilityDB | "version \(.Version), updated \(.UpdatedAt)"' 2>/dev/null)
tool=$(trivy version --format json 2>/dev/null | jq -r '.Version' 2>/dev/null)
{
    echo "# Vulnerability scan"
    echo ""
    echo "Images \`${REGISTRY}/dnp3-interop-<adapter>:${VERSION}\`, scanned with Trivy ${tool:-?}"
    echo "(vulnerability database ${db:-?}) on $(date -u +%Y-%m-%d)."
    echo "The images are test fixtures; findings are recorded here, not gated on (SECURITY.md)."
    echo "Each image also has a CycloneDX SBOM per architecture, \`sbom-<adapter>-<arch>.cdx.json\`."
    echo ""
    echo "| Image | Arch | Critical | High | Medium | Low | Unknown | Fixable |"
    echo "|-------|------|---------:|-----:|-------:|----:|--------:|--------:|"
    for adapter in "$@"; do
        for arch in "${ARCHS[@]}"; do
            f="${OUT}/vulns-${adapter}-${arch}.json"
            [[ -s "${f}" ]] || { echo "| \`${adapter}\` | ${arch} | scan failed | | | | | |"; continue; }
            jq -r --arg a "${adapter}" --arg r "${arch}" '
                [.Results[]? | .Vulnerabilities[]?] as $v
                | def n($s): [$v[] | select(.Severity == $s)] | length;
                "| `\($a)` | \($r) | \(n("CRITICAL")) | \(n("HIGH")) | \(n("MEDIUM")) | \(n("LOW")) | \(n("UNKNOWN")) | \([$v[] | select(.FixedVersion != null and .FixedVersion != "")] | length) |"' "${f}"
        done
    done
    echo ""
    echo "## Findings with a fix available"
    echo ""
    echo "What a rebuild on newer base images or packages would remove."
    echo ""
    echo "| Image | Arch | Package | Installed | Fixed in | Vulnerability | Severity | Kind |"
    echo "|-------|------|---------|-----------|----------|---------------|----------|------|"
    for adapter in "$@"; do
        for arch in "${ARCHS[@]}"; do
            f="${OUT}/vulns-${adapter}-${arch}.json"
            [[ -s "${f}" ]] || continue
            jq -r --arg a "${adapter}" --arg r "${arch}" '
                .Results[]? as $t | $t.Vulnerabilities[]? | select(.FixedVersion != null and .FixedVersion != "")
                | "| `\($a)` | \($r) | \(.PkgName) | \(.InstalledVersion) | \(.FixedVersion) | \(.VulnerabilityID) | \(.Severity) | \($t.Type) |"' "${f}"
        done
    done
    echo ""
    echo "## Critical and high findings without a fix, by package"
    echo ""
    echo "Upstream has no fixed version yet; they disappear when it has and the images are rebuilt."
    echo ""
    echo "| Image | Arch | Package | Kind | Critical | High | Vulnerabilities |"
    echo "|-------|------|---------|------|---------:|-----:|-----------------|"
    for adapter in "$@"; do
        for arch in "${ARCHS[@]}"; do
            f="${OUT}/vulns-${adapter}-${arch}.json"
            [[ -s "${f}" ]] || continue
            jq -r --arg a "${adapter}" --arg r "${arch}" '
                [.Results[]? as $t | $t.Vulnerabilities[]?
                 | select((.Severity == "CRITICAL" or .Severity == "HIGH") and (.FixedVersion == null or .FixedVersion == ""))
                 | {pkg: .PkgName, kind: $t.Type, sev: .Severity, id: .VulnerabilityID}]
                | group_by(.pkg) | .[]
                | "| `\($a)` | \($r) | \(.[0].pkg) | \(.[0].kind) | \([.[] | select(.sev == "CRITICAL")] | length) | \([.[] | select(.sev == "HIGH")] | length) | \([.[].id] | unique | join(", ")) |"' "${f}"
        done
    done
    echo ""
    echo "## What the scan can see"
    echo ""
    echo "Trivy reads the Debian packages of every image, the Python packages of the dnp3py"
    echo "image and the Go module information of the dscsystems binary. It cannot see what"
    echo "is compiled into the opendnp3 binary (opendnp3, asio, exe4cpp, ser4cpp, statically linked;"
    echo "listed in \`versions.yaml\`): their versions are in the release's \`manifest.json\` and"
    echo "\`versions.yaml\`. The stepfunc image is not published (the licence of the Step Function I/O"
    echo "dnp3 crate forbids distributing it), so it is not scanned here: each user builds it locally."
} > "${OUT}/vulnerabilities.md"

log "summary: ${OUT}/vulnerabilities.md"
exit "${status}"
