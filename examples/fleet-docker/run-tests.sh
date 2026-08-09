#!/usr/bin/env bash
#
# Drives the dockerised fleet and asserts the behaviours `outfit fleet`
# promises. This is both the CI integration test and something a maintainer can
# run locally — there is no CI-only path that can drift from what you run by
# hand.
#
# Usage: ./run-tests.sh [--keep]
#   --keep  leave the stack running afterwards, to poke at it yourself

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly HERE
REPO_ROOT="$(cd "${HERE}/../.." && pwd)"
readonly REPO_ROOT
# Where the built binary lands; the stack is driven by the outfit built from
# this working tree, so the test covers this commit.
readonly OUTFIT_BIN="${HERE}/.outfit-test-bin"
readonly READY_TIMEOUT_SECS=90

keep_stack=0
failures=0

#######################################
# Report a passing assertion.
# Arguments:
#   Description of what passed.
# Outputs:
#   Writes the result to stdout.
#######################################
pass() {
  echo "  ok   - $1"
}

#######################################
# Report a failing assertion and record it, without aborting the run — one
# failure should not hide the rest.
# Globals:
#   failures
# Arguments:
#   Description, expected, actual.
# Outputs:
#   Writes the failure to stderr.
#######################################
fail() {
  echo "  FAIL - $1" >&2
  echo "         expected: $2" >&2
  echo "         actual:   $3" >&2
  failures=$((failures + 1))
}

#######################################
# Assert that a string contains a substring.
# Arguments:
#   Description, haystack, needle.
#######################################
assert_contains() {
  local description="$1" haystack="$2" needle="$3"
  if [[ "${haystack}" == *"${needle}"* ]]; then
    pass "${description}"
  else
    fail "${description}" "to contain '${needle}'" "${haystack}"
  fi
}

#######################################
# Assert that a string does not contain a substring.
# Arguments:
#   Description, haystack, needle.
#######################################
assert_not_contains() {
  local description="$1" haystack="$2" needle="$3"
  if [[ "${haystack}" != *"${needle}"* ]]; then
    pass "${description}"
  else
    fail "${description}" "not to contain '${needle}'" "${haystack}"
  fi
}

#######################################
# Assert an exact string equality.
# Arguments:
#   Description, actual, expected.
#######################################
assert_equals() {
  local description="$1" actual="$2" expected="$3"
  if [[ "${actual}" == "${expected}" ]]; then
    pass "${description}"
  else
    fail "${description}" "${expected}" "${actual}"
  fi
}

#######################################
# Run `outfit fleet` against the example's fleet.yaml.
# Globals:
#   OUTFIT_BIN, HERE
# Arguments:
#   Arguments to pass to `outfit fleet`.
# Outputs:
#   The command's stdout; stderr is discarded so assertions read cleanly.
#######################################
fleet() {
  "${OUTFIT_BIN}" fleet "$@" --fleet "${HERE}/fleet.yaml" 2>/dev/null
}

#######################################
# As fleet(), but merging stderr — for assertions about error messages, which
# the CLI writes to stderr.
# Globals:
#   OUTFIT_BIN, HERE
# Arguments:
#   Arguments to pass to `outfit fleet`.
# Outputs:
#   The command's stdout and stderr.
#######################################
fleet_with_stderr() {
  "${OUTFIT_BIN}" fleet "$@" --fleet "${HERE}/fleet.yaml" 2>&1
}

#######################################
# The state column for one node, or "" when the node is absent.
# Arguments:
#   Node name.
# Outputs:
#   Writes the node's state to stdout.
#######################################
node_state() {
  local name="$1"
  fleet status | awk -v n="${name}" '$1 == n {print $2}'
}

#######################################
# Wait until every node's daemon answers, so assertions do not race the
# containers' startup.
# Globals:
#   READY_TIMEOUT_SECS
# Returns:
#   0 once all nodes report a state, 1 on timeout.
#######################################
wait_for_fleet() {
  local deadline=$((SECONDS + READY_TIMEOUT_SECS))
  while (( SECONDS < deadline )); do
    if ! fleet status | grep -q "unreachable"; then
      return 0
    fi
    sleep 2
  done
  echo "Error: the fleet did not become reachable in ${READY_TIMEOUT_SECS}s" >&2
  fleet status >&2 || true
  return 1
}

#######################################
# Wait for one node to reach a state.
# Arguments:
#   Node name, expected state, timeout in seconds.
# Returns:
#   0 when the state is reached, 1 on timeout.
#######################################
wait_for_state() {
  local name="$1" want="$2" timeout="$3"
  local deadline=$((SECONDS + timeout))
  while (( SECONDS < deadline )); do
    if [[ "$(node_state "${name}")" == "${want}" ]]; then
      return 0
    fi
    sleep 1
  done
  return 1
}

#######################################
# Tear the stack down unless --keep was given. Registered as an EXIT trap so a
# failure part-way through still cleans up.
# Globals:
#   keep_stack, HERE
#######################################
cleanup() {
  if (( keep_stack )); then
    echo
    echo "Stack left running (--keep). Try:"
    echo "  cd ${HERE} && set -a && . ./.env && set +a"
    echo "  outfit fleet status --fleet ${HERE}/fleet.yaml"
    echo "Tear down with: docker compose -f ${HERE}/compose.yaml down -v"
    return
  fi
  echo
  echo "Tearing down..."
  docker compose -f "${HERE}/compose.yaml" down -v >/dev/null 2>&1 || true
  rm -f "${OUTFIT_BIN}"
}

