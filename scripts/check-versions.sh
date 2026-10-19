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
need "opendnp3 version"  "version: \"$(arg adapters/opendnp3/Dockerfile OPENDNP3_VERSION)\""
need "opendnp3 revision" "$(arg adapters/opendnp3/Dockerfile OPENDNP3_SHA)"
need "debian digest"     "$(arg adapters/opendnp3/Dockerfile DEBIAN_DIGEST)"

# stepfunc: the Dockerfile, Cargo.toml and Cargo.lock name the same crate version
dockerfile_dnp3="$(arg adapters/stepfunc/Dockerfile DNP3_VERSION)"
cargo_dnp3="$(sed -nE 's/^dnp3 = \{ version = "=([^"]+)".*/\1/p' adapters/stepfunc/Cargo.toml)"
lock_dnp3="$(awk '/^name = "dnp3"$/ {getline; sub(/^version = "/, ""); sub(/"$/, ""); print}' adapters/stepfunc/Cargo.lock 2>/dev/null || true)"
need "dnp3 crate version" "version: \"${dockerfile_dnp3}\""
same "dnp3 in Dockerfile and Cargo.toml" "${dockerfile_dnp3}" "${cargo_dnp3}"
same "dnp3 in Dockerfile and Cargo.lock" "${dockerfile_dnp3}" "${lock_dnp3}"
need "dnp3 revision"     "$(arg adapters/stepfunc/Dockerfile DNP3_SHA)"
need "rust digest"       "$(arg adapters/stepfunc/Dockerfile RUST_DIGEST)"
need "stepfunc debian"   "$(arg adapters/stepfunc/Dockerfile DEBIAN_DIGEST)"

# dscsystems: the Dockerfile and go.mod name the same go-dnp3 version
dockerfile_godnp3="$(arg adapters/dscsystems/Dockerfile GODNP3_VERSION)"
gomod_godnp3="$(sed -nE 's|^require github.com/dscsystems/go-dnp3 (v[^ ]+).*$|\1|p' adapters/dscsystems/go.mod)"
need "go-dnp3 version"   "version: \"${dockerfile_godnp3}\""
same "go-dnp3 in Dockerfile and go.mod" "${dockerfile_godnp3}" "${gomod_godnp3}"
need "go-dnp3 revision"  "$(arg adapters/dscsystems/Dockerfile GODNP3_SHA)"
need "golang digest"     "$(arg adapters/dscsystems/Dockerfile GOLANG_DIGEST)"
need "dscsystems debian" "$(arg adapters/dscsystems/Dockerfile DEBIAN_DIGEST)"

# dnp3py: the Dockerfile and requirements.txt name the same version
dockerfile_dnp3py="$(arg adapters/dnp3py/Dockerfile DNP3PY_VERSION)"
requirements_dnp3py="$(sed -nE 's/^dnp3py==([^ ]+).*$/\1/p' adapters/dnp3py/requirements.txt)"
need "dnp3py version"    "version: \"${dockerfile_dnp3py}\""
same "dnp3py in Dockerfile and requirements.txt" "${dockerfile_dnp3py}" "${requirements_dnp3py}"
need "dnp3py revision"   "$(arg adapters/dnp3py/Dockerfile DNP3PY_SHA)"
need "python digest"     "$(arg adapters/dnp3py/Dockerfile PYTHON_DIGEST)"

exit "${status}"
