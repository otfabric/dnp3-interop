#!/usr/bin/env bash
# Fails when versions.yaml disagrees with what the builds actually pin.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"
status=0

need() {  # need <description> <value that must appear in versions.yaml>
    if [[ -n "$2" ]] && grep -qF -- "$2" versions.yaml; then
        echo "ok   $1: $2"
    else
        echo "FAIL $1: '$2' is not in versions.yaml"
        status=1
    fi
}

same() {  # same <description> <a> <b>
    if [[ "$2" == "$3" ]]; then
        echo "ok   $1: $2"
    else
        echo "FAIL $1: '$2' and '$3' disagree"
        status=1
    fi
}

arg() { sed -nE "s/^ARG $2=(.*)$/\1/p" "$1" | head -1; }

# opendnp3
need "opendnp3 version"  "version: \"$(arg adapters/opendnp3/Dockerfile UPSTREAM_VERSION)\""
need "opendnp3 revision" "$(arg adapters/opendnp3/Dockerfile UPSTREAM_SHA)"
need "debian digest"     "$(arg adapters/opendnp3/Dockerfile DEBIAN_DIGEST)"

# dscsystems: the Dockerfile and go.mod name the same DSC go-dnp3 version
dockerfile_godnp3="$(arg adapters/dscsystems/Dockerfile UPSTREAM_VERSION)"
gomod_godnp3="$(sed -nE 's|^require github.com/dscsystems/go-dnp3 (v[^ ]+).*$|\1|p' adapters/dscsystems/go.mod)"
need "DSC go-dnp3 version"   "version: \"${dockerfile_godnp3}\""
same "DSC go-dnp3 in Dockerfile and go.mod" "${dockerfile_godnp3}" "${gomod_godnp3}"
need "DSC go-dnp3 revision"  "$(arg adapters/dscsystems/Dockerfile UPSTREAM_SHA)"
need "golang digest"     "$(arg adapters/dscsystems/Dockerfile GOLANG_DIGEST)"
need "dscsystems debian" "$(arg adapters/dscsystems/Dockerfile DEBIAN_DIGEST)"

# dnp3py: the Dockerfile and requirements.txt name the same version
dockerfile_dnp3py="$(arg adapters/dnp3py/Dockerfile UPSTREAM_VERSION)"
requirements_dnp3py="$(sed -nE 's/^dnp3py==([^ ]+).*$/\1/p' adapters/dnp3py/requirements.txt)"
need "dnp3py version"    "version: \"${dockerfile_dnp3py}\""
same "dnp3py in Dockerfile and requirements.txt" "${dockerfile_dnp3py}" "${requirements_dnp3py}"
need "dnp3py revision"   "$(arg adapters/dnp3py/Dockerfile UPSTREAM_SHA)"
need "python digest"     "$(arg adapters/dnp3py/Dockerfile PYTHON_DIGEST)"

# cbsigma: the Dockerfile and go.mod name the same cB-Sigma dnp3 version
dockerfile_cbsigma="$(arg adapters/cbsigma/Dockerfile UPSTREAM_VERSION)"
gomod_cbsigma="$(sed -nE 's|^[[:space:]]*(require[[:space:]]+)?github.com/cB-Sigma/dnp3 (v[^ ]+).*$|\2|p' adapters/cbsigma/go.mod)"
need "cB-Sigma dnp3 version"  "version: \"${dockerfile_cbsigma}\""
same "cB-Sigma dnp3 in Dockerfile and go.mod" "${dockerfile_cbsigma}" "${gomod_cbsigma}"
need "cB-Sigma dnp3 revision" "$(arg adapters/cbsigma/Dockerfile UPSTREAM_SHA)"
need "cbsigma golang digest"  "$(arg adapters/cbsigma/Dockerfile GOLANG_DIGEST)"
need "cbsigma debian digest"  "$(arg adapters/cbsigma/Dockerfile DEBIAN_DIGEST)"

# nfmdnp3: the Dockerfile and requirements.txt name the same nfm-dnp3 version
dockerfile_nfmdnp3="$(arg adapters/nfmdnp3/Dockerfile UPSTREAM_VERSION)"
requirements_nfmdnp3="$(sed -nE 's/^nfm-dnp3==([^ ]+).*$/\1/p' adapters/nfmdnp3/requirements.txt)"
need "nfm-dnp3 version"   "version: \"${dockerfile_nfmdnp3}\""
same "nfm-dnp3 in Dockerfile and requirements.txt" "${dockerfile_nfmdnp3}" "${requirements_nfmdnp3}"
need "nfm-dnp3 revision"  "$(arg adapters/nfmdnp3/Dockerfile UPSTREAM_SHA)"
need "nfmdnp3 python digest" "$(arg adapters/nfmdnp3/Dockerfile PYTHON_DIGEST)"

# dnp3c: the Dockerfile and pom.xml name the same dnp3c version
dockerfile_dnp3c="$(arg conformance/dnp3c/Dockerfile DNP3C_VERSION)"
pom_dnp3c="$(sed -nE 's|^ *<dnp3c.version>([^<]+)</dnp3c.version>.*$|\1|p' conformance/dnp3c/pom.xml)"
need "dnp3c version"     "version: \"${dockerfile_dnp3c}\""
same "dnp3c in Dockerfile and pom.xml" "${dockerfile_dnp3c}" "${pom_dnp3c}"
need "maven digest"      "$(arg conformance/dnp3c/Dockerfile MAVEN_DIGEST)"
need "jre digest"        "$(arg conformance/dnp3c/Dockerfile JRE_DIGEST)"

exit "${status}"
