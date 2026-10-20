#!/usr/bin/env bash
# Container-contract smoke test for one adapter image.
#
# Usage: smoke.sh <adapter>          e.g. smoke.sh opendnp3
#
# Checks the parts of docs/CONTAINER_CONTRACT.md that do not need a second
# adapter: the command surface, capabilities, the baked fixture, readiness,
# the server event stream, exit codes and a clean stop on SIGTERM.
#
# An adapter without the server role is checked against the server of
# SMOKE_SERVER (default opendnp3), and its "server" command must exit 2.
#
# Environment: REGISTRY, VERSION (image tag, default dev), VERBOSE=1,
#              REPORT_DIR (keeps the capability document when set),
#              SMOKE_SERVER.

set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
# shellcheck source=scripts/lib.sh
source "${SCRIPT_DIR}/lib.sh"

ADAPTER="${1:?usage: smoke.sh <adapter>}"
IMAGE="$(image_of "${ADAPTER}")"
require_tools
trap cleanup EXIT
network_up
log "${IMAGE}"

expect_exit() {  # expect_exit <code> <label> <docker run args...>
    local want="$1" label="$2"; shift 2
    docker run --rm --network "${NETWORK}" --label "dnp3-interop.run=${RUN_ID}" "$@" >/dev/null 2>&1
    local got=$?
    if [[ "${got}" == "${want}" ]]; then ok "${label}: exit ${got}"; else fail "${label}: exit ${got}, want ${want}"; fi
}

# ---- capabilities ----
caps=$(docker run --rm "${IMAGE}" print-capabilities 2>/dev/null)
if [[ -n "${REPORT_DIR:-}" ]]; then
    mkdir -p "${REPORT_DIR}"
    RESULTS_FILE="${REPORT_DIR}/results-smoke-${ADAPTER}.tsv"
    : > "${RESULTS_FILE}"
    printf '%s\n' "${caps}" > "${REPORT_DIR}/capabilities-${ADAPTER}.json"
fi
if jq -e --arg a "${ADAPTER}" '
        .schemaVersion == "1.0" and .adapter == $a and .protocol == "dnp3"
        and (.adapterVersion | type == "string") and (.upstream.name | type == "string")
        and (.upstream.version | type == "string") and (.upstream.license | type == "string")
        and (.upstream.independentEngine | type == "boolean")
        and (.roles.server | type == "boolean") and (.roles.client | type == "boolean")
        and (.features | type == "object") and ([.features[] | type == "boolean"] | all)
        and (.pointTypes | type == "array") and (.commandTypes | type == "array")
        and (.clientOperations | type == "array")' <<<"${caps}" >/dev/null 2>&1
then ok "print-capabilities"; else fail "print-capabilities is not a valid capability document: ${caps}"; fi
if [[ $(wc -l <<<"${caps}") -eq 1 ]]; then ok "print-capabilities is one line"; else fail "print-capabilities printed more than one line"; fi
serves=false
jq -e '.roles.server == true' <<<"${caps}" >/dev/null 2>&1 && serves=true
# The fixture the adapter's own server serves: the baseline, or (dnp3py) the
# dnp3py fixture when its point types do not cover the baseline's.
FIXTURE=baseline
if [[ "${serves}" == true ]] && ! jq -e '.pointTypes | index("doubleBitInputs") and index("octetStrings")' \
        <<<"${caps}" >/dev/null 2>&1; then
    FIXTURE=dnp3py
fi
FIXTURE_ARGS=(--fixture "/fixtures/${FIXTURE}/fixture.json")
SERVER_ADAPTER="${ADAPTER}"
if [[ "${serves}" == false ]]; then
    SERVER_ADAPTER="${SMOKE_SERVER:-opendnp3}"
    log "no server role: client checks against $(image_of "${SERVER_ADAPTER}")"
fi
version=$(docker run --rm "${IMAGE}" version 2>/dev/null)
if [[ "${version}" == "dnp3-interop ${ADAPTER} "* ]]; then ok "version"; else fail "version printed: ${version}"; fi

# ---- the baked fixture is the repository's ----
for fixture in baseline dnp3py; do
    if diff -q <(docker run --rm "${IMAGE}" print-fixture "${fixture}" 2>/dev/null) "${ROOT}/fixtures/${fixture}/fixture.json" >/dev/null
    then ok "print-fixture ${fixture}"; else fail "print-fixture ${fixture} differs from fixtures/${fixture}/fixture.json"; fi
done
expect_exit 1 "print-fixture of an unknown name" "${IMAGE}" print-fixture no-such-fixture

