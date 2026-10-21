#!/usr/bin/env bash
# Reference-stack self-test: every client adapter against every server adapter.
#
# Usage: interop.sh <adapter> [<adapter> ...]      e.g. interop.sh stepfunc opendnp3
#
# For each case in tests/cases.tsv and each client/server pairing:
#   - the outcome must match the expectation of the case, and
#   - all pairings must return the same result document (modulo time stamps,
#     header order and the restart bit).
# Then checks on values rather than outcomes, each on a fresh server:
#   - an integrity poll returns exactly the points of the fixture,
#   - controls change the outputs and their targets and produce the events
#     of their classes, once,
#   - a freeze copies the counters, a freeze-and-clear clears them,
#   - enabling unsolicited responses delivers buffered events unsolicited,
#   - a class assignment moves the events of a point to another class,
#   - a time synchronization clears NEED_TIME.
#
# The work runs as one suite per server adapter, all at the same time (an
# outstation serves one master at a time, so it is split by server); the
# agreement of the documents across pairings is checked when all are done.
#
# tests/known-bugs.tsv lists bugs of specific upstream versions; a case they
# explain is counted as a known bug instead of a failure.
#
# This tests the reference adapters against each other. It says nothing about
# any library that consumes the images.
#
# Environment: REGISTRY, VERSION (image tag, default dev), VERBOSE=1,
#              REPORT_DIR (keeps every result document when set),
#              FIXTURE (default baseline): the fixture the servers serve, with
#              the cases of tests/cases-<fixture>.tsv (tests/cases.tsv for the
#              baseline). Only adapters whose pointTypes cover the fixture
#              serve; the value checks beyond the integrity poll are the
#              baseline's.

set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
# shellcheck source=scripts/lib.sh
source "${SCRIPT_DIR}/lib.sh"

