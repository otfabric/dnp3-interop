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
#           every master; (contract 1.3) appending to a file, and an
#           outstation that requires AUTHENTICATE_FILE: refusal without
#           credentials and with a wrong password, reads and writes with them
#   sa      secure authentication: reads and controls with the shared update
#           key, the server's auth events, refusal of a wrong key, of a wrong
#           user and of a master without authentication
#   reverse (contract 1.2) dual end point: the outstation dials a master
#           that listens, over TCP and, where declared, TLS
#   stats   (contract 1.2) security statistics: g121 by read and g122 events
#           from an outstation with secure authentication on
#   attributes (contract 1.3) device attributes of the attributes fixture:
#           read all of a set and one, write the writable ones and read them
#           back, refusal of a read-only one, the server's events
#   controls (contract 1.3) several controls in one request, direct and
#           select-before-operate, a refused one among them, and the limit
#           of the controls fixture (two per request)
#   freeze  (contract 1.3) FREEZE AT TIME, at once and scheduled, and the
#           freeze of analog inputs into frozen analog inputs
#   deadbands (contract 1.3) an analog deadband written (g34v3) holds back
#           an event of a smaller change and lets a larger one through, and
#           reads back (g34) where declared
#   outstations (contract 1.3) two outstations on one port: each reached by
#           its link address with its own database, no answer from a third
#   manage  (contract 1.3) the management functions 15..19: accepted and
#           reported where declared, refused elsewhere
#   broadcast (contract 1.3) a DIRECT OPERATE NO ACK to 0xFFFF: carried out,
#           IIN1.0 in the next response, the server's broadcast event
#   link    (contract 1.3) link-layer confirms from the master and from the
#           outstation, and keep-alives (REQUEST_LINK_STATUS) on an idle link
#           with the server's link-status events where it reports them
#   events  (contract 1.3) the events fixture's buffer of three overflows:
#           IIN2.3, the events that did not fit lost, and IIN2.3 cleared
#           once the master has confirmed what was reported
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