#######################################
# Assert the fleet is usable from cold: every node up, nothing started.
#######################################
test_cold_start() {
  echo "Cold start: a usable fleet with nothing running"
  local out
  out="$(fleet status)"
  assert_contains "status lists studio" "${out}" "studio"
  assert_contains "status lists gpu-box" "${out}" "gpu-box"
  assert_contains "status lists laptop" "${out}" "laptop"
  assert_equals "studio is idle before anything is started" \
    "$(node_state studio)" "idle"
  # A fleet where nothing runs is still a working view, not an error.
  fleet status >/dev/null
  assert_equals "status succeeds with nothing running" "$?" "0"
}

#######################################
# Assert start/stop drive exactly one node.
#######################################
test_start_stop_one_node() {
  echo "Driving one node"
  fleet start studio >/dev/null
  if wait_for_state studio running 30; then
    pass "fleet start studio brings it up"
  else
    fail "fleet start studio brings it up" "running" "$(node_state studio)"
  fi
  # Starting one node must not touch the others.
  assert_equals "gpu-box is untouched by studio's start" \
    "$(node_state gpu-box)" "idle"

  local out
  out="$(fleet_with_stderr start || true)"
  assert_contains "start with no node lists the fleet" "${out}" "gpu-box"
  assert_equals "start with no node changes nothing" \
    "$(node_state gpu-box)" "idle"

  fleet stop studio >/dev/null
  if wait_for_state studio stopped 30; then
    pass "fleet stop studio stops it"
  else
    fail "fleet stop studio stops it" "stopped" "$(node_state studio)"
  fi
}

#######################################
# Assert metrics come from the engine, through the daemon's collector.
#######################################
test_metrics() {
  echo "Metrics from a real engine"
  fleet start gpu-box >/dev/null
  wait_for_state gpu-box running 30 || true

  local out
  out="$(fleet metrics)"
  # The counters the fake engine serves, parsed by outfit's own collector.
  assert_contains "token counters reach the fleet view" "${out}" "prompt tokens"
  assert_contains "prompt token count is the engine's" "${out}" "4096"
  assert_contains "resource bars are rendered" "${out}" "RAM"

  out="$(fleet metrics --format=json)"
  assert_contains "json is labelled by node" "${out}" '"node": "gpu-box"'
  assert_contains "json reports the outcome" "${out}" '"outcome": "ok"'
}

#######################################
# Assert a stopped node degrades to unreachable without failing the view.
#######################################
test_unreachable_node() {
  echo "A stopped node degrades, the rest keep reporting"
  docker compose -f "${HERE}/compose.yaml" stop laptop >/dev/null 2>&1

  local out
  out="$(fleet status)"
  assert_contains "the stopped node reads unreachable" "${out}" "unreachable"
  assert_contains "a reason is shown" "${out}" "connect"
  assert_contains "other nodes still report" "${out}" "gpu-box"
  # The whole point: one bad node must not fail the command.
  fleet status >/dev/null
  assert_equals "status still succeeds" "$?" "0"

  docker compose -f "${HERE}/compose.yaml" start laptop >/dev/null 2>&1
  wait_for_fleet || true
}

#######################################
# Assert a rejected token is not reported as unreachability.
#######################################
test_unauthorized() {
  echo "A rejected token is distinguished from an unreachable node"
  local out
  out="$(STUDIO_TOKEN=definitely-not-the-token fleet status)"
  assert_contains "a bad token reads unauthorized" "${out}" "unauthorized"
  assert_not_contains "a bad token is not reported as unreachable" \
    "$(echo "${out}" | grep '^studio')" "unreachable"
}

#######################################
# Assert a crashed engine is reported and can be recovered.
# The engine is killed via its PID inside the container: the daemon's DIRECT
# child. Killing anything else would not exercise the supervisor's crash path.
#######################################
test_crash_and_recover() {
  echo "A crashed engine is reported and recoverable"
  fleet start studio >/dev/null
  wait_for_state studio running 30 || true

  local engine_pid
  engine_pid="$(docker compose -f "${HERE}/compose.yaml" exec -T studio \
    sh -c "ps -o pid,ppid,args | awk '\$2==1 && /imposter-go/ {print \$1}'" \
    2>/dev/null | tr -d '\r ' | head -1)"
  if [[ -z "${engine_pid}" ]]; then
    fail "found the engine process to kill" "a pid" "none"
    return
  fi
  docker compose -f "${HERE}/compose.yaml" exec -T studio \
    kill -9 "${engine_pid}" >/dev/null 2>&1 || true

  if wait_for_state studio crashed 30; then
    pass "an abnormally killed engine reads crashed"
  else
    fail "an abnormally killed engine reads crashed" "crashed" \
      "$(node_state studio)"
  fi

  fleet start studio >/dev/null
  if wait_for_state studio running 30; then
    pass "fleet start recovers a crashed node"
  else
    fail "fleet start recovers a crashed node" "running" \
      "$(node_state studio)"
  fi
}

main() {
  if [[ "${1:-}" == "--keep" ]]; then
    keep_stack=1
  fi

  cd "${HERE}"
  if [[ ! -f .env ]]; then
    echo "Using .env.example for tokens (no .env present)"
    cp .env.example .env
  fi
  set -a
  # shellcheck source=/dev/null
  . ./.env
  set +a

  trap cleanup EXIT

  echo "Building outfit from the working tree..."
  (cd "${REPO_ROOT}" && go build -o "${OUTFIT_BIN}" ./cmd/outfit)

  echo "Bringing the fleet up..."
  docker compose -f "${HERE}/compose.yaml" up -d --build >/dev/null 2>&1
  wait_for_fleet

  echo
  test_cold_start
  echo
  test_start_stop_one_node
  echo
  test_metrics
  echo
  test_unreachable_node
  echo
  test_unauthorized
  echo
  test_crash_and_recover

  echo
  if (( failures > 0 )); then
    echo "${failures} assertion(s) failed" >&2
    return 1
  fi
  echo "All assertions passed"
}

main "$@"
