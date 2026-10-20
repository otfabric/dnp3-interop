#!/usr/bin/env bash
# Cross-stack self-test of the parts of container contract 1.1: the
# transports (TLS, UDP, serial), file transfer and secure authentication.
#
# Usage: features.sh <adapter> [<adapter> ...]
#
# For every client/server pairing that declares a feature in
# print-capabilities:
#   tls     an integrity poll, a control and its events over TLS; a master
#           that does not trust the outstation's certificate cannot connect;
#           an outstation does not serve a master whose certificate it does
#           not trust
#   udp     an integrity poll, a control and its events over UDP
#   serial  the same over a pseudo-terminal line bridged by socat
#   files   reading, listing, describing, writing and deleting files of the
#           files fixture; the refusal of a file that does not exist; the
#           server's file events; the same listing and descriptors for
#           every master
#   sa      secure authentication: reads and controls with the shared update
#           key, the server's auth events, refusal of a wrong key, of a wrong
#           user and of a master without authentication
#   reverse (contract 1.2) dual end point: the outstation dials a master
#           that listens, over TCP and, where declared, TLS
#   points  (contract 1.2) the extended fixture: frozen analog inputs (g31),
#           command events of the outputs (g13, g43) with the status of the
#           control, and a time and interval object (g50v4)
#
# One suite per server adapter, all at the same time, as in interop.sh.
#
# This tests the reference adapters against each other. It says nothing about
# any library that consumes the images.
#
# Environment: REGISTRY, VERSION (image tag, default dev), VERBOSE=1,
#              REPORT_DIR (keeps the outcome of every check when set).

set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
# shellcheck source=scripts/lib.sh
source "${SCRIPT_DIR}/lib.sh"

