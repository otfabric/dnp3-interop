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
#   - a freeze copies the counters, a freeze-and-clear also clears them, and
#     a freeze of unchanged counters keeps the frozen values; whether it
#     raises frozen counter events is the outstation's policy (either is
#     accepted, consistently, the one it declares, and the events it does
#     raise are checked, docs/COMPATIBILITY.md),
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
# Every outcome goes to results.json (interop-result-v1): in REPORT_DIR, with
# those of the other self-tests, when it is set. A run with no pass, or with a
# known bug that no longer reproduces, fails.
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
SERVER_ARGS=() SUFFIX="" ID_PREFIX=""
CASES="${ROOT}/tests/cases.tsv"
if [[ "${FIXTURE_NAME}" != baseline ]]; then
    SERVER_ARGS=(--fixture "/fixtures/${FIXTURE_NAME}/fixture.json")
    SUFFIX="-${FIXTURE_NAME}"
    ID_PREFIX="${FIXTURE_NAME}."
    CASES="${ROOT}/tests/cases-${FIXTURE_NAME}.tsv"
fi
KNOWN_BUGS="${ROOT}/tests/known-bugs.tsv"
require_tools
WORK=$(mktemp -d)
trap 'results_abort; rm -rf "${WORK}"; cleanup' EXIT
results_open "interop${SUFFIX}" "${REPORT_DIR:-${WORK}}" "VERSION=${VERSION} FIXTURE=${FIXTURE_NAME} scripts/interop.sh $*"
network_up