# Known upstream bugs of the versions under test: the entries of
# tests/known-bugs.tsv whose fifth column is "features".
: > "${WORK}/active"
: > "${WORK}/reproduced"
for adapter in "${ADAPTERS[@]}"; do
    docker run --rm "$(image_of "${adapter}")" print-capabilities > "${WORK}/caps-${adapter}.json" 2>/dev/null
    version=$(jq -r '.upstream.version' "${WORK}/caps-${adapter}.json")
    while IFS=$'\t' read -r bug_adapter bug_version bug_regex bug_text bug_fixture; do
        [[ -z "${bug_adapter}" || "${bug_adapter}" == \#* || "${bug_fixture:-}" != features ]] && continue
        if [[ "${bug_adapter%%:*}" == "${adapter}" && "${bug_version}" == "${version}" ]]; then
            printf '%s\t%s\t%s\n' "${bug_adapter}" "${bug_regex}" "${bug_text}" >> "${WORK}/active"
            log "known bug in ${adapter} ${version}: ${bug_text}"
        fi
    done < "${ROOT}/tests/known-bugs.tsv"
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
# known_bug <pair> <label>: the description of a known bug of the pairing
# whose regular expression matches the label, if there is one.
known_bug() {
    local client="${1%%->*}" server="${1#*->}" bug_adapter bug_regex bug_text
    while IFS=$'\t' read -r bug_adapter bug_regex bug_text; do
        if [[ "${bug_adapter}" == "${client}" || "${bug_adapter}" == "${server}" || "${bug_adapter}" == "${client}:client" \
              || "${bug_adapter}" == "${server}:server" ]] && [[ "$2" =~ ${bug_regex} ]]; then
            echo "${bug_text}"
            return 0
        fi
    done < "${WORK}/active"
    return 1
}

# check <label> <want> <got>: the label starts with the pairing.
check() {
    local bug
    if [[ "$3" == "$2" ]]; then ok "$1"; return 0; fi
    if bug=$(known_bug "${1%%: *}" "${1#*: }"); then
        KNOWN=$((KNOWN + 1)); record known "$1 -- ${bug}"; echo "${bug}" >> "${WORK}/reproduced"
        return 1
    fi
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
        "$(jq -rs '(map(select(.event == "ready")) | .[0]) as $r | "\($r.dial) \($r.transport) \(any(.[]; .event == "connection-opened"))"' \
            "${WORK}/events-fo-${server}-${client}-${RUN_ID}" 2>/dev/null)"
    doc=$(reverse_op "${server}" "${client}" "${tag}" operate --type g12v1 --index 0 --code LATCH_ON)
    check "${pair}: [reverse ${tag}] operate" "SUCCESS" "$(jq -r '.commandStatus' <<<"${doc}" 2>/dev/null)"
}

# multi_op <server> <client> <args>: operate-multi against a fresh outstation
# of the controls fixture; prints the result, then the binary and analog
# output values ("bo=... ao=...") on a second line.
multi_op() {
    local server="$1" client="$2"; shift 2
    local name="fm-${server}-${RUN_ID}" doc
    start "${server}" "${name}" -- --fixture /fixtures/controls/fixture.json
    wait_ready "${name}" || { echo "{}"; echo "server did not start"; return; }
    client "${client}" -- operate-multi "$@" --host "${name}" | jq -c '{error: .error.code, commandStatus, commandStatuses}'
    echo "bo=$(client dscsystems -- read --group 10 --variation 2 --start 0 --stop 2 --host "${name}" | jq -c '[.objects[].value]') ao=$(client dscsystems -- read --group 40 --variation 1 --start 0 --stop 1 --host "${name}" | jq -c '[.objects[].value]')"
    stop_server "${name}"
}

# multi <server> <client> <pair>
multi() {
    local server="$1" client="$2" pair="$3" out
    # statuses <want>: the per-control statuses, or null where the master does
    # not report them (stepfunc).
    statuses() { jq -c '.commandStatuses' <<<"${out%%$'\n'*}" | grep -qx "null" && echo null || echo "$1"; }
    out=$(multi_op "${server}" "${client}" --controls g12v1:0:LATCH_ON,g41v1:0:5)
    check "${pair}: [controls] two controls" \
        "{\"error\":null,\"commandStatus\":\"SUCCESS\",\"commandStatuses\":$(statuses '["SUCCESS","SUCCESS"]')}" "${out%%$'\n'*}"
    check "${pair}: [controls] both carried out" "bo=[true,false,false] ao=[5,0]" "${out#*$'\n'}"
    out=$(multi_op "${server}" "${client}" --controls g41v1:0:5,g41v1:1:500)
    check "${pair}: [controls] a refused control among them" \
        "{\"error\":\"command-failed\",\"commandStatus\":\"OUT_OF_RANGE\",\"commandStatuses\":$(statuses '["SUCCESS","OUT_OF_RANGE"]')}" "${out%%$'\n'*}"
    check "${pair}: [controls] the other is carried out" "bo=[false,false,false] ao=[5,0]" "${out#*$'\n'}"
    out=$(multi_op "${server}" "${client}" --controls g12v1:0:LATCH_ON,g41v1:1:7 --mode sbo)
    check "${pair}: [controls] select-before-operate of two" \
        "{\"error\":null,\"commandStatus\":\"SUCCESS\",\"commandStatuses\":$(statuses '["SUCCESS","SUCCESS"]')}" "${out%%$'\n'*}"
    check "${pair}: [controls] both operated" "bo=[true,false,false] ao=[0,7]" "${out#*$'\n'}"
    if has "${server}" maxControlsServer; then
        out=$(multi_op "${server}" "${client}" --controls g12v1:0:LATCH_ON,g12v1:1:LATCH_ON,g12v1:2:LATCH_ON)
        check "${pair}: [controls] the third of three is one too many" \
            "{\"error\":\"command-failed\",\"commandStatus\":\"TOO_MANY_OPS\",\"commandStatuses\":$(statuses '["SUCCESS","SUCCESS","TOO_MANY_OPS"]')}" "${out%%$'\n'*}"
        check "${pair}: [controls] the two within the limit are carried out" "bo=[true,true,false] ao=[0,0]" "${out#*$'\n'}"
    else
        skip "${pair}: control limit (needs server:maxControlsServer)"
    fi
}

suite() {
    local server="$1" k="$2" client pair name doc host run ip_server ip_client
    PASS=0 FAIL=0 SKIPPED=0 KNOWN=0
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

        # ---- file append and AUTHENTICATE_FILE (contract 1.3) ----
        if has "${server}" fileAppendServer && has "${client}" fileAppendClient; then
            name="fa-${server}-${RUN_ID}"
            start "${server}" "${name}" -- --fixture /fixtures/files/fixture.json
            if wait_ready "${name}"; then
                client "${client}" -- file-put --name /logs/a.txt --content 6162 --host "${name}" >/dev/null
                doc=$(client "${client}" -- file-put --name /logs/a.txt --content 6364 --append --host "${name}")
                check "${pair}: [files] file-put --append" "ok=true error=-" "$(outcome "${doc}")"
                check "${pair}: [files] adds to the end of the file" "61626364" \
                    "$(client "${client}" -- file-get --name /logs/a.txt --host "${name}" | jq -r '.file.content')"
            else
                fail "${pair}: [files] server did not start"
            fi
            stop_server "${name}"
        else
            skip "${pair}: file append (needs server:fileAppendServer,client:fileAppendClient)"
        fi
        if has "${server}" fileAuthServer && has "${client}" fileAuthClient; then
            name="fu-${server}-${RUN_ID}"
            local creds=(--file-user operator --file-password s3cret)
            start "${server}" "${name}" -- --fixture /fixtures/files/fixture.json "${creds[@]}"
            if wait_ready "${name}"; then
                doc=$(client "${client}" -- file-authenticate "${creds[@]}" --host "${name}")
                check "${pair}: [files] file-authenticate" "ok=true error=- SUCCESS" "$(outcome "${doc}") $(jq -r '.fileStatus' <<<"${doc}" 2>/dev/null)"
                doc=$(client "${client}" -- file-authenticate --file-user operator --file-password wrong --host "${name}")
                check "${pair}: [files] a wrong password is refused" "ok=false error=file-error PERMISSION_DENIED" \
                    "$(outcome "${doc}") $(jq -r '.fileStatus' <<<"${doc}" 2>/dev/null)"
                doc=$(client "${client}" -- file-get --name /readme.txt --host "${name}")
                check "${pair}: [files] an open without authentication is refused" "ok=false error=file-error PERMISSION_DENIED" \
                    "$(outcome "${doc}") $(jq -r '.fileStatus' <<<"${doc}" 2>/dev/null)"
                doc=$(client "${client}" -- file-get --name /readme.txt "${creds[@]}" --host "${name}")
                check "${pair}: [files] file-get with credentials" "${README_HEX}" "$(jq -r '.file.content' <<<"${doc}" 2>/dev/null)"
                doc=$(client "${client}" -- file-put --name /logs/auth.txt --content 00 "${creds[@]}" --host "${name}")
                check "${pair}: [files] file-put with credentials" "ok=true error=-" "$(outcome "${doc}")"
                sleep 0.3
                check "${pair}: [files] server authenticate events" \
                    "authenticate:operator:SUCCESS authenticate:operator:PERMISSION_DENIED" \
                    "$(docker logs "${name}" 2>/dev/null | jq -r 'select(.event == "file" and .action == "authenticate") | "\(.action):\(.name):\(.status)"' | head -2 | paste -sd' ' -)"
            else
                fail "${pair}: [files] server did not start"
            fi
            stop_server "${name}"
        else
            skip "${pair}: file authentication (needs server:fileAuthServer,client:fileAuthClient)"
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

        # ---- security statistics (contract 1.2) ----
        if has "${server}" securityStatisticsServer && has "${client}" securityStatisticsClient; then
            name="fx-${server}-${RUN_ID}"
            start "${server}" "${name}" -- "${SA[@]}" --sa-statistics-class 2 --sa-statistics-threshold 1
            if wait_ready "${name}"; then
                client "${client}" -- integrity-poll --host "${name}" "${SA[@]}" >/dev/null
                client "${client}" -- operate --type g12v1 --index 0 --code LATCH_ON --host "${name}" \
                    --sa-update-key 0f0e0d0c0b0a09080706050403020100 --sa-user 3 >/dev/null
                doc=$(client "${client}" -- read --group 121 --variation 1 --start 0 --stop 17 --host "${name}" "${SA[@]}")
                check "${pair}: [stats] g121 has the 18 statistics, association 0" "18 0-17 [0]" \
                    "$(jq -r '"\(.objects | length) \(.objects[0].index)-\(.objects[-1].index) \([.objects[].association] | unique | tostring)"' <<<"${doc}" 2>/dev/null)"
                check "${pair}: [stats] a refused key change is counted (statistics 10 and 14)" "true true" \
                    "$(jq -r '"\(.objects[] | select(.index == 10) | .value > 0) \(.objects[] | select(.index == 14) | .value > 0)"' <<<"${doc}" 2>/dev/null)"
                doc=$(client "${client}" -- event-poll --classes 2 --host "${name}" "${SA[@]}")
                check "${pair}: [stats] g122 events in the configured class" "true true" \
                    "$(jq -r '"\((.objects | length) > 0 and all(.objects[]; .group == 122 and .variation == 2)) \(any(.objects[]; .index == 14))"' <<<"${doc}" 2>/dev/null)"
            else
                fail "${pair}: [stats] server did not start"
            fi
            stop_server "${name}"
            name="fy-${server}-${RUN_ID}"
            start "${server}" "${name}" --
            if wait_ready "${name}"; then
                check "${pair}: [stats] without secure authentication g121 is refused" "ok=false error=rejected" \
                    "$(outcome "$(client "${client}" -- read --group 121 --variation 1 --start 0 --stop 17 --host "${name}")")"
            fi
            stop_server "${name}"
        else
            skip "${pair}: security statistics (needs server:securityStatisticsServer,client:securityStatisticsClient)"
        fi

        # ---- several controls in one request (contract 1.3) ----
        if has_role "${server}" server && has "${client}" multipleControlsClient; then
            multi "${server}" "${client}" "${pair}"
        else
            skip "${pair}: several controls (needs client:multipleControlsClient)"
        fi

        # ---- FREEZE AT TIME and the analog freeze (contract 1.3) ----
        if has "${server}" freezeAtTimeServer && has "${client}" freezeAtTimeClient; then
            name="fz-${server}-${RUN_ID}"
            start "${server}" "${name}" --
            if wait_ready "${name}"; then
                doc=$(client "${client}" -- freeze --at immediately --host "${name}")
                check "${pair}: [freeze] at time 0 freezes at once" "ok=true error=- g21v1:0=123456 g21v2:1=65535" \
                    "$(outcome "${doc}") $(objects "$(client "${client}" -- read --group 21 --variation 0 --start 0 --stop 1 --host "${name}")")"
            fi
            stop_server "${name}"
            start "${server}" "${name}" --
            if wait_ready "${name}"; then
                doc=$(client "${client}" -- freeze --at +1500 --host "${name}")
                check "${pair}: [freeze] a freeze in 1.5 s is accepted, not done yet" "ok=true error=- g21v1:0=0 g21v2:1=0" \
                    "$(outcome "${doc}") $(objects "$(client "${client}" -- read --group 21 --variation 0 --start 0 --stop 1 --host "${name}")")"
                # A master stays connected meanwhile: go-dnp3 runs scheduled
                # freezes only while one is (COMPATIBILITY.md).
                client "${client}" -- monitor --duration-ms 2500 --host "${name}" >/dev/null
                check "${pair}: [freeze] and done at its time" "g21v1:0=123456 g21v2:1=65535" \
                    "$(objects "$(client "${client}" -- read --group 21 --variation 0 --start 0 --stop 1 --host "${name}")")"
            else
                fail "${pair}: [freeze] server did not start"
            fi
            stop_server "${name}"
        else
            skip "${pair}: freeze at time (needs server:freezeAtTimeServer,client:freezeAtTimeClient)"
        fi
        if has "${server}" analogFreezeServer && has "${client}" analogFreezeClient && serves_extended "${server}"; then
            name="fy-${server}-${RUN_ID}"
            start "${server}" "${name}" -- --fixture /fixtures/extended/fixture.json
            if wait_ready "${name}"; then
                doc=$(client "${client}" -- freeze --group 30 --host "${name}")
                check "${pair}: [freeze] a freeze of analog inputs" "ok=true error=-" "$(outcome "${doc}")"
                check "${pair}: [freeze] copies them into the frozen analog inputs of the same index" "0 49.5" \
                    "$(client dscsystems -- read --group 31 --variation 0 --start 0 --stop 1 --host "${name}" | jq -r '[.objects[].value] | join(" ")')"
            else
                fail "${pair}: [freeze] server did not start"
            fi
            stop_server "${name}"
        else
            skip "${pair}: analog freeze (needs server:analogFreezeServer,client:analogFreezeClient)"
        fi

        # ---- several outstations (contract 1.3) ----
        if has "${server}" multipleOutstationsServer; then
            name="fs-${server}-${RUN_ID}"
            start "${server}" "${name}" -- --fixture /fixtures/controls/fixture.json --outstations 2
            if wait_ready "${name}"; then
                local ao='[.objects[]? | select(.group == 40) | .value] | join(" ")'
                doc=$(client "${client}" -- operate --type g41v1 --index 0 --value 9 --remote-addr 1025 --host "${name}")
                check "${pair}: [outstations] a control of the second" "ok=true error=-" "$(outcome "${doc}")"
                check "${pair}: [outstations] the second has it" "9" \
                    "$(client "${client}" -- read --group 40 --variation 0 --start 0 --stop 0 --remote-addr 1025 --host "${name}" | jq -r "${ao}")"
                check "${pair}: [outstations] the first does not" "0" \
                    "$(client "${client}" -- read --group 40 --variation 0 --start 0 --stop 0 --remote-addr 1024 --host "${name}" | jq -r "${ao}")"
                doc=$(client "${client}" -- integrity-poll --remote-addr 1026 --timeout-ms 1500 --host "${name}")
                check "${pair}: [outstations] no third" "ok=false error=timeout" "$(outcome "${doc}")"
            else
                fail "${pair}: [outstations] server did not start"
            fi
            stop_server "${name}"
        else
            skip "${pair}: several outstations (needs server:multipleOutstationsServer)"
        fi

        # ---- management functions (contract 1.3) ----
        if has "${client}" manageClient && has_role "${server}" server; then
            name="fm-${server}-${RUN_ID}"
            start "${server}" "${name}" -- --fixture /fixtures/controls/fixture.json
            if wait_ready "${name}"; then
                if has "${server}" manageServer; then
                    local f got=""
                    for f in initialize-data initialize-application start-application stop-application save-configuration; do
                        got+="$(outcome "$(client "${client}" -- manage --function "${f}" --host "${name}")") "
                    done
                    check "${pair}: [manage] the five functions" "$(printf 'ok=true error=- %.0s' 1 2 3 4 5)" "${got}"
                    sleep 0.3
                    check "${pair}: [manage] the server's events" "15 16 17 18 19" \
                        "$(docker logs "${name}" 2>/dev/null | jq -r 'select(.event == "manage") | .function' | paste -sd' ' -)"
                else
                    doc=$(client "${client}" -- manage --function save-configuration --host "${name}")
                    check "${pair}: [manage] an outstation without them refuses" "ok=false error=rejected" "$(outcome "${doc}")"
                fi
            else
                fail "${pair}: [manage] server did not start"
            fi
            stop_server "${name}"
        else
            skip "${pair}: management functions (needs client:manageClient)"
        fi

        # ---- broadcast (contract 1.3) ----
        if has "${server}" broadcastServer && has "${client}" broadcastClient; then
            name="fc-${server}-${RUN_ID}"
            start "${server}" "${name}" -- --fixture /fixtures/controls/fixture.json
            if wait_ready "${name}"; then
                doc=$(client "${client}" -- operate --type g41v1 --index 0 --value 77 --mode direct-no-ack --broadcast --host "${name}")
                check "${pair}: [broadcast] a control to 0xFFFF" "ok=true error=-" "$(outcome "${doc}")"
                sleep 0.3
                # Read by stepfunc's master, which sends nothing first: go-dnp3's
                # writes IIN at start, and that response carries IIN1.0.
                doc=$(client stepfunc -- read --group 40 --variation 0 --start 0 --stop 0 --host "${name}")
                check "${pair}: [broadcast] is carried out" "77" "$(jq -r '[.objects[]?.value] | join(" ")' <<<"${doc}" 2>/dev/null)"
                check "${pair}: [broadcast] IIN1.0 in the next response" "true" \
                    "$(jq -r '.iin | index("BROADCAST") != null' <<<"${doc}" 2>/dev/null)"
                if has "${server}" broadcastEventServer; then
                    check "${pair}: [broadcast] the server's event" "true" \
                        "$(docker logs "${name}" 2>/dev/null | jq -s 'any(.[]; .event == "broadcast" and .function == 6)')"
                fi
            else
                fail "${pair}: [broadcast] server did not start"
            fi
            stop_server "${name}"
        else
            skip "${pair}: broadcast (needs server:broadcastServer,client:broadcastClient)"
        fi

        # ---- link layer (contract 1.3) ----
        if has_role "${server}" server && { has "${client}" linkConfirmsClient || has "${server}" linkConfirmsServer \
                || has "${client}" keepAliveClient; }; then
            name="fl-${server}-${RUN_ID}"
            if has "${client}" linkConfirmsClient; then
                start "${server}" "${name}" -- --fixture /fixtures/controls/fixture.json
                if wait_ready "${name}"; then
                    doc=$(client "${client}" -- integrity-poll --link-confirms --host "${name}")
                    check "${pair}: [link] a poll with link-layer confirms" "ok=true error=-" "$(outcome "${doc}")"
                    doc=$(client "${client}" -- operate --type g12v1 --index 0 --code LATCH_ON --link-confirms --host "${name}")
                    check "${pair}: [link] a control with link-layer confirms" "ok=true error=-" "$(outcome "${doc}")"
                fi
                stop_server "${name}"
            fi
            if has "${server}" linkConfirmsServer; then
                start "${server}" "${name}" -- --fixture /fixtures/controls/fixture.json --link-confirms
                if wait_ready "${name}"; then
                    doc=$(client "${client}" -- integrity-poll --host "${name}")
                    check "${pair}: [link] a poll of an outstation that confirms" "ok=true error=-" "$(outcome "${doc}")"
                fi
                stop_server "${name}"
            fi
            if has "${client}" keepAliveClient; then
                start "${server}" "${name}" -- --fixture /fixtures/controls/fixture.json
                if wait_ready "${name}"; then
                    doc=$(client "${client}" -- monitor --duration-ms 3500 --keep-alive-ms 1000 --host "${name}")
                    check "${pair}: [link] keep-alives on an idle link" "ok=true error=-" "$(outcome "${doc}")"
                    if has "${server}" linkStatusEventServer; then
                        check "${pair}: [link] the server sees them" "true" \
                            "$(docker logs "${name}" 2>/dev/null | jq -s '[.[] | select(.event == "link-status")] | length >= 2')"
                    fi
                fi
                stop_server "${name}"
            fi
        else
            skip "${pair}: link layer (needs client:linkConfirmsClient|server:linkConfirmsServer|client:keepAliveClient)"
        fi

        # ---- event buffer overflow (contract 1.3) ----
        if has "${server}" eventBufferServer; then
            name="fo-${server}-${RUN_ID}"
            start "${server}" "${name}" -- --fixture /fixtures/events/fixture.json
            if wait_ready "${name}"; then
                # Ten changes of AI0 (class 2) into a buffer of three.
                local v
                for v in 10 20 30 40 50 60 70 80 90 100; do
                    client dscsystems -- operate --type g41v1 --index 0 --value "${v}" --host "${name}" >/dev/null
                done
                doc=$(client "${client}" -- event-poll --classes 1,2,3 --host "${name}")
                check "${pair}: [events] an overflowing buffer sets IIN2.3" "true" \
                    "$(jq -r '.iin | index("EVENT_BUFFER_OVERFLOW") != null' <<<"${doc}" 2>/dev/null)"
                check "${pair}: [events] loses what did not fit, keeps the latest" "true" \
                    "$(jq -r '[.objects[]? | select(.group == 32) | .value] | length < 10 and .[-1] == 100' <<<"${doc}" 2>/dev/null)"
                doc=$(client "${client}" -- event-poll --classes 1,2,3 --host "${name}")
                check "${pair}: [events] IIN2.3 clears once the events are confirmed" "false" \
                    "$(jq -r '.iin | index("EVENT_BUFFER_OVERFLOW") != null' <<<"${doc}" 2>/dev/null)"
            else
                fail "${pair}: [events] server did not start"
            fi
            stop_server "${name}"
        else
            skip "${pair}: event buffer overflow (needs server:eventBufferServer)"
        fi

        # ---- analog deadbands (contract 1.3) ----
        if has "${server}" deadbandWriteServer && has "${client}" deadbandWriteClient; then
            name="fb-${server}-${RUN_ID}"
            start "${server}" "${name}" --
            if wait_ready "${name}"; then
                # AO0 drives AI4 (class 2). One change first: stepfunc compares
                # with the last value reported, RESTART until there is one.
                client dscsystems -- operate --type g41v1 --index 0 --value 1000 --host "${name}" >/dev/null
                client dscsystems -- event-poll --classes 1,2,3 --host "${name}" >/dev/null
                doc=$(client "${client}" -- deadband-write --deadbands 4:100 --host "${name}")
                check "${pair}: [deadbands] write a deadband" "ok=true error=-" "$(outcome "${doc}")"
                local ai4='[.objects[]? | select(.group == 32 and .index == 4) | .value] | join(" ")'
                client dscsystems -- operate --type g41v1 --index 0 --value 1050 --host "${name}" >/dev/null
                check "${pair}: [deadbands] a change within it reports no event" "" \
                    "$(client dscsystems -- event-poll --classes 2 --host "${name}" | jq -r "${ai4}")"
                client dscsystems -- operate --type g41v1 --index 0 --value 1200 --host "${name}" >/dev/null
                check "${pair}: [deadbands] a change beyond it does" "1200" \
                    "$(client dscsystems -- event-poll --classes 2 --host "${name}" | jq -r "${ai4}")"
                if has "${server}" deadbandReadServer && has "${client}" deadbandReadClient; then
                    check "${pair}: [deadbands] and reads back" "g34v3:4=100" \
                        "$(objects "$(client "${client}" -- read --group 34 --variation 3 --start 4 --stop 4 --host "${name}")")"
                fi
            else
                fail "${pair}: [deadbands] server did not start"
            fi
            stop_server "${name}"
        else
            skip "${pair}: analog deadbands (needs server:deadbandWriteServer,client:deadbandWriteClient)"
        fi

        # ---- device attributes (contract 1.3) ----
        if has "${server}" deviceAttributesServer && has "${client}" deviceAttributesClient; then
            name="fd-${server}-${RUN_ID}"
            start "${server}" "${name}" -- --fixture /fixtures/attributes/fixture.json
            if wait_ready "${name}"; then
                local attrs='[.attributes[]? | select(.variation != 3) | "\(.variation):\(.type):\(.value)"] | join(" ")'
                doc=$(client "${client}" -- attribute-read --set 1 --host "${name}")
                check "${pair}: [attributes] read a set" \
                    "1:visibleString:dnp3-interop reference outstation 2:unsignedInt:4000000000 10:visibleString:writable text 11:unsignedInt:7" \
                    "$(jq -r "${attrs}" <<<"${doc}" 2>/dev/null)"
                check "${pair}: [attributes] signed integer" "signedInt:-42" \
                    "$(jq -r '.attributes[]? | select(.variation == 3) | "\(.type):\(.value)"' <<<"${doc}" 2>/dev/null)"
                check "${pair}: [attributes] read one" "2:unsignedInt:4000000000" \
                    "$(client "${client}" -- attribute-read --set 1 --variation 2 --host "${name}" | jq -r "${attrs}")"
                doc=$(client "${client}" -- attribute-write --set 1 --variation 10 --text changed --host "${name}")
                check "${pair}: [attributes] write a string" "ok=true error=-" "$(outcome "${doc}")"
                doc=$(client "${client}" -- attribute-write --set 1 --variation 11 --uint 99 --host "${name}")
                check "${pair}: [attributes] write an unsigned integer" "ok=true error=-" "$(outcome "${doc}")"
                check "${pair}: [attributes] the written values read back" "10:visibleString:changed 11:unsignedInt:99" \
                    "$(client "${client}" -- attribute-read --set 1 --host "${name}" | jq -r '[.attributes[]? | select(.variation >= 10) | "\(.variation):\(.type):\(.value)"] | join(" ")')"
                doc=$(client "${client}" -- attribute-write --set 1 --variation 1 --text no --host "${name}")
                check "${pair}: [attributes] a read-only attribute refuses a write" "false" "$(jq -r '.ok' <<<"${doc}" 2>/dev/null)"
                sleep 0.3
                check "${pair}: [attributes] server events" "10:changed 11:99" \
                    "$(docker logs "${name}" 2>/dev/null | jq -r 'select(.event == "attribute-write") | "\(.variation):\(.value)"' | paste -sd' ' -)"
            else
                fail "${pair}: [attributes] server did not start"
            fi
            stop_server "${name}"
        else
            skip "${pair}: device attributes (needs server:deviceAttributesServer,client:deviceAttributesClient)"
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
            # No statistics events (contract 1.2) in the polls of these checks.
            if has "${server}" securityStatisticsServer; then
                start "${server}" "${name}" -- "${SA[@]}" --sa-statistics-threshold 1000000
            else
                start "${server}" "${name}" -- "${SA[@]}"
            fi
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
    echo "${PASS} ${FAIL} ${SKIPPED} ${KNOWN}" > "${WORK}/tally-${server}"
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

PASS=0 FAIL=0 SKIPPED=0 KNOWN=0
for server in "${servers[@]}"; do
    if [[ ! -s "${WORK}/tally-${server}" ]]; then
        fail "the suite of ${server} did not finish"
        continue
    fi
    read -r p f s k < "${WORK}/tally-${server}"
    PASS=$((PASS + p)) FAIL=$((FAIL + f)) SKIPPED=$((SKIPPED + s)) KNOWN=$((KNOWN + k))
done
# A known bug that no longer shows is a stale entry, unless it is marked
# "(intermittent)".
while IFS=$'\t' read -r bug_adapter _ bug_text; do
    [[ "${bug_text}" == *"(intermittent)" ]] && continue
    grep -qxF "${bug_text}" "${WORK}/reproduced" || fail "known bug of ${bug_adapter} did not reproduce: ${bug_text}"
done < "${WORK}/active"
finish