# ---- usage errors never touch the network ----
# (Without a command the image runs "server": that is its default.)
expect_exit 2 "unknown command" "${IMAGE}" frobnicate
expect_exit 2 "client without --host" "${IMAGE}" client integrity-poll
expect_exit 2 "client with an unknown operation" "${IMAGE}" client frobnicate --host nowhere
expect_exit 2 "client with an unknown flag" "${IMAGE}" client integrity-poll --host nowhere --frobnicate 1
expect_exit 2 "operate without --index" "${IMAGE}" client operate --host nowhere --type g12v1 --code LATCH_ON
expect_exit 2 "operate with a bad code" "${IMAGE}" client operate --host nowhere --type g12v1 --index 0 --code ON
expect_exit 2 "operate with a value out of range" "${IMAGE}" client operate --host nowhere --type g41v2 --index 0 --value 99999
expect_exit 2 "event-poll of class 0" "${IMAGE}" client event-poll --host nowhere --classes 0
expect_exit 2 "a link address out of range" "${IMAGE}" client integrity-poll --host nowhere --remote-addr 65520
expect_exit 2 "server with an unknown flag" "${IMAGE}" server --frobnicate
expect_exit 2 "server with a link address out of range" "${IMAGE}" server --local-addr 70000

# ---- contract 1.1: flags that are wrong whatever the image has ----
expect_exit 2 "an unknown transport" "${IMAGE}" client integrity-poll --host nowhere --transport carrier-pigeon
expect_exit 2 "client --tls without certificates" "${IMAGE}" client integrity-poll --host nowhere --tls
expect_exit 2 "server --tls-cert without key and CA" "${IMAGE}" server --tls-cert /nowhere.pem
expect_exit 2 "TLS over UDP" "${IMAGE}" client integrity-poll --host nowhere --transport udp --tls \
    --tls-ca /a --tls-cert /b --tls-key /c
expect_exit 2 "serial without a device" "${IMAGE}" client integrity-poll --transport serial
expect_exit 2 "a file operation without --name" "${IMAGE}" client file-get --host nowhere
expect_exit 2 "--connect with --port" "${IMAGE}" server --connect master:20000 --port 20001
expect_exit 2 "--listen with --host" "${IMAGE}" client integrity-poll --listen --host nowhere
expect_exit 2 "an update key of the wrong length" "${IMAGE}" client integrity-poll --host nowhere --sa-update-key 0011
if ! jq -e '.features.secureAuthentication' <<<"${caps}" >/dev/null 2>&1; then
    expect_exit 2 "secure authentication without the feature" "${IMAGE}" client integrity-poll --host nowhere \
        --sa-update-key 000102030405060708090a0b0c0d0e0f
fi
if ! jq -e '.features.fileClient' <<<"${caps}" >/dev/null 2>&1; then
    expect_exit 2 "a file operation without the feature" "${IMAGE}" client file-get --host nowhere --name /a
fi
for feature in udp serial; do
    if ! jq -e --arg f "${feature}" '.features[$f]' <<<"${caps}" >/dev/null 2>&1; then
        expect_exit 2 "--transport ${feature} without the feature" "${IMAGE}" client integrity-poll --host nowhere \
            --transport "${feature}"
    fi
done
for fixture in files extended; do
    if diff -q <(docker run --rm "${IMAGE}" print-fixture "${fixture}" 2>/dev/null) "${ROOT}/fixtures/${fixture}/fixture.json" >/dev/null
    then ok "print-fixture ${fixture}"; else fail "print-fixture ${fixture} differs from fixtures/${fixture}/fixture.json"; fi
done

if [[ "${serves}" == true ]]; then
    # ---- an invalid fixture stops the server before it listens ----
    bad=$(mktemp -d)
    trap 'rm -rf "${bad}"; cleanup' EXIT
    chmod 755 "${bad}"
    jq '.binaryOutputs[0].target = 9' "${ROOT}/fixtures/${FIXTURE}/fixture.json" > "${bad}/fixture.json"
    chmod 644 "${bad}/fixture.json"
    expect_exit 4 "server with an invalid fixture" -v "${bad}:/bad:ro" "${IMAGE}" server --fixture /bad/fixture.json
    expect_exit 4 "server with a missing fixture" "${IMAGE}" server --fixture /no/such/fixture.json
    # A fixture with files needs the feature fileServer (contract 1.1).
    if jq -e '.features.fileServer' <<<"${caps}" >/dev/null 2>&1; then
        name=$(start_server "${ADAPTER}" --fixture /fixtures/files/fixture.json)
        if wait_ready "${name}"; then ok "server serves the files fixture"; else fail "server does not serve the files fixture"; fi
        stop_server "${name}"
    else
        expect_exit 4 "server without file transfer refuses the files fixture" "${IMAGE}" server \
            --fixture /fixtures/files/fixture.json
    fi
    # The extended fixture needs frozen analogs, g50v4 and command events (1.2).
    if jq -e '.features.commandEventsServer and (.pointTypes | index("frozenAnalogInputs") and index("timeAndIntervals"))' \
            <<<"${caps}" >/dev/null 2>&1; then
        name=$(start_server "${ADAPTER}" --fixture /fixtures/extended/fixture.json)
        if wait_ready "${name}"; then ok "server serves the extended fixture"; else fail "server does not serve the extended fixture"; fi
        stop_server "${name}"
    else
        expect_exit 4 "server refuses the extended fixture" "${IMAGE}" server --fixture /fixtures/extended/fixture.json
    fi
