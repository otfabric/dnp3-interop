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

# arg <Dockerfile> <name>: the default of the first "ARG <name>=". sed stops
# there itself: "| head -1" would end sed with SIGPIPE when the ARG comes
# twice, and pipefail would end this script, now and then.
arg() { sed -nE "/^ARG $2=/{s/^ARG $2=(.*)$/\1/p;q;}" "$1"; }

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

# Base images: every digest a Dockerfile of an adapter or of own/ pins is in
# base_images, and the used_by of that base image names the image (otfabric
# for own/); every name in a used_by pins that digest.
# base_users <digest>: the used_by names of the base image with that digest.
base_users() {
    awk -v d="$1" '
        /^[^ #]/ { inside = ($0 ~ /^base_images:/); next }
        !inside { next }
        /^  [A-Za-z0-9_]+:[[:space:]]*$/ { ref = ""; next }
        /^    image: / { ref = $0; next }
        /^    used_by: / && ref != "" && index(ref, d) {
            sub(/^    used_by: *\[/, ""); sub(/\].*$/, ""); gsub(/, */, "\n"); print; exit
        }' versions.yaml
}
# dockerfile_of <name>: the Dockerfile of an image that used_by names.
dockerfile_of() { if [[ "$1" == otfabric ]]; then echo own/go-dnp3/Dockerfile; else echo "adapters/$1/Dockerfile"; fi; }
digests() { sed -nE 's/^ARG [A-Z0-9_]+_DIGEST=(sha256:[0-9a-f]+)$/\1/p' "$1" | sort -u; }
for dockerfile in adapters/*/Dockerfile own/*/Dockerfile; do
    name="$(basename "$(dirname "${dockerfile}")")"
    [[ "${dockerfile}" == own/* ]] && name=otfabric
    while read -r digest; do
        need "${dockerfile} base image" "${digest}"
        if base_users "${digest}" | grep -xF -- "${name}" >/dev/null; then
            echo "ok   ${dockerfile}: used_by of ${digest} names ${name}"
        else
            echo "FAIL ${dockerfile}: the used_by of the base image ${digest} does not name ${name}"
            status=1
        fi
    done < <(digests "${dockerfile}")
done
while read -r digest; do
    while read -r name; do
        [[ -n "${name}" ]] || continue
        if [[ -f "$(dockerfile_of "${name}")" ]] && digests "$(dockerfile_of "${name}")" | grep -xF -- "${digest}" >/dev/null; then
            echo "ok   base image ${digest} used by ${name}"
        else
            echo "FAIL base image ${digest}: used_by names ${name}, whose Dockerfile does not pin it"
            status=1
        fi
    done < <(base_users "${digest}")
done < <(sed -nE '/^base_images:/,/^[^ #]/s/^    image: "[^"]*@(sha256:[0-9a-f]+)".*$/\1/p' versions.yaml)

# Implementations: each says whether its image is published, and exactly the
# published ones are PUBLISHED_ADAPTERS of the Makefile; each that is built
# records the hash its source is verified with: wheel_sha256 equal to the hash
# of its requirements.txt (a package), module_sum equal to the h1: of its
# go.sum (a Go module), or else a source_sha256.
# impl_names: the implementations, one per line.
impl_names() {
    awk '/^[^ #]/ { inside = ($0 ~ /^implementations:/); next }
         inside && /^  [A-Za-z0-9_-]+:[[:space:]]*$/ { sub(/^  /, ""); sub(/:.*$/, ""); print }' versions.yaml
}
# impl <name> <field>: the value of a field of an implementation, without quotes or comment.
impl() {
    awk -v n="$1" -v f="$2:" '
        /^[^ #]/ { inside = ($0 ~ /^implementations:/); next }
        !inside { next }
        /^  [A-Za-z0-9_-]+:[[:space:]]*$/ { cur = $1; sub(/:$/, "", cur); next }
        cur == n && /^    [a-z0-9_]+:/ && $1 == f {
            v = $0; sub(/^ *[a-z0-9_]+: */, "", v); sub(/[[:space:]]+#.*$/, "", v); gsub(/"/, "", v); print v; exit
        }' versions.yaml
}
# recorded <description> <value in versions.yaml> <value of the build file>:
# same, but an empty value in versions.yaml fails.
recorded() {
    if [[ -n "$2" ]]; then same "$1" "$2" "$3"; else echo "FAIL $1: nothing recorded in versions.yaml"; status=1; fi
}
# req_hash <requirements.txt> <package>: the sha256 pip checks for the package.
req_hash() {
    awk -v p="$2==" 'index($0, p) == 1 { found = 1 }
        found && match($0, /--hash=sha256:[0-9a-f]+/) { print substr($0, RSTART + 14, RLENGTH - 14); exit }' "$1"
}
published_adapters=" $(sed -nE 's/^override PUBLISHED_ADAPTERS := (.*)$/\1/p' Makefile) "
while read -r name; do
    case "$(impl "${name}" published)" in
        true)
            if [[ "${published_adapters}" == *" ${name} "* ]]; then echo "ok   ${name} published: true"
            else echo "FAIL ${name}: published: true, but not in PUBLISHED_ADAPTERS"; status=1; fi ;;
        false)
            if [[ "${published_adapters}" != *" ${name} "* ]]; then echo "ok   ${name} published: false"
            else echo "FAIL ${name}: published: false, but in PUBLISHED_ADAPTERS"; status=1; fi ;;
        *)
            echo "FAIL ${name}: no published: true or false"; status=1 ;;
    esac
    [[ "$(impl "${name}" status)" == implemented ]] || continue
    package="$(impl "${name}" package)"
    module="$(impl "${name}" module)"
    if [[ -n "${package}" ]]; then
        recorded "${name} wheel_sha256 and requirements.txt" "$(impl "${name}" wheel_sha256)" \
            "$(req_hash "adapters/${name}/requirements.txt" "${package}")"
    elif [[ -n "${module}" ]]; then
        recorded "${name} module_sum and go.sum" "$(impl "${name}" module_sum)" \
            "$(awk -v m="${module}" -v v="$(impl "${name}" version)" '$1 == m && $2 == v { print $3; exit }' "adapters/${name}/go.sum")"
    elif [[ "$(impl "${name}" source_sha256)" =~ ^[0-9a-f]{64}$ ]]; then
        echo "ok   ${name} source_sha256: $(impl "${name}" source_sha256)"
    else
        echo "FAIL ${name}: no wheel_sha256, module_sum or source_sha256"
        status=1
    fi