# ---- known upstream bugs that apply to the versions under test ----
# ${WORK}/active: "<adapter>\t<operation regex>\t<description>\t<waiver id>\t<evidence>" per line.
: > "${WORK}/active"
: > "${WORK}/reproduced"
for adapter in "${ADAPTERS[@]}"; do
    docker run --rm "$(image_of "${adapter}")" print-capabilities > "${WORK}/caps-${adapter}.json" 2>/dev/null
    version=$(jq -r '.upstream.version' "${WORK}/caps-${adapter}.json")
    log "${adapter}: upstream ${version}"
    while IFS=$'\t' read -r bug_adapter bug_version bug_regex bug_text bug_fixture bug_id bug_url; do
        [[ -z "${bug_adapter}" || "${bug_adapter}" == \#* ]] && continue
        [[ "${bug_fixture:-baseline}" == "${FIXTURE_NAME}" ]] || continue
        if [[ "${bug_adapter%%:*}" == "${adapter}" && "${bug_version}" == "${version}" ]]; then
            printf '%s\t%s\t%s\t%s\t%s\n' "${bug_adapter}" "${bug_regex}" "${bug_text}" "${bug_id}" "${bug_url}" >> "${WORK}/active"
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
skip() { SKIPPED=$((SKIPPED + 1)); record skip "$*"; result unsupported "$*"; [[ -n "${VERBOSE:-}" ]] && log "skip: $*"; return 0; }

# known_bug <client> <server> <operation>: prints the description of a known
# bug that may affect this pairing and operation, if there is one.
known_bug() {
    local bug_adapter bug_regex bug_text
    while IFS=$'\t' read -r bug_adapter bug_regex bug_text _; do
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
    result known-difference "$1: $2: $3" "$(waiver_id "$3")"
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

# only_group <document> <group>, without_group <document> <group>: the
# document with the objects of that group alone, or with all the others.
only_group() { jq -c --argjson g "$2" '.objects = [(.objects // [])[] | select(.group == $g)]' <<<"$1" 2>/dev/null; }
without_group() { jq -c --argjson g "$2" '.objects = [(.objects // [])[] | select(.group != $g)]' <<<"$1" 2>/dev/null; }

# freeze_event_faults <document>: the counter and frozen counter events whose
# flags or time stamp are wrong. The baseline's points are ONLINE, and only
# variations 5 and 6 of groups 22 and 23 carry a time.
freeze_event_faults() {
    jq -r '[.objects[]? | select(.group == 22 or .group == 23)
        | select(.flags != ["ONLINE"] or
            (if .variation == 5 or .variation == 6
             then (.time // "" | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:.]+Z$") | not)
             else has("time") end))
        | "g\(.group)v\(.variation):\(.index) flags=\(.flags) time=\(.time)"] | join(" ")' <<<"$1" 2>/dev/null
}

# frozen_event_policy <pair> <label> <events of every frozen counter> <got>:
# a freeze that left the frozen values as they were. Raising an event for each
# of them and raising none are both accepted, and only those; the policy goes
# to FROZEN_POLICY for the checks that it holds. Nothing here treats an event
# as a duplicate for repeating the value of an earlier one.
frozen_event_policy() {
    if [[ -z "$4" ]]; then
        ok "$1: $2 (no events)"
        FROZEN_POLICY="on change"
    else
        check "$1" "$2" "$3" "$4"
        FROZEN_POLICY="every freeze"
    fi
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
    local server="$1" n=0 state want_exit want_summary needs operation client pair name doc summary bug got got_flags procedure frozen_events first_policy server_policy=""
    PASS=0 FAIL=0 SKIPPED=0 KNOWN=0
    if [[ -n "${REPORT_DIR:-}" ]]; then RESULTS_FILE="${REPORT_DIR}/results-interop${SUFFIX}-${server}.tsv"; : > "${RESULTS_FILE}"; fi
    while IFS=$'\t' read -r case_id state want_exit want_summary needs operation; do
        [[ -z "${case_id}" || "${case_id}" == \#* ]] && continue
        n=$((n + 1))
        mkdir -p "${WORK}/agree/${n}"
        for client in "${ADAPTERS[@]}"; do
            pair="${client}->${server}"
            at "${case_id}" "${client}" "${server}"
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
        at_pair "${ID_PREFIX}values.integrity-poll-returns-the-fixture" "${pair}"
        check "${pair}" "integrity poll returns the fixture" "${want_static}" "$(objects "${doc}")"
        got_flags=$(jq -cS '[.objects[] | select(.flags) | .flags] | sort' <<<"${doc}" 2>/dev/null)
        at_pair "${ID_PREFIX}values.integrity-poll-returns-the-flags-of-the-fixture" "${pair}"
        check "${pair}" "integrity poll returns the flags of the fixture" "${want_flags}" "${got_flags}"
        # The value checks below use the baseline's points.
        [[ "${FIXTURE_NAME}" == baseline ]] || continue

        # Controls, their events, once; the values a later poll reports.
        name=$(start_server "${server}" ${SERVER_ARGS[@]+"${SERVER_ARGS[@]}"}); wait_ready "${name}" || exit 1
        run_client "${client}" operate --type g12v1 --index 0 --code LATCH_ON --host "${name}" >/dev/null
        run_client "${client}" operate --type g41v1 --index 0 --value 42 --host "${name}" >/dev/null
        at_pair "values.controls-produce-class-1-events" "${pair}"
        check "${pair}" "controls produce class 1 events" "g11v2:0=true g2v2:1=true g42v1:0=42" \
            "$(objects "$(run_client "${client}" event-poll --classes 1 --host "${name}")")"
        at_pair "values.controls-produce-class-2-events" "${pair}"
        check "${pair}" "controls produce class 2 events" "g32v1:4=42" \
            "$(objects "$(run_client "${client}" event-poll --classes 2 --host "${name}")")"
        at_pair "values.confirmed-events-are-gone" "${pair}"
        check "${pair}" "confirmed events are gone" "" \
            "$(objects "$(run_client "${client}" event-poll --host "${name}")")"
        at_pair "values.outputs-and-targets-keep-the-controlled-values" "${pair}"
        check "${pair}" "outputs and targets keep the controlled values" "g10v2:0=true g1v2:1=true g30v1:4=42 g40v1:0=42" \
            "$(run_client "${client}" integrity-poll --host "${name}" | jq -r '[.objects[] | select((.group == 1 and .index == 1) or (.group == 10 and .index == 0) or (.group == 30 and .index == 4) or (.group == 40 and .index == 0)) | "g\(.group)v\(.variation):\(.index)=\(.value)"] | sort | join(" ")')"
        run_client "${client}" operate --type g12v1 --index 0 --code LATCH_ON --host "${name}" >/dev/null
        at_pair "values.control-that-changes-nothing-produces-no-event" "${pair}"
        check "${pair}" "a control that changes nothing produces no event" "" \
            "$(objects "$(run_client "${client}" event-poll --host "${name}")")"
        stop_server "${name}"

        # Select-before-operate as the first request after the null unsolicited
        # response of the outstation was confirmed (see tests/known-bugs.tsv).
        name=$(start_server "${server}" ${SERVER_ARGS[@]+"${SERVER_ARGS[@]}"}); wait_ready "${name}" || exit 1
        run_client "${client}" read --group 1 --variation 0 --start 0 --stop 0 --collect-ms 500 --host "${name}" >/dev/null
        at_pair "values.select-before-operate-of-a-select-required-output" "${pair}"
        check "${pair}" "select-before-operate of a select-required output" "SUCCESS" \
            "$(run_client "${client}" operate --type g12v1 --index 1 --code LATCH_ON --mode sbo --host "${name}" | jq -r '.commandStatus')"
        at_pair "values.select-before-operate-of-an-analog-output" "${pair}"
        check "${pair}" "select-before-operate of an analog output" "SUCCESS" \
            "$(run_client "${client}" operate --type g41v3 --index 1 --value 49.5 --mode sbo --host "${name}" | jq -r '.commandStatus')"
        at_pair "values.operated-values-are-reported" "${pair}"
        check "${pair}" "the operated values are reported" "g10v2:1=true g40v3:1=49.5" \
            "$(run_client "${client}" integrity-poll --host "${name}" | jq -r '[.objects[] | select((.group == 10 and .index == 1) or (.group == 40 and .index == 1)) | "g\(.group)v\(.variation):\(.index)=\(.value)"] | sort | join(" ")')"
        stop_server "${name}"

        # A freeze without acknowledgement still freezes.
        if supported "${client}" "${server}" "server:freezeNoAckServer,client:freezeNoAckClient"; then
            name=$(start_server "${server}" ${SERVER_ARGS[@]+"${SERVER_ARGS[@]}"}); wait_ready "${name}" || exit 1
            run_client "${client}" freeze --no-ack --host "${name}" >/dev/null
            at_pair "values.freeze-without-acknowledgement-copies-the-counters" "${pair}"
            check "${pair}" "a freeze without acknowledgement copies the counters" "g21v1:0=123456 g21v2:1=65535" \
                "$(objects "$(run_client "${client}" read --group 21 --variation 0 --start 0 --stop 1 --host "${name}")")"
            stop_server "${name}"
        else
            at_pair "skipped.freeze-without-acknowledgement" "${pair}"
            skip "${pair}: freeze without acknowledgement"
        fi

        # Direct operate without acknowledgement still operates.
        if supported "${client}" "${server}" "server:directOperateNoAckServer,client:directOperateNoAckClient"; then
            name=$(start_server "${server}" ${SERVER_ARGS[@]+"${SERVER_ARGS[@]}"}); wait_ready "${name}" || exit 1
            run_client "${client}" operate --type g12v1 --index 0 --code LATCH_ON --mode direct-no-ack --host "${name}" >/dev/null
            run_client "${client}" operate --type g41v2 --index 0 --value 7 --mode direct-no-ack --host "${name}" >/dev/null
            at_pair "values.direct-operate-no-ack-operates" "${pair}"
            check "${pair}" "direct operate no ack operates" "g10v2:0=true g40v1:0=7" \
                "$(run_client "${client}" integrity-poll --host "${name}" | jq -r '[.objects[] | select((.group == 10 and .index == 0) or (.group == 40 and .index == 0)) | "g\(.group)v\(.variation):\(.index)=\(.value)"] | sort | join(" ")')"
            stop_server "${name}"
        else
            at_pair "skipped.direct-operate-no-ack" "${pair}"
            skip "${pair}: direct operate no ack"
        fi

        # A restart is answered, also by a master that does not report the delay.
        name=$(start_server "${server}" ${SERVER_ARGS[@]+"${SERVER_ARGS[@]}"}); wait_ready "${name}" || exit 1
        at_pair "values.cold-restart-is-accepted" "${pair}"
        check "${pair}" "a cold restart is accepted" "true" \
            "$(run_client "${client}" restart --type cold --host "${name}" | jq -r '.ok')"
        stop_server "${name}"

        # Freeze, the same freeze again, freeze and clear, and a freeze of the
        # counters that changed. What a freeze does to the values is checked
        # hard; only whether a freeze that changes no frozen value raises
        # events is left to the outstation (docs/COMPATIBILITY.md).
        frozen_events="g23v1:0=123456 g23v2:1=65535"
        name=$(start_server "${server}" ${SERVER_ARGS[@]+"${SERVER_ARGS[@]}"}); wait_ready "${name}" || exit 1
        run_client "${client}" freeze --host "${name}" >/dev/null
        at_pair "values.freeze-copies-the-counters" "${pair}"
        check "${pair}" "a freeze copies the counters" "g21v1:0=123456 g21v2:1=65535" \
            "$(objects "$(run_client "${client}" read --group 21 --variation 0 --start 0 --stop 1 --host "${name}")")"
        doc=$(run_client "${client}" event-poll --classes 3 --host "${name}")
        at_pair "values.freeze-produces-frozen-counter-events" "${pair}"
        check "${pair}" "a freeze produces frozen counter events" "${frozen_events}" "$(objects "${doc}")"
        at_pair "values.frozen-counter-events-have-the-right-flags-and-time" "${pair}"
        check "${pair}" "frozen counter events have the right flags and time" "" "$(freeze_event_faults "${doc}")"

        run_client "${client}" freeze --host "${name}" >/dev/null
        at_pair "values.second-freeze-keeps-the-frozen-values" "${pair}"
        check "${pair}" "a second freeze keeps the frozen values" "g21v1:0=123456 g21v2:1=65535" \
            "$(objects "$(run_client "${client}" read --group 21 --variation 0 --start 0 --stop 1 --host "${name}")")"
        at_pair "values.freeze-leaves-the-counters" "${pair}"
        check "${pair}" "a freeze leaves the counters" "g20v1:0=123456 g20v2:1=65535 g20v5:2=4294967295" \
            "$(objects "$(run_client "${client}" read --group 20 --variation 0 --start 0 --stop 2 --host "${name}")")"
        doc=$(run_client "${client}" event-poll --classes 3 --host "${name}")
        at_pair "values.second-freeze-event-policy" "${pair}"
        frozen_event_policy "${pair}" "a second freeze raises events for every frozen value or none" "${frozen_events}" "$(objects "${doc}")"
        first_policy="${FROZEN_POLICY}"
        at_pair "values.second-freeze-events-have-the-right-flags-and-time" "${pair}"
        check "${pair}" "second freeze events have the right flags and time" "" "$(freeze_event_faults "${doc}")"
        at_pair "values.confirmed-frozen-counter-events-are-gone" "${pair}"
        check "${pair}" "confirmed frozen counter events are gone" "" \
            "$(objects "$(run_client "${client}" event-poll --classes 3 --host "${name}")")"

        run_client "${client}" freeze --clear --host "${name}" >/dev/null
        at_pair "values.freeze-and-clear-clears-the-counters" "${pair}"
        check "${pair}" "a freeze and clear clears the counters" "g20v1:0=0 g20v2:1=0 g20v5:2=0" \
            "$(objects "$(run_client "${client}" read --group 20 --variation 0 --start 0 --stop 2 --host "${name}")")" "freeze --clear"
        at_pair "values.freeze-and-clear-freezes-before-it-clears" "${pair}"
        check "${pair}" "a freeze and clear freezes before it clears" "g21v1:0=123456 g21v2:1=65535" \
            "$(objects "$(run_client "${client}" read --group 21 --variation 0 --start 0 --stop 1 --host "${name}")")"
        doc=$(run_client "${client}" event-poll --classes 3 --host "${name}")
        at_pair "values.clear-produces-counter-events" "${pair}"
        check "${pair}" "a clear produces counter events" "g22v1:0=0 g22v2:1=0" "$(objects "$(without_group "${doc}" 23)")"
        at_pair "values.freeze-and-clear-event-policy" "${pair}"
        frozen_event_policy "${pair}" "a freeze and clear raises events for every frozen value or none" "${frozen_events}" "$(objects "$(only_group "${doc}" 23)")"
        at_pair "values.one-frozen-event-policy-across-freezes" "${pair}"
        check "${pair}" "one frozen event policy across freezes" "${first_policy}" "${FROZEN_POLICY}"
        # An outstation with one policy declares it; contract 1.4. One that can
        # be set runs its default here, the first it declares.
        at_pair "values.frozen-event-policy-as-declared" "${pair}"
        check "${pair}" "the frozen event policy is the one declared" \
            "$(jq -r '.frozenEventPolicies[0] // "none" | if . == "always" then "every freeze" else "on change" end' "${WORK}/caps-${server}.json")" "${first_policy}"
        at_pair "values.one-frozen-event-policy-across-masters" "${pair}"
        check "${pair}" "one frozen event policy across masters" "${server_policy:-${first_policy}}" "${first_policy}"
        server_policy="${first_policy}"
        at_pair "values.clear-events-have-the-right-flags-and-time" "${pair}"
        check "${pair}" "clear events have the right flags and time" "" "$(freeze_event_faults "${doc}")"

        # The clear changed the counters: the next freeze must carry that over,
        # and an event is due whatever the policy, the frozen values changed.
        run_client "${client}" freeze --host "${name}" >/dev/null
        at_pair "values.freeze-after-a-counter-change-updates-the-frozen-values" "${pair}"
        check "${pair}" "a freeze after a counter change updates the frozen values" "g21v1:0=0 g21v2:1=0" \
            "$(objects "$(run_client "${client}" read --group 21 --variation 0 --start 0 --stop 1 --host "${name}")")"
        doc=$(run_client "${client}" event-poll --classes 3 --host "${name}")
        at_pair "values.freeze-after-a-counter-change-produces-frozen-counter-events" "${pair}"
        check "${pair}" "a freeze after a counter change produces frozen counter events" "g23v1:0=0 g23v2:1=0" "$(objects "${doc}")"
        at_pair "values.changed-frozen-counter-events-have-the-right-flags-and-time" "${pair}"
        check "${pair}" "changed frozen counter events have the right flags and time" "" "$(freeze_event_faults "${doc}")"
        stop_server "${name}"

        # Unsolicited responses: buffered events arrive once a class is enabled.
        if supported "${client}" "${server}" "server:unsolicited,client:unsolicited"; then
            name=$(start_server "${server}" ${SERVER_ARGS[@]+"${SERVER_ARGS[@]}"}); wait_ready "${name}" || exit 1
            run_client "${client}" operate --type g12v1 --index 0 --code LATCH_ON --host "${name}" >/dev/null
            at_pair "values.enabling-class-1-delivers-its-events-unsolicited" "${pair}"
            check "${pair}" "enabling class 1 delivers its events unsolicited" "g11v2u:0=true g2v2u:1=true" \
                "$(objects "$(run_client "${client}" enable-unsolicited --classes 1 --collect-ms 2000 --host "${name}")")"
            at_pair "values.events-delivered-unsolicited-are-not-polled-again" "${pair}"
            check "${pair}" "events delivered unsolicited are not polled again" "" \
                "$(objects "$(run_client "${client}" event-poll --classes 1 --host "${name}")")"
            stop_server "${name}"
        else
            at_pair "skipped.unsolicited-responses" "${pair}"
            skip "${pair}: unsolicited responses"
        fi

        # A class assignment moves the events of the binary outputs.
        if supported "${client}" "${server}" "server:assignClassServer,client:assignClassClient"; then
            name=$(start_server "${server}" ${SERVER_ARGS[@]+"${SERVER_ARGS[@]}"}); wait_ready "${name}" || exit 1
            run_client "${client}" assign-class --class 2 --group 10 --host "${name}" >/dev/null
            run_client "${client}" operate --type g12v1 --index 0 --code LATCH_ON --host "${name}" >/dev/null
            at_pair "values.assigned-class-takes-the-events" "${pair}"
            check "${pair}" "an assigned class takes the events" "g11v2:0=true" \
                "$(objects "$(run_client "${client}" event-poll --classes 2 --host "${name}")")"
            at_pair "values.old-class-has-them-no-more" "${pair}"
            check "${pair}" "the old class has them no more" "g2v2:1=true" \
                "$(objects "$(run_client "${client}" event-poll --classes 1 --host "${name}")")"
            stop_server "${name}"
        else
            at_pair "skipped.class-assignment" "${pair}"
            skip "${pair}: class assignment"
        fi

        # Time synchronization clears NEED_TIME, by either procedure.
        for procedure in lan non-lan; do
            name=$(start_server "${server}" ${SERVER_ARGS[@]+"${SERVER_ARGS[@]}"}); wait_ready "${name}" || exit 1
            run_client "${client}" time-sync --procedure "${procedure}" --host "${name}" >/dev/null
            got=$(run_client "${client}" read --group 1 --variation 0 --start 0 --stop 0 --host "${name}" | jq -c '.iin - ["DEVICE_RESTART"]')
            at_pair "values.time-sync-${procedure}-clears-need-time" "${pair}"
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
while IFS=$'\t' read -r case_id _ _ _ _ operation; do
    [[ -z "${case_id}" || "${case_id}" == \#* ]] && continue
    n=$((n + 1))
    reference="" reference_pair=""
    for server in "${servers[@]}"; do
        for client in "${ADAPTERS[@]}"; do
            file="${WORK}/agree/${n}/${client}__${server}"
            [[ -f "${file}" ]] || continue
            pair="${client}->${server}"
            at "${case_id}.agrees" "${client}" "${server}"
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
while IFS=$'\t' read -r bug_adapter _ bug_text bug_id bug_url; do
    if grep -qxF "${bug_text}" "${WORK}/reproduced"; then
        waiver "${bug_id}" "${bug_adapter}" "${bug_url}" true
    else
        waiver "${bug_id}" "${bug_adapter}" "${bug_url}" false
        at waiver.stale
        fail "known bug of ${bug_adapter} did not reproduce: ${bug_text}"
        log "    remove it from tests/known-bugs.tsv; the pin can move to this version"
    fi
done < "${WORK}/active"

finish