else
    expect_exit 2 "server of an adapter without the server role" "${IMAGE}" server
fi

# ---- a connection that cannot be made ----
doc=$(run_client "${ADAPTER}" integrity-poll --host "no-such-host-${RUN_ID}" --connect-timeout-ms 1500); CLIENT_EXIT=$?
if [[ "${CLIENT_EXIT}" == 3 ]] && jq -e '.ok == false and .error.code == "connect-failed" and .connected == false' <<<"${doc}" >/dev/null 2>&1
then ok "connect failure: exit 3 and a result document"; else fail "connect failure: exit ${CLIENT_EXIT}, ${doc}"; fi

# ---- server: readiness, events, operations, clean stop ----
SERVER_FIXTURE_ARGS=()
[[ "${SERVER_ADAPTER}" == "${ADAPTER}" ]] && SERVER_FIXTURE_ARGS=("${FIXTURE_ARGS[@]}")
SERVER_FIXTURE=baseline
[[ "${SERVER_ADAPTER}" == "${ADAPTER}" ]] && SERVER_FIXTURE="${FIXTURE}"
name=$(start_server "${SERVER_ADAPTER}" "${SERVER_FIXTURE_ARGS[@]}")
if wait_ready "${name}"; then ok "server becomes ready"; else fail "server did not become ready"; finish; exit 1; fi
first=$(docker logs "${name}" 2>/dev/null | head -1)
if jq -e --arg a "${SERVER_ADAPTER}" --arg f "${SERVER_FIXTURE}" '.event == "ready" and .adapter == $a and .fixture == $f
        and .linkAddress == 1024 and .masterAddress == 1 and (.address | type == "string")' <<<"${first}" >/dev/null 2>&1
then ok "ready event"; else fail "first stdout line is not the ready event: ${first}"; fi

doc=$(run_client "${ADAPTER}" integrity-poll --host "${name}"); CLIENT_EXIT=$?
if [[ "${CLIENT_EXIT}" == 0 ]] && jq -e --arg a "${ADAPTER}" '
        .schemaVersion == "1.0" and .adapter == $a and .operation == "integrity-poll" and .ok == true
        and .error == null and .connected and (.iin | type == "array") and (.iin | index("NEED_TIME") != null)
        and (.elapsedMs | type == "number") and (.objects | length) > 0
        and ([.objects[] | (.group, .variation, .index, .value, .unsolicited) != null] | all)' <<<"${doc}" >/dev/null 2>&1
then ok "client result document"; else fail "integrity-poll: exit ${CLIENT_EXIT}, ${doc}"; fi
if [[ $(wc -l <<<"${doc}") -eq 1 ]]; then ok "client result is one line"; else fail "client printed more than one line on stdout"; fi

run_client "${ADAPTER}" operate --type g12v1 --index 0 --code LATCH_ON --host "${name}" >/dev/null
run_client "${ADAPTER}" time-sync --host "${name}" >/dev/null
run_client "${ADAPTER}" restart --type warm --host "${name}" >/dev/null
sleep 0.3
events=$(docker logs "${name}" 2>/dev/null)
if grep -v '^{' <<<"${events}" | grep -q .; then fail "server stdout has lines that are not JSON"; else ok "server stdout is JSON Lines only"; fi
for want in \
    '.event == "connection-opened"' \
    '.event == "command" and .function == "direct-operate" and .type == "g12v1" and .index == 0 and .code == 3 and .count == 1 and .status == "SUCCESS"' \
    '.event == "time-sync" and (.time | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:]{8}\\.[0-9]{3}Z$"))' \
    '.event == "restart" and .type == "warm"' \
    '.event == "connection-closed"'
do
    if jq -e -s "map(select(${want})) | length > 0" <<<"${events}" >/dev/null 2>&1; then ok "event: ${want}"; else fail "no server event with ${want}"; fi
done

if [[ "${serves}" == false ]]; then
    stop_server "${name}"
    finish
    exit
fi

docker stop -t 10 "${name}" >/dev/null
code=$(docker inspect --format '{{.State.ExitCode}}' "${name}")
last=$(docker logs "${name}" 2>/dev/null | tail -1)
if [[ "${code}" == 0 ]]; then ok "SIGTERM: exit 0"; else fail "SIGTERM: exit ${code}"; fi
if jq -e '.event == "stopped"' <<<"${last}" >/dev/null 2>&1; then ok "stopped event"; else fail "last stdout line is not the stopped event: ${last}"; fi
stop_server "${name}"

# ---- a port that is already taken ----
name=$(start_server "${ADAPTER}" "${FIXTURE_ARGS[@]}"); wait_ready "${name}"
docker exec "${name}" /usr/local/bin/dnp3-interop server --ready-file /tmp/second "${FIXTURE_ARGS[@]}" >/dev/null 2>&1
code=$?
if [[ "${code}" != 0 ]]; then ok "second server on a busy port fails (exit ${code})"; else fail "second server on a busy port exited 0"; fi
stop_server "${name}"

finish