[[ $# -ge 1 ]] || { echo "usage: features.sh <adapter> [<adapter> ...]" >&2; exit 2; }
ADAPTERS=("$@")
FILES_FIXTURE="${ROOT}/fixtures/files/fixture.json"
require_tools
command -v openssl >/dev/null || { log "missing required tool: openssl"; exit 2; }
WORK=$(mktemp -d)
trap 'rm -rf "${WORK}"; cleanup' EXIT

# UDP endpoints are fixed addresses, so the network needs a subnet of its own.
for _ in 1 2 3 4 5 6 7 8; do
    SUBNET_PREFIX="10.$(( (RANDOM % 200) + 30 )).$(( RANDOM % 250 ))"
    docker network create --subnet "${SUBNET_PREFIX}.0/24" "${NETWORK}" >/dev/null 2>&1 && break
    SUBNET_PREFIX=""
done
[[ -n "${SUBNET_PREFIX}" ]] || { log "cannot create a network with a free subnet"; exit 1; }

# Test certificates, readable by the images' user.
PKI="${WORK}/pki"
"${SCRIPT_DIR}/test-pki.sh" "${PKI}" dnp3-interop-server || { log "cannot make the test PKI"; exit 1; }
chmod 755 "${WORK}" "${PKI}"
SERVER_TLS=(--tls-cert /pki/server.pem --tls-key /pki/server.key --tls-ca /pki/ca.pem)
CLIENT_TLS=(--tls --tls-ca /pki/ca.pem --tls-cert /pki/client.pem --tls-key /pki/client.key --tls-server-name dnp3-interop-server)
SA_KEY=000102030405060708090a0b0c0d0e0f
SA=(--sa-update-key "${SA_KEY}" --sa-user 3)

for adapter in "${ADAPTERS[@]}"; do
    docker run --rm "$(image_of "${adapter}")" print-capabilities > "${WORK}/caps-${adapter}.json" 2>/dev/null
done
has() { jq -e --arg f "$2" '.features[$f] == true' "${WORK}/caps-$1.json" >/dev/null 2>&1; }
has_role() { jq -e --arg r "$2" '.roles[$r] == true' "${WORK}/caps-$1.json" >/dev/null 2>&1; }
# serves_extended <adapter>: its outstation serves the extended fixture.
serves_extended() {
    has_role "$1" server && has "$1" commandEventsServer \
        && jq -e '.pointTypes | index("frozenAnalogInputs") and index("timeAndIntervals")' "${WORK}/caps-$1.json" >/dev/null 2>&1
}
# objects of a document with the status of command events and the units of
# g50v4, as "g13v2:0=true/SUCCESS".
detail() {
    jq -r '[.objects[]? | "g\(.group)v\(.variation):\(.index)=\(.value|tostring)\(if .status then "/\(.status)" else "" end)\(if .units then "/\(.time)/\(.units)" else "" end)"] | sort | join(" ")' <<<"$1" 2>/dev/null
}

SKIPPED=0
skip() { SKIPPED=$((SKIPPED + 1)); record skip "$*"; [[ -n "${VERBOSE:-}" ]] && log "skip: $*"; return 0; }

# check <label> <want> <got>
check() {
    if [[ "$3" == "$2" ]]; then ok "$1"; return 0; fi
    fail "$1"; log "    want ${2:-<nothing>}"; log "    got  ${3:-<nothing>}"
    if [[ -n "${name:-}" ]] && docker inspect "${name}" >/dev/null 2>&1; then
        log "    log of ${name}:"
        docker logs --tail 40 "${name}" 2>&1 | sed 's/^/        /' >&2
    fi
    return 1
}

objects() {
    jq -r '[.objects[]? | "g\(.group)v\(.variation)\(if .unsolicited then "u" else "" end):\(.index)=\(.value|tostring)"] | sort | join(" ")' <<<"$1" 2>/dev/null
}
outcome() { jq -r '"ok=\(.ok) error=\(.error.code // "-")"' <<<"$1" 2>/dev/null; }

# The integrity poll of the baseline fixture, as tests/cases.tsv states it.
WANT_POLL=$(awk -F'\t' '$5 == "integrity-poll" { print $3; exit }' "${ROOT}/tests/cases.tsv")

# start <adapter> <container name> [docker args] -- [server args]: a server
# container, not waited for.
start() {
    local adapter="$1" cname="$2"; shift 2
    local dargs=()
    while [[ $# -gt 0 && "$1" != "--" ]]; do dargs+=("$1"); shift; done
    [[ "${1:-}" == "--" ]] && shift
    docker rm -f "${cname}" >/dev/null 2>&1
    docker run -d --name "${cname}" --network "${NETWORK}" --label "dnp3-interop.run=${RUN_ID}" \
        -e "DNP3_INTEROP_DEBUG=${SERVER_DEBUG:-1}" -v "${PKI}:/pki:ro" "${dargs[@]}" \
        "$(image_of "${adapter}")" server "$@" >/dev/null
}

# client <adapter> [docker args] -- <client args>: the result document.
client() {
    local adapter="$1"; shift
    local dargs=()
    while [[ $# -gt 0 && "$1" != "--" ]]; do dargs+=("$1"); shift; done
    [[ "${1:-}" == "--" ]] && shift
    docker run --rm --network "${NETWORK}" --label "dnp3-interop.run=${RUN_ID}" -v "${PKI}:/pki:ro" \
        "${dargs[@]}" "$(image_of "${adapter}")" client "$@" 2>/dev/null | keep
}

# keep: passes a result document through, and keeps it in REPORT_DIR, when
# set, for scripts/validate-documents.py.
keep() {
    if [[ -n "${REPORT_DIR:-}" ]]; then tee "${REPORT_DIR}/feature-${BASHPID}-${RANDOM}${RANDOM}.json"; else cat; fi
}

# The serial line: the outstation holds one end of a pseudo-terminal pair,
# masters attach to the other end over TCP (docs/CONTAINER_CONTRACT.md).
SERIAL_SERVER='socat pty,link=/tmp/ttyDNP3,raw,echo=0 pty,link=/tmp/ttyLINE,raw,echo=0 &
while [ ! -e /tmp/ttyDNP3 ] || [ ! -e /tmp/ttyLINE ]; do sleep 0.05; done
socat -t 0.05 tcp-listen:20001,reuseaddr,fork file:/tmp/ttyLINE,raw,echo=0 &
exec dnp3-interop server --transport serial --serial-device /tmp/ttyDNP3'
serial_client() {  # serial_client <adapter> <server container> <client args>
    local adapter="$1" server="$2"; shift 2
    docker run --rm --network "${NETWORK}" --label "dnp3-interop.run=${RUN_ID}" --entrypoint /bin/sh \
        "$(image_of "${adapter}")" -c "socat pty,link=/tmp/ttyDNP3,raw,echo=0 tcp:${server}:20001 &
while [ ! -e /tmp/ttyDNP3 ]; do sleep 0.05; done
exec dnp3-interop client \"\$@\" --transport serial --serial-device /tmp/ttyDNP3" sh "$@" 2>/dev/null | keep
}

# basics <pair> <tag> <run>: the integrity poll, a control and its events,
# where <run> runs one client operation (its arguments follow).
basics() {
    local pair="$1" tag="$2" run="$3" doc
    doc=$(${run} integrity-poll)
    check "${pair}: [${tag}] integrity-poll" "${WANT_POLL}" "$(jq -r "${SUMMARY}" <<<"${doc}" 2>/dev/null)"
    doc=$(${run} operate --type g12v1 --index 0 --code LATCH_ON)
    check "${pair}: [${tag}] operate" "SUCCESS" "$(jq -r '.commandStatus' <<<"${doc}" 2>/dev/null)"
    check "${pair}: [${tag}] the control produces class 1 events" "g11v2:0=true g2v2:1=true" \
        "$(objects "$(${run} event-poll --classes 1)")"
    check "${pair}: [${tag}] confirmed events are gone" "" "$(objects "$(${run} event-poll)")"
}

# ready_transport <container>: the transport of the ready event.
ready_transport() { docker logs "$1" 2>/dev/null | head -1 | jq -r '.transport' 2>/dev/null; }

# Fixture values the file checks expect.
README_HEX=$(jq -r '.files[] | select(.name == "/readme.txt") | .content' "${FILES_FIXTURE}")
BLOCKS_HEX=$(jq -r '.files[] | select(.name == "/data/blocks.bin") | .content' "${FILES_FIXTURE}")
NORMALIZE_FILES='del(.adapter, .elapsedMs, .error.message?) | (.file?.time?) |= empty | (.files[]?.time?) |= empty
  | (if .files then .files |= sort_by(.name) else . end)'

# reverse_op <server> <client> <tag> <client args...>: one operation with the
# roles of the connection reversed; prints the master's result document. The
# master listens under the name the outstation dials; the outstation is
# started for the operation and stopped after it.
reverse_op() {
    local server="$1" client="$2" tag="$3"; shift 3
    local listener="fr-${server}-${client}-${RUN_ID}" outst="fo-${server}-${client}-${RUN_ID}"
    local ctls=() stls=()
    if [[ "${tag}" == tls ]]; then
        # The master is the TLS server now: it shows the server certificate.
        ctls=(--tls --tls-ca /pki/ca.pem --tls-cert /pki/server.pem --tls-key /pki/server.key)
        stls=(--tls-cert /pki/client.pem --tls-key /pki/client.key --tls-ca /pki/ca.pem --tls-server-name dnp3-interop-server)
    fi
    docker rm -f "${listener}" "${outst}" >/dev/null 2>&1
    docker run -d --name "${listener}" --network "${NETWORK}" --label "dnp3-interop.run=${RUN_ID}" -v "${PKI}:/pki:ro" \
        "$(image_of "${client}")" client "$@" --listen --connect-timeout-ms 30000 "${ctls[@]}" >/dev/null
    start "${server}" "${outst}" -- --connect "${listener}:20000" "${stls[@]}"
    docker wait "${listener}" >/dev/null 2>&1
    docker logs "${listener}" 2>/dev/null | keep
    # The caller runs this in a subshell: the outstation's events go to a file.
    docker logs "${outst}" 2>/dev/null > "${WORK}/events-${outst}"
    docker rm -f "${listener}" "${outst}" >/dev/null 2>&1
}

# reverse <server> <client> <pair> <tcp|tls>
reverse() {
    local server="$1" client="$2" pair="$3" tag="$4" doc
    doc=$(reverse_op "${server}" "${client}" "${tag}" integrity-poll)
    check "${pair}: [reverse ${tag}] integrity-poll" "${WANT_POLL}" "$(jq -r "${SUMMARY}" <<<"${doc}" 2>/dev/null)"
    check "${pair}: [reverse ${tag}] the outstation dialled" "true ${tag} true" \
        "$(jq -rs '"\(.[0].dial) \(.[0].transport) \(any(.[]; .event == "connection-opened"))"' \
            "${WORK}/events-fo-${server}-${client}-${RUN_ID}" 2>/dev/null)"
    doc=$(reverse_op "${server}" "${client}" "${tag}" operate --type g12v1 --index 0 --code LATCH_ON)
    check "${pair}: [reverse ${tag}] operate" "SUCCESS" "$(jq -r '.commandStatus' <<<"${doc}" 2>/dev/null)"
}

suite() {
    local server="$1" k="$2" client pair name doc host run ip_server ip_client
    PASS=0 FAIL=0 SKIPPED=0
    if [[ -n "${REPORT_DIR:-}" ]]; then RESULTS_FILE="${REPORT_DIR}/results-features-${server}.tsv"; : > "${RESULTS_FILE}"; fi
    ip_server="${SUBNET_PREFIX}.$((10 + k))"
    ip_client="${SUBNET_PREFIX}.$((100 + k))"
    mkdir -p "${WORK}/agree-${server}"

    for client in "${ADAPTERS[@]}"; do
        has_role "${client}" client || continue
        pair="${client}->${server}"

        # ---- TLS ----
        if has "${server}" tls && has "${client}" tls; then
            name="ft-${server}-${RUN_ID}"
            start "${server}" "${name}" -- "${SERVER_TLS[@]}"
            if wait_ready "${name}"; then
                check "${pair}: [tls] ready event" "tls" "$(ready_transport "${name}")"
                host="${name}"
                run="tls_run ${client} ${host}"
                tls_run() { local a="$1" h="$2"; shift 2; client "${a}" -- "$@" --host "${h}" "${CLIENT_TLS[@]}"; }
                basics "${pair}" tls "${run}"
                doc=$(client "${client}" -- integrity-poll --host "${host}" --connect-timeout-ms 2000 --tls \
                    --tls-ca /pki/other-ca.pem --tls-cert /pki/client.pem --tls-key /pki/client.key --tls-server-name dnp3-interop-server)
                check "${pair}: [tls] a master that does not trust the outstation cannot connect" \
                    "ok=false error=connect-failed" "$(outcome "${doc}")"
                doc=$(client "${client}" -- integrity-poll --host "${host}" --connect-timeout-ms 2000 --timeout-ms 2000 --tls \
                    --tls-ca /pki/ca.pem --tls-cert /pki/other-server.pem --tls-key /pki/other-server.key --tls-server-name dnp3-interop-server)
                check "${pair}: [tls] an outstation does not serve a master it does not trust" \
                    "false" "$(jq -r '.ok' <<<"${doc}" 2>/dev/null)"
            else
                fail "${pair}: [tls] server did not start"
            fi
            stop_server "${name}"
        else
            skip "${pair}: tls (needs server:tls,client:tls)"
        fi

        # ---- UDP ----
        if has "${server}" udp && has "${client}" udp; then
            name="fu-${server}-${RUN_ID}"
            start "${server}" "${name}" --ip "${ip_server}" -- --transport udp --udp-peer "${ip_client}:20000"
            if wait_ready "${name}"; then
                check "${pair}: [udp] ready event" "udp" "$(ready_transport "${name}")"
                udp_run() { local a="$1" ipc="$2" ips="$3"; shift 3; client "${a}" --ip "${ipc}" -- "$@" --host "${ips}" --transport udp; }
                basics "${pair}" udp "udp_run ${client} ${ip_client} ${ip_server}"
            else
                fail "${pair}: [udp] server did not start"
            fi
            stop_server "${name}"
        else
            skip "${pair}: udp (needs server:udp,client:udp)"
        fi

        # ---- serial ----
        if has "${server}" serial && has "${client}" serial; then
            name="fs-${server}-${RUN_ID}"
            docker rm -f "${name}" >/dev/null 2>&1
            docker run -d --name "${name}" --network "${NETWORK}" --label "dnp3-interop.run=${RUN_ID}" \
                -e "DNP3_INTEROP_DEBUG=${SERVER_DEBUG:-1}" --entrypoint /bin/sh \
                "$(image_of "${server}")" -c "${SERIAL_SERVER}" >/dev/null
            if wait_ready "${name}"; then
                check "${pair}: [serial] ready event" "serial" "$(ready_transport "${name}")"
                basics "${pair}" serial "serial_client ${client} ${name}"
            else
                fail "${pair}: [serial] server did not start"
            fi
            stop_server "${name}"
        else
            skip "${pair}: serial (needs server:serial,client:serial)"
        fi

        # ---- file transfer ----
        if has "${server}" fileServer && has "${client}" fileClient; then
            name="ff-${server}-${RUN_ID}"
            start "${server}" "${name}" -- --fixture /fixtures/files/fixture.json
            if wait_ready "${name}"; then
                doc=$(client "${client}" -- file-get --name /readme.txt --host "${name}")
                check "${pair}: [files] file-get of a short file" "${README_HEX}" "$(jq -r '.file.content' <<<"${doc}" 2>/dev/null)"
                doc=$(client "${client}" -- file-get --name /data/blocks.bin --host "${name}")
                check "${pair}: [files] file-get of a file of several blocks" "${BLOCKS_HEX}" "$(jq -r '.file.content' <<<"${doc}" 2>/dev/null)"
                check "${pair}: [files] file-get reports the size" "3000" "$(jq -r '.file.size' <<<"${doc}" 2>/dev/null)"
                doc=$(client "${client}" -- file-ls --name / --host "${name}")
                check "${pair}: [files] file-ls of the root" "data:directory logs:directory readme.txt:file" \
                    "$(jq -r '[.files[]? | "\(.name):\(.type)"] | sort | join(" ")' <<<"${doc}" 2>/dev/null)"
                jq -cS "${NORMALIZE_FILES}" <<<"${doc}" > "${WORK}/agree-${server}/ls-${client}" 2>/dev/null
                doc=$(client "${client}" -- file-info --name /readme.txt --host "${name}")
                check "${pair}: [files] file-info" "readme.txt file 43 rw-r--r--" \
                    "$(jq -r '"\(.file.name) \(.file.type) \(.file.size) \(.file.permissions)"' <<<"${doc}" 2>/dev/null)"
                jq -cS "${NORMALIZE_FILES}" <<<"${doc}" > "${WORK}/agree-${server}/info-${client}" 2>/dev/null
                doc=$(client "${client}" -- file-put --name /logs/new.txt --content 0001fe --host "${name}")
                check "${pair}: [files] file-put" "ok=true error=- SUCCESS" "$(outcome "${doc}") $(jq -r '.fileStatus' <<<"${doc}" 2>/dev/null)"
                check "${pair}: [files] the written file reads back" "0001fe" \
                    "$(client "${client}" -- file-get --name /logs/new.txt --host "${name}" | jq -r '.file.content')"
                check "${pair}: [files] the written file is listed" "new.txt:3" \
                    "$(client "${client}" -- file-ls --name /logs --host "${name}" | jq -r '[.files[]? | "\(.name):\(.size)"] | join(" ")')"
                doc=$(client "${client}" -- file-get --name /no-such-file --host "${name}")
                check "${pair}: [files] a file that does not exist" "ok=false error=file-error FILE_NOT_FOUND" \
                    "$(outcome "${doc}") $(jq -r '.fileStatus' <<<"${doc}" 2>/dev/null)"
                if has "${client}" fileDeleteClient; then
                    doc=$(client "${client}" -- file-delete --name /logs/new.txt --host "${name}")
                    check "${pair}: [files] file-delete" "ok=true error=-" "$(outcome "${doc}")"
                    check "${pair}: [files] a deleted file is gone" "FILE_NOT_FOUND" \
                        "$(client "${client}" -- file-get --name /logs/new.txt --host "${name}" | jq -r '.fileStatus')"
                else
                    skip "${pair}: file-delete (needs client:fileDeleteClient)"
                fi
                sleep 0.3
                check "${pair}: [files] server file events" \
                    "read:/readme.txt:SUCCESS read:/data/blocks.bin:SUCCESS list:/:SUCCESS info:/readme.txt:SUCCESS write:/logs/new.txt:SUCCESS" \
                    "$(docker logs "${name}" 2>/dev/null | jq -r 'select(.event == "file") | "\(.action):\(.name):\(.status)"' | head -5 | paste -sd' ' -)"
            else
                fail "${pair}: [files] server did not start"
            fi
            stop_server "${name}"
        elif has "${client}" fileClient && ! has "${server}" fileServer && has_role "${server}" server; then
            # A server without files refuses the file transfer functions.
            name="ff-${server}-${RUN_ID}"
            start "${server}" "${name}" --
            if wait_ready "${name}"; then
                doc=$(client "${client}" -- file-get --name /readme.txt --host "${name}")
                check "${pair}: [files] an outstation without file transfer refuses it" "false" "$(jq -r '.ok' <<<"${doc}" 2>/dev/null)"
            fi
            stop_server "${name}"
        else
            skip "${pair}: files (needs server:fileServer,client:fileClient)"
        fi

        # ---- dual end point: the outstation dials, the master listens (1.2) ----
        if has "${server}" dualEndpointServer && has "${client}" dualEndpointClient; then
            reverse "${server}" "${client}" "${pair}" tcp
            if has "${server}" tls && has "${client}" dualEndpointClientTls; then
                reverse "${server}" "${client}" "${pair}" tls
            else
                skip "${pair}: dual end point over TLS (needs server:tls,client:dualEndpointClientTls)"
            fi
        else
            skip "${pair}: dual end point (needs server:dualEndpointServer,client:dualEndpointClient)"
        fi

        # ---- point types of the extended fixture (contract 1.2) ----
        if serves_extended "${server}"; then
            name="fp-${server}-${RUN_ID}"
            start "${server}" "${name}" -- --fixture /fixtures/extended/fixture.json
            if wait_ready "${name}"; then
                if has "${client}" frozenAnalogInputsClient; then
                    doc=$(client "${client}" -- read --group 31 --variation 0 --start 0 --stop 1 --host "${name}")
                    check "${pair}: [points] frozen analog inputs" "g31v1:0=1500 g31v7:1=49.5" "$(detail "${doc}")"
                    check "${pair}: [points] frozen analog flags" '[["ONLINE"],["ONLINE","OVER_RANGE"]]' \
                        "$(jq -cS '[.objects[]? | .flags] | sort' <<<"${doc}" 2>/dev/null)"
                else
                    skip "${pair}: frozen analog inputs (needs client:frozenAnalogInputsClient)"
                fi
                if has "${client}" timeAndIntervalClient; then
                    check "${pair}: [points] time and interval" "g50v4:0=15/2026-01-01T00:00:00.000Z/3" \
                        "$(detail "$(client "${client}" -- read --group 50 --variation 4 --start 0 --stop 0 --host "${name}")")"
                else
                    skip "${pair}: time and interval (needs client:timeAndIntervalClient)"
                fi
                if has "${client}" commandEventsClient; then
                    client "${client}" -- operate --type g12v1 --index 0 --code LATCH_ON --host "${name}" >/dev/null
                    client "${client}" -- operate --type g41v1 --index 0 --value 42 --host "${name}" >/dev/null
                    client "${client}" -- operate --type g41v1 --index 0 --value 5000000 --host "${name}" >/dev/null
                    check "${pair}: [points] command events with the status of each control" \
                        "g13v2:0=true/SUCCESS g43v3:0=42/SUCCESS g43v3:0=5000000/OUT_OF_RANGE" \
                        "$(detail "$(client "${client}" -- event-poll --classes 2 --host "${name}")")"
                    check "${pair}: [points] the outputs' own events are unchanged" \
                        "g11v2:0=true g2v2:1=true g32v1:0=42 g42v1:0=42" \
                        "$(objects "$(client "${client}" -- event-poll --classes 1 --host "${name}")")"
                else
                    skip "${pair}: command events (needs client:commandEventsClient)"
                fi
            else
                fail "${pair}: [points] server did not start"
            fi
            stop_server "${name}"
        else
            skip "${pair}: extended point types (needs a server that serves the extended fixture)"
        fi

        # ---- secure authentication ----
        if has "${server}" secureAuthentication && has "${client}" secureAuthentication; then
            name="fa-${server}-${RUN_ID}"
            start "${server}" "${name}" -- "${SA[@]}"
            if wait_ready "${name}"; then
                sa_run() { local a="$1" h="$2"; shift 2; client "${a}" -- "$@" --host "${h}" "${SA[@]}"; }
                basics "${pair}" sa "sa_run ${client} ${name}"
                sleep 0.3
                check "${pair}: [sa] the outstation authenticated the control" "true" \
                    "$(docker logs "${name}" 2>/dev/null | jq -s 'any(.[]; .event == "auth" and .user == 3 and .function == 5)')"
                doc=$(client "${client}" -- operate --type g12v1 --index 0 --code LATCH_OFF --host "${name}" \
                    --sa-update-key 0f0e0d0c0b0a09080706050403020100 --sa-user 3)
                check "${pair}: [sa] a wrong update key is refused" "ok=false error=auth-failed" "$(outcome "${doc}")"
                doc=$(client "${client}" -- operate --type g12v1 --index 0 --code LATCH_OFF --host "${name}" \
                    --sa-update-key "${SA_KEY}" --sa-user 4)
                check "${pair}: [sa] an unknown user is refused" "ok=false error=auth-failed" "$(outcome "${doc}")"
                doc=$(client "${client}" -- operate --type g12v1 --index 0 --code LATCH_OFF --host "${name}" --timeout-ms 2000)
                check "${pair}: [sa] a master without authentication cannot operate" "false" "$(jq -r '.ok' <<<"${doc}" 2>/dev/null)"
                sleep 0.3
                check "${pair}: [sa] the outstation reported the refusals" "1 11" \
                    "$(docker logs "${name}" 2>/dev/null | jq -r 'select(.event == "auth-error") | .code' | sort -un | paste -sd' ' -)"
                check "${pair}: [sa] the refused controls did not operate" "g10v2:0=true" \
                    "$(sa_run "${client}" "${name}" read --group 10 --variation 2 --start 0 --stop 0 | jq -r '[.objects[] | "g\(.group)v\(.variation):\(.index)=\(.value)"] | join(" ")')"
            else
                fail "${pair}: [sa] server did not start"
            fi
            stop_server "${name}"
        else
            skip "${pair}: secure authentication (needs server:secureAuthentication,client:secureAuthentication)"
        fi
    done

    # Every master lists and describes the files the same way.
    local f reference ref_client kind
    for kind in ls info; do
        reference="" ref_client=""
        for f in "${WORK}/agree-${server}/${kind}-"*; do
            [[ -f "${f}" ]] || continue
            client="${f##*/${kind}-}"
            if [[ -z "${reference}" ]]; then reference=$(cat "${f}"); ref_client="${client}"
            elif [[ "$(cat "${f}")" == "${reference}" ]]; then ok "${client}->${server} agrees with ${ref_client}->${server}: file-${kind}"
            else fail "${client}->${server} and ${ref_client}->${server} disagree on: file-${kind}"
                diff <(jq -S . <<<"${reference}") <(jq -S . "${f}") | head -20 >&2
            fi
        done
    done
    echo "${PASS} ${FAIL} ${SKIPPED}" > "${WORK}/tally-${server}"
}

log "adapters: ${ADAPTERS[*]} (${VERSION})"
[[ -n "${REPORT_DIR:-}" ]] && mkdir -p "${REPORT_DIR}"
servers=()
for adapter in "${ADAPTERS[@]}"; do
    has_role "${adapter}" server && servers+=("${adapter}")
done
k=0
for server in "${servers[@]}"; do
    suite "${server}" "${k}" &
    k=$((k + 1))
done
wait

PASS=0 FAIL=0 SKIPPED=0
for server in "${servers[@]}"; do
    if [[ ! -s "${WORK}/tally-${server}" ]]; then
        fail "the suite of ${server} did not finish"
        continue
    fi
    read -r p f s < "${WORK}/tally-${server}"
    PASS=$((PASS + p)) FAIL=$((FAIL + f)) SKIPPED=$((SKIPPED + s))
done
finish