done < <(impl_names)
for name in ${published_adapters}; do
    [[ -n "$(impl "${name}" published)" ]] || { echo "FAIL ${name}: in PUBLISHED_ADAPTERS, not an implementation in versions.yaml"; status=1; }
done

# Tooling: the images and versions of the Makefile and scripts/qualify.sh
mk() { sed -nE "/^$1 +[?:]?= /{s|^$1 +[?:]?= (.*)$|\1|p;q;}" Makefile; }
for tool in SHELLCHECK HADOLINT PYTHON BATS; do
    image="$(mk "${tool}_IMAGE")"
    need "Makefile ${tool}_IMAGE" "\"${image#"\$(TOOLS_REGISTRY)/"}\""
done
need "Makefile GO_IMAGE digest" "$(mk GO_IMAGE | sed -E 's|^.*@||')"
need "Makefile govulncheck" "govulncheck@$(mk GOVULNCHECK_VERSION)\""
need "qualify.sh Trivy" "\"$(sed -nE 's|^TRIVY_IMAGE="\$\{TRIVY_IMAGE:-(.*)\}"$|\1|p' scripts/qualify.sh)\""

# Debian packages: every Debian stage installs from the snapshot in
# versions.yaml, and every package at a version listed there.
# apt_packages <Dockerfile>: the words after "apt-get install" up to the next
# "&&" of every RUN instruction, one per line, options left out.
apt_packages() {
    awk '/\\$/ { sub(/\\$/, ""); line = line $0; next } { print line $0; line = "" }' "$1" \
        | grep 'apt-get install' | sed -E 's/.*apt-get install//; s/&&.*//' | tr ' ' '\n' | grep -E '^[^-]' || true
}
for dockerfile in adapters/*/Dockerfile own/*/Dockerfile; do
    grep -q 'apt-get install' "${dockerfile}" || continue
    need "${dockerfile} Debian snapshot" "debian_snapshot: \"$(arg "${dockerfile}" DEBIAN_SNAPSHOT)\""
    while read -r word; do
        if [[ "${word}" == *=* ]]; then
            need "${dockerfile} ${word%%=*}" "  ${word%%=*}: \"${word#*=}\""
        else
            echo "FAIL ${dockerfile}: Debian package ${word} has no version"
            status=1
        fi
    done < <(apt_packages "${dockerfile}")
done

exit "${status}"