[[ $# -ge 1 ]] || { echo "usage: interop.sh <adapter> [<adapter> ...]" >&2; exit 2; }
ADAPTERS=("$@")
FIXTURE_NAME="${FIXTURE:-baseline}"
FIXTURE="${ROOT}/fixtures/${FIXTURE_NAME}/fixture.json"
[[ -f "${FIXTURE}" ]] || { echo "no fixture ${FIXTURE_NAME}" >&2; exit 2; }
SERVER_ARGS=() SUFFIX=""
CASES="${ROOT}/tests/cases.tsv"
if [[ "${FIXTURE_NAME}" != baseline ]]; then
    SERVER_ARGS=(--fixture "/fixtures/${FIXTURE_NAME}/fixture.json")
    SUFFIX="-${FIXTURE_NAME}"
    CASES="${ROOT}/tests/cases-${FIXTURE_NAME}.tsv"
fi
KNOWN_BUGS="${ROOT}/tests/known-bugs.tsv"
require_tools
WORK=$(mktemp -d)
trap 'rm -rf "${WORK}"; cleanup' EXIT
network_up

# ---- known upstream bugs that apply to the versions under test ----
# ${WORK}/active: "<adapter>\t<operation regex>\t<description>" per line.
: > "${WORK}/active"
: > "${WORK}/reproduced"
for adapter in "${ADAPTERS[@]}"; do
    docker run --rm "$(image_of "${adapter}")" print-capabilities > "${WORK}/caps-${adapter}.json" 2>/dev/null
    version=$(jq -r '.upstream.version' "${WORK}/caps-${adapter}.json")
    log "${adapter}: upstream ${version}"
    while IFS=$'\t' read -r bug_adapter bug_version bug_regex bug_text bug_fixture; do
        [[ -z "${bug_adapter}" || "${bug_adapter}" == \#* ]] && continue
        [[ "${bug_fixture:-baseline}" == "${FIXTURE_NAME}" ]] || continue
        if [[ "${bug_adapter%%:*}" == "${adapter}" && "${bug_version}" == "${version}" ]]; then
            printf '%s\t%s\t%s\n' "${bug_adapter}" "${bug_regex}" "${bug_text}" >> "${WORK}/active"
            log "known bug in ${adapter} ${version}: ${bug_text}"
        fi
    done < "${KNOWN_BUGS}"
done

# has <adapter> <feature>: the adapter declares the feature.
has() { jq -e --arg f "$2" '.features[$f] == true' "${WORK}/caps-$1.json" >/dev/null 2>&1; }
# has_server <adapter> <feature>: the feature as its server role has it
# (serverFeatures, contract 1.2, where it differs from features).
has_server() {
    jq -e --arg f "$2" 'if (.serverFeatures // {}) | has($f) then .serverFeatures[$f] else .features[$f] end == true' \
        "${WORK}/caps-$1.json" >/dev/null 2>&1
}
# serves <adapter>: the adapter's outstation serves the fixture: its point
# types cover the fixture's.
serves() {
    jq -e --slurpfile fx "${FIXTURE}" '.roles.server and ((.pointTypes) as $p
        | [$fx[0] | keys[] | select(. as $k | ["binaryInputs","doubleBitInputs","binaryOutputs","counters",
            "frozenCounters","analogInputs","analogOutputs","octetStrings","frozenAnalogInputs","timeAndIntervals"]
            | index($k))] | all(. as $k | $p | index($k)))' "${WORK}/caps-$1.json" >/dev/null 2>&1
}

# has_role <adapter> <server|client>
has_role() { jq -e --arg r "$2" '.roles[$r] == true' "${WORK}/caps-$1.json" >/dev/null 2>&1; }

# supported <client> <server> <needs>: the pairing declares every feature in
# the comma-separated list of "server:<feature>" and "client:<feature>"; a
# feature written "!<feature>" must be declared false.
supported() {
    local need side feature adapter
    has_role "$1" client && serves "$2" || return 1
    [[ "$3" == "-" ]] && return 0
    for need in ${3//,/ }; do
        side="${need%%:*}"; feature="${need#*:}"
        if [[ "${side}" == "server" ]]; then adapter="$2"; else adapter="$1"; fi
        local test=has
        [[ "${side}" == server ]] && test=has_server
        if [[ "${feature}" == !* ]]; then
            "${test}" "${adapter}" "${feature#!}" && return 1
        else
            "${test}" "${adapter}" "${feature}" || return 1
        fi
    done
    return 0
}

SKIPPED=0
skip() { SKIPPED=$((SKIPPED + 1)); record skip "$*"; [[ -n "${VERBOSE:-}" ]] && log "skip: $*"; return 0; }

# known_bug <client> <server> <operation>: prints the description of a known
# bug that may affect this pairing and operation, if there is one.
known_bug() {
    local bug_adapter bug_regex bug_text
    while IFS=$'\t' read -r bug_adapter bug_regex bug_text; do
        if [[ "${bug_adapter}" == "$1" || "${bug_adapter}" == "$2" || "${bug_adapter}" == "$1:client" \
              || "${bug_adapter}" == "$2:server" ]] && [[ "$3" =~ ${bug_regex} ]]; then
            echo "${bug_text}"
            return 0
        fi
    done < "${WORK}/active"
    return 1
}

# hit_known_bug <pair> <operation> <description>
hit_known_bug() {
    KNOWN=$((KNOWN + 1))
    echo "$3" >> "${WORK}/reproduced"
    record known "$1: $2 -- $3"
    [[ -n "${VERBOSE:-}" ]] && log "known bug: $1: $2"
    return 0
}

# One server per adapter is shared by the read-only cases; cases that change
# the outstation get a server of their own.
shared_server() { SERVER_TAG=shared server_name "$1"; }
for adapter in "${ADAPTERS[@]}"; do
    serves "${adapter}" || continue
    name=$(SERVER_TAG=shared start_server "${adapter}" ${SERVER_ARGS[@]+"${SERVER_ARGS[@]}"})
    wait_ready "${name}" || exit 1
done

keep() {  # keep <label> <document>
    [[ -n "${REPORT_DIR:-}" ]] || return 0
    mkdir -p "${REPORT_DIR}"
    printf '%s\n' "$2" > "${REPORT_DIR}/$(echo "$1" | tr -c 'A-Za-z0-9._\n-' '_').json"
}

# ---- values ----
# objects <document>: "g<group>v<variation>:<index>=<value>" per object,
# sorted, unsolicited ones marked "u"; what the value checks compare.
objects() {
    jq -r '[.objects[]? | "g\(.group)v\(.variation)\(if .unsolicited then "u" else "" end):\(.index)=\(.value|tostring)"] | sort | join(" ")' <<<"$1" 2>/dev/null
}

# check <pair> <label> <want> <got> [<operation a known bug may affect>]
check() {
    local client="${1%%->*}" server="${1#*->}" bug
    if [[ "$4" == "$3" ]]; then ok "$1: $2"
    elif [[ -n "${5:-}" ]] && bug=$(known_bug "${client}" "${server}" "$5"); then hit_known_bug "$1" "$2" "${bug}"
    else
        fail "$1: $2"; log "    want ${3:-<nothing>}"; log "    got  ${4:-<nothing>}"
        # The outstation's own account (its frames), while the server exists.
        if [[ -n "${name:-}" ]] && docker inspect "${name}" >/dev/null 2>&1; then
            log "    log of ${name}:"
            docker logs --tail 60 "${name}" 2>&1 | sed 's/^/        /' >&2
        fi
    fi
}

# The integrity poll of the fixture, as objects() prints it: every point in
# its static variation.
want_static=$(jq -r '
    def sv($d): (.staticVariation // $d);
    [ (.binaryInputs[]?    | "g1v\(sv(2)):\(.index)=\(.value)"),
      (.doubleBitInputs[]? | "g3v\(sv(2)):\(.index)=\(.value)"),
      (.binaryOutputs[]?   | "g10v\(sv(2)):\(.index)=\(.value)"),
      (.counters[]?        | "g20v\(sv(1)):\(.index)=\(.value)"),
      (.frozenCounters[]?  | "g21v\(sv(1)):\(.index)=\(.value)"),
      (.analogInputs[]?    | "g30v\(sv(1)):\(.index)=\(.value)"),
      (.analogOutputs[]?   | "g40v\(sv(1)):\(.index)=\(.value)"),
      (.octetStrings[]?    | "g110v\(.value | length / 2):\(.index)=\(.value)") ] | sort | join(" ")' "${FIXTURE}")
want_flags=$(jq -cS '
    [ (.binaryInputs[]?, .doubleBitInputs[]?, .binaryOutputs[]?, .counters[]?, .frozenCounters[]?,
       .analogInputs[]?, .analogOutputs[]?) | .flags // ["ONLINE"] ] | sort' "${FIXTURE}")

# suite <server>: every case and every value check against the outstations of
# one server adapter, with every client adapter. One suite runs per server
# adapter, all at the same time: an outstation has one master connection at a
# time, so the work is split by server, never within one. A suite keeps the
# normalized document of every pairing that met its expectation, for the
# agreement check across pairings, and leaves its counts in ${WORK}.
suite() {
    local server="$1" n=0 state want_exit want_summary needs operation client pair name doc summary bug got got_flags procedure
    PASS=0 FAIL=0 SKIPPED=0 KNOWN=0
    if [[ -n "${REPORT_DIR:-}" ]]; then RESULTS_FILE="${REPORT_DIR}/results-interop${SUFFIX}-${server}.tsv"; : > "${RESULTS_FILE}"; fi
    while IFS=$'\t' read -r state want_exit want_summary needs operation; do
        [[ -z "${state}" || "${state}" == \#* ]] && continue
        n=$((n + 1))
        mkdir -p "${WORK}/agree/${n}"
        for client in "${ADAPTERS[@]}"; do
            pair="${client}->${server}"
            if ! supported "${client}" "${server}" "${needs}"; then
                skip "${pair}: ${operation} (needs ${needs})"
                continue
            fi
            if [[ "${state}" == "rw" ]]; then
                name=$(SERVER_TAG=rw start_server "${server}" ${SERVER_ARGS[@]+"${SERVER_ARGS[@]}"}); wait_ready "${name}" || { fail "${pair}: server did not start"; continue; }
            else
                name=$(shared_server "${server}")
            fi
            read -r -a op_args <<<"${operation}"
            doc=$(run_client "${client}" "${op_args[@]}" --host "${name}"); CLIENT_EXIT=$?
            keep "case${SUFFIX}${n}-${pair}" "${doc}"

            bug=$(known_bug "${client}" "${server}" "${operation}")
            summary=$(jq -r "${SUMMARY}" <<<"${doc}" 2>/dev/null)
            if [[ "${summary}" != "${want_summary}" || "${CLIENT_EXIT}" != "${want_exit}" ]]; then
                if [[ -n "${bug}" ]]; then
                    [[ "${state}" == "rw" ]] && stop_server "${name}"
                    hit_known_bug "${pair}" "${operation}" "${bug}"
                    continue
                fi
                fail "${pair}: ${operation}"
                log "    want exit=${want_exit} ${want_summary}"
                log "    got  exit=${CLIENT_EXIT} ${summary:-<no result document>}"
                log "    message: $(jq -r '.error.message // "-"' <<<"${doc}" 2>/dev/null)"
                # The outstation's own account of the exchange (its frames, with
                # DNP3_INTEROP_DEBUG=1), while the server still exists.
                log "    log of ${name}:"
                docker logs --tail 60 "${name}" 2>&1 | sed 's/^/        /' >&2
                [[ "${state}" == "rw" ]] && stop_server "${name}"
                continue
            fi
            [[ "${state}" == "rw" ]] && stop_server "${name}"
            ok "${pair}: ${operation}"
            # A pairing a known bug may affect is not held to agreement.
            [[ -n "${bug}" ]] && continue
            jq -cS "${NORMALIZE}" <<<"${doc}" > "${WORK}/agree/${n}/${client}__${server}"
        done
    done < "${CASES}"

    for client in "${ADAPTERS[@]}"; do
        pair="${client}->${server}"
        supported "${client}" "${server}" "-" || continue
        pair="${client}->${server}"
        supported "${client}" "${server}" "-" || continue
        name=$(shared_server "${server}")
        doc=$(run_client "${client}" integrity-poll --host "${name}")
        check "${pair}" "integrity poll returns the fixture" "${want_static}" "$(objects "${doc}")"
        got_flags=$(jq -cS '[.objects[] | select(.flags) | .flags] | sort' <<<"${doc}" 2>/dev/null)
        check "${pair}" "integrity poll returns the flags of the fixture" "${want_flags}" "${got_flags}"
        # The value checks below use the baseline's points.
        [[ "${FIXTURE_NAME}" == baseline ]] || continue

        # Controls, their events, once; the values a later poll reports.
        name=$(start_server "${server}" ${SERVER_ARGS[@]+"${SERVER_ARGS[@]}"}); wait_ready "${name}" || exit 1
        run_client "${client}" operate --type g12v1 --index 0 --code LATCH_ON --host "${name}" >/dev/null
        run_client "${client}" operate --type g41v1 --index 0 --value 42 --host "${name}" >/dev/null
        check "${pair}" "controls produce class 1 events" "g11v2:0=true g2v2:1=true g42v1:0=42" \
            "$(objects "$(run_client "${client}" event-poll --classes 1 --host "${name}")")"
        check "${pair}" "controls produce class 2 events" "g32v1:4=42" \
            "$(objects "$(run_client "${client}" event-poll --classes 2 --host "${name}")")"
        check "${pair}" "confirmed events are gone" "" \
            "$(objects "$(run_client "${client}" event-poll --host "${name}")")"
        check "${pair}" "outputs and targets keep the controlled values" "g10v2:0=true g1v2:1=true g30v1:4=42 g40v1:0=42" \
            "$(run_client "${client}" integrity-poll --host "${name}" | jq -r '[.objects[] | select((.group == 1 and .index == 1) or (.group == 10 and .index == 0) or (.group == 30 and .index == 4) or (.group == 40 and .index == 0)) | "g\(.group)v\(.variation):\(.index)=\(.value)"] | sort | join(" ")')"
        run_client "${client}" operate --type g12v1 --index 0 --code LATCH_ON --host "${name}" >/dev/null
        check "${pair}" "a control that changes nothing produces no event" "" \
            "$(objects "$(run_client "${client}" event-poll --host "${name}")")"
        stop_server "${name}"

        # Select-before-operate as the first request after the null unsolicited
        # response of the outstation was confirmed (see tests/known-bugs.tsv).
        name=$(start_server "${server}" ${SERVER_ARGS[@]+"${SERVER_ARGS[@]}"}); wait_ready "${name}" || exit 1
        run_client "${client}" read --group 1 --variation 0 --start 0 --stop 0 --collect-ms 500 --host "${name}" >/dev/null
        check "${pair}" "select-before-operate of a select-required output" "SUCCESS" \
            "$(run_client "${client}" operate --type g12v1 --index 1 --code LATCH_ON --mode sbo --host "${name}" | jq -r '.commandStatus')"
        check "${pair}" "select-before-operate of an analog output" "SUCCESS" \
            "$(run_client "${client}" operate --type g41v3 --index 1 --value 49.5 --mode sbo --host "${name}" | jq -r '.commandStatus')"
        check "${pair}" "the operated values are reported" "g10v2:1=true g40v3:1=49.5" \
            "$(run_client "${client}" integrity-poll --host "${name}" | jq -r '[.objects[] | select((.group == 10 and .index == 1) or (.group == 40 and .index == 1)) | "g\(.group)v\(.variation):\(.index)=\(.value)"] | sort | join(" ")')"
        stop_server "${name}"

        # A freeze without acknowledgement still freezes.
        if supported "${client}" "${server}" "server:freezeNoAckServer,client:freezeNoAckClient"; then
            name=$(start_server "${server}" ${SERVER_ARGS[@]+"${SERVER_ARGS[@]}"}); wait_ready "${name}" || exit 1
            run_client "${client}" freeze --no-ack --host "${name}" >/dev/null
            check "${pair}" "a freeze without acknowledgement copies the counters" "g21v1:0=123456 g21v2:1=65535" \
                "$(objects "$(run_client "${client}" read --group 21 --variation 0 --start 0 --stop 1 --host "${name}")")"
            stop_server "${name}"
        else
            skip "${pair}: freeze without acknowledgement"
        fi

        # Direct operate without acknowledgement still operates.
        if supported "${client}" "${server}" "server:directOperateNoAckServer,client:directOperateNoAckClient"; then
            name=$(start_server "${server}" ${SERVER_ARGS[@]+"${SERVER_ARGS[@]}"}); wait_ready "${name}" || exit 1
            run_client "${client}" operate --type g12v1 --index 0 --code LATCH_ON --mode direct-no-ack --host "${name}" >/dev/null
            run_client "${client}" operate --type g41v2 --index 0 --value 7 --mode direct-no-ack --host "${name}" >/dev/null
            check "${pair}" "direct operate no ack operates" "g10v2:0=true g40v1:0=7" \
                "$(run_client "${client}" integrity-poll --host "${name}" | jq -r '[.objects[] | select((.group == 10 and .index == 0) or (.group == 40 and .index == 0)) | "g\(.group)v\(.variation):\(.index)=\(.value)"] | sort | join(" ")')"
            stop_server "${name}"
        else
            skip "${pair}: direct operate no ack"
        fi

        # A restart is answered, also by a master that does not report the delay.
        name=$(start_server "${server}" ${SERVER_ARGS[@]+"${SERVER_ARGS[@]}"}); wait_ready "${name}" || exit 1
        check "${pair}" "a cold restart is accepted" "true" \
            "$(run_client "${client}" restart --type cold --host "${name}" | jq -r '.ok')"
        stop_server "${name}"

        # Freeze, then freeze and clear.
        name=$(start_server "${server}" ${SERVER_ARGS[@]+"${SERVER_ARGS[@]}"}); wait_ready "${name}" || exit 1
        run_client "${client}" freeze --host "${name}" >/dev/null
        check "${pair}" "a freeze copies the counters" "g21v1:0=123456 g21v2:1=65535" \
            "$(objects "$(run_client "${client}" read --group 21 --variation 0 --start 0 --stop 1 --host "${name}")")"
        check "${pair}" "a freeze produces frozen counter events" "g23v1:0=123456 g23v2:1=65535" \
            "$(objects "$(run_client "${client}" event-poll --classes 3 --host "${name}")")"
        run_client "${client}" freeze --clear --host "${name}" >/dev/null
        check "${pair}" "a freeze and clear clears the counters" "g20v1:0=0 g20v2:1=0 g20v5:2=0" \
            "$(objects "$(run_client "${client}" read --group 20 --variation 0 --start 0 --stop 2 --host "${name}")")" "freeze --clear"
        check "${pair}" "a clear produces counter events" "g22v1:0=0 g22v2:1=0" \
            "$(objects "$(run_client "${client}" event-poll --classes 3 --host "${name}")")" "freeze, unchanged frozen value"
        stop_server "${name}"

        # Unsolicited responses: buffered events arrive once a class is enabled.
        if supported "${client}" "${server}" "server:unsolicited,client:unsolicited"; then
            name=$(start_server "${server}" ${SERVER_ARGS[@]+"${SERVER_ARGS[@]}"}); wait_ready "${name}" || exit 1
            run_client "${client}" operate --type g12v1 --index 0 --code LATCH_ON --host "${name}" >/dev/null
            check "${pair}" "enabling class 1 delivers its events unsolicited" "g11v2u:0=true g2v2u:1=true" \
                "$(objects "$(run_client "${client}" enable-unsolicited --classes 1 --collect-ms 2000 --host "${name}")")"
            check "${pair}" "events delivered unsolicited are not polled again" "" \
                "$(objects "$(run_client "${client}" event-poll --classes 1 --host "${name}")")"
            stop_server "${name}"
        else
            skip "${pair}: unsolicited responses"
        fi

        # A class assignment moves the events of the binary outputs.
        if supported "${client}" "${server}" "server:assignClassServer,client:assignClassClient"; then
            name=$(start_server "${server}" ${SERVER_ARGS[@]+"${SERVER_ARGS[@]}"}); wait_ready "${name}" || exit 1
            run_client "${client}" assign-class --class 2 --group 10 --host "${name}" >/dev/null
            run_client "${client}" operate --type g12v1 --index 0 --code LATCH_ON --host "${name}" >/dev/null
            check "${pair}" "an assigned class takes the events" "g11v2:0=true" \
                "$(objects "$(run_client "${client}" event-poll --classes 2 --host "${name}")")"
            check "${pair}" "the old class has them no more" "g2v2:1=true" \
                "$(objects "$(run_client "${client}" event-poll --classes 1 --host "${name}")")"
            stop_server "${name}"
        else
            skip "${pair}: class assignment"
        fi

        # Time synchronization clears NEED_TIME, by either procedure.
        for procedure in lan non-lan; do
            name=$(start_server "${server}" ${SERVER_ARGS[@]+"${SERVER_ARGS[@]}"}); wait_ready "${name}" || exit 1
            run_client "${client}" time-sync --procedure "${procedure}" --host "${name}" >/dev/null
            got=$(run_client "${client}" read --group 1 --variation 0 --start 0 --stop 0 --host "${name}" | jq -c '.iin - ["DEVICE_RESTART"]')
            check "${pair}" "time-sync ${procedure} clears NEED_TIME" "[]" "${got}"
            stop_server "${name}"
        done
    done
    echo "${PASS} ${FAIL} ${SKIPPED} ${KNOWN}" > "${WORK}/tally-${server}"
}

log "adapters: ${ADAPTERS[*]} (${VERSION})"
[[ -n "${REPORT_DIR:-}" ]] && mkdir -p "${REPORT_DIR}"
servers=()
for adapter in "${ADAPTERS[@]}"; do
    serves "${adapter}" && servers+=("${adapter}")
done
log "fixture ${FIXTURE_NAME}: servers ${servers[*]}"
for server in "${servers[@]}"; do
    suite "${server}" &
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

[[ -n "${REPORT_DIR:-}" ]] && { RESULTS_FILE="${REPORT_DIR}/results-interop${SUFFIX}-agreement.tsv"; : > "${RESULTS_FILE}"; }

# ---- every pairing that met a case's expectation returned the same document ----
n=0
while IFS=$'\t' read -r state _ _ _ operation; do
    [[ -z "${state}" || "${state}" == \#* ]] && continue
    n=$((n + 1))
    reference="" reference_pair=""
    for server in "${servers[@]}"; do
        for client in "${ADAPTERS[@]}"; do
            file="${WORK}/agree/${n}/${client}__${server}"
            [[ -f "${file}" ]] || continue
            pair="${client}->${server}"
            normalized=$(cat "${file}")
            if [[ -z "${reference}" ]]; then
                reference="${normalized}"; reference_pair="${pair}"
            elif [[ "${normalized}" != "${reference}" ]]; then
                fail "${pair} and ${reference_pair} disagree on: ${operation}"
                diff <(jq -S . <<<"${reference}") <(jq -S . <<<"${normalized}") | head -20 >&2
            else
                ok "${pair} agrees with ${reference_pair}: ${operation}"
            fi
        done
    done
done < "${CASES}"

# ---- a known bug that no longer shows is a stale entry ----
while IFS=$'\t' read -r bug_adapter bug_regex bug_text; do
    if ! grep -qxF "${bug_text}" "${WORK}/reproduced"; then
        fail "known bug of ${bug_adapter} did not reproduce: ${bug_text}"
        log "    remove it from tests/known-bugs.tsv; the pin can move to this version"
    fi
done < "${WORK}/active"

finish
