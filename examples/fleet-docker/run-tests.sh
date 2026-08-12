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
# Assert a node that has never been told anything cannot be started. The
# daemon reads no Outfit, so until a client sends a config there is nothing
# for `fleet start` to run — and it says so rather than guessing.
#######################################
test_untold_node_cannot_start() {
  echo "A node that has been told nothing"
  local out
  out="$(fleet_with_stderr start laptop || true)"
  assert_contains "starting an untold node says there is nothing to serve" \
    "${out}" "nothing to serve"
  assert_equals "and nothing started" "$(node_state laptop)" "idle"
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
  # By now routing has woken studio once, so it has a config stored. Before
  # that it had nothing: a node is told what to run, it does not know.
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
# Assert routing picks a node and wakes one when nothing is serving. This is
# the published-port case: the engine binds 8080 inside each container and is
# published on another port outside, so it only works because fleet.yaml
# declares a per-node `engine:` block.
# Globals:
#   HERE, OUTFIT_BIN
#######################################
test_routing() {
  echo "Routing a launch at a node"
  # The only Outfit here: the nodes hold none.
  local outfit_file="${HERE}/client/Outfit"
  local out

  # Nothing is serving yet, so route reports what a launch would wake and
  # starts nothing itself.
  out="$("${OUTFIT_BIN}" fleet route --fleet "${HERE}/fleet.yaml" "${outfit_file}" 2>&1)"
  assert_contains "route says nothing is serving the model" "${out}" "is serving"
  assert_contains "route names the node a launch would wake" "${out}" "would wake"
  assert_contains "route says it started nothing" "${out}" "Nothing has been started"
  # Whatever studio was before, it is not running: reporting a route must not
  # start an engine. (An earlier test leaves it `stopped` rather than `idle`.)
  local state_after
  state_after="$(node_state studio)"
  if [[ "${state_after}" != "running" ]]; then
    pass "route really started nothing"
  else
    fail "route really started nothing" "any state but running" "${state_after}"
  fi

  # A real routed launch: it wakes studio, gates its engine with the key only
  # this side holds, and hands the agent the address it resolved. The harness
  # is a stub that prints what it was given, and HOME/XDG_CONFIG_HOME are
  # redirected so the run cannot touch the caller's own harness config.
  local sandbox="${HERE}/.routing-sandbox"
  rm -rf "${sandbox}"
  mkdir -p "${sandbox}/bin" "${sandbox}/home"
  cat > "${sandbox}/bin/opencode" <<'STUB'
#!/usr/bin/env bash
echo "HARNESS base_url=${OPENAI_BASE_URL:-<unset>} key=${OPENAI_API_KEY:-<unset>}"
STUB
  chmod +x "${sandbox}/bin/opencode"

  local launch
  launch="$(PATH="${sandbox}/bin:${PATH}" HOME="${sandbox}/home" \
    XDG_CONFIG_HOME="${sandbox}/home/.config" \
    "${OUTFIT_BIN}" harness -O="${outfit_file}" -H opencode 2>&1 || true)"
  assert_contains "a routed launch wakes the node" "${launch}" "Waking studio"
  assert_contains "the agent is pointed at the published engine port" "${launch}" "18080"
  assert_contains "the agent is given the key the client set" \
    "${launch}" "key=${STUDIO_ENGINE_KEY}"
  if wait_for_state studio running 30; then
    pass "the launch left the node running"
  else
    fail "the launch left the node running" "running" "$(node_state studio)"
  fi
  rm -rf "${sandbox}"

  out="$("${OUTFIT_BIN}" fleet route --fleet "${HERE}/fleet.yaml" "${outfit_file}" 2>&1)"
  assert_contains "route picks the running node" "${out}" "Would use studio"
  assert_contains "route resolves the published engine port" "${out}" "18080"
  assert_contains "route names the preference in force" "${out}" "prefer idle"

  # The endpoint it reports is one that actually answers.
  local base_url
  base_url="$(printf '%s\n' "${out}" | sed -n 's/.*Would use studio at \([^ ]*\).*/\1/p')"
  if curl -fsS --max-time 5 "${base_url%/v1}/health" >/dev/null 2>&1; then
    pass "the routed endpoint answers"
  else
    fail "the routed endpoint answers" "a live engine at ${base_url}" "no answer"
  fi

  # A second node, chosen by pinning rather than by ranking.
  out="$("${OUTFIT_BIN}" fleet route --fleet "${HERE}/fleet.yaml" --node gpu-box "${outfit_file}" 2>&1)"
  assert_contains "a pinned node is reported even when idle" "${out}" "gpu-box"

  # studio names an engine key, so the engine it was woken with is gated —
  # with a key that only ever existed on this side.
  local status
  status="$(curl -fsS -H "Authorization: Bearer ${STUDIO_TOKEN}" \
    http://127.0.0.1:14242/v1/status 2>/dev/null || true)"
  assert_contains "a woken node reports its engine needs a key" "${status}" '"requiresKey":true'
  assert_not_contains "the node never discloses the key" "${status}" "${STUDIO_ENGINE_KEY}"
  # The key reached the engine as a path, not a literal. The shim echoes its
  # own argv into the engine log, which is where that can be checked — in the
  # process list the shim has already exec'd and replaced itself.
  local enginelog
  enginelog="$(fleet logs studio --limit 50 2>/dev/null || true)"
  assert_contains "the engine was gated by file" "${enginelog}" "--api-key-file"
  assert_not_contains "the key itself never reaches the command line" \
    "${enginelog}" "${STUDIO_ENGINE_KEY}"
  local psout
  psout="$(docker compose -f "${HERE}/compose.yaml" exec -T studio ps ax 2>/dev/null || true)"
  assert_not_contains "the key is not in the node's process list" "${psout}" "${STUDIO_ENGINE_KEY}"

  fleet stop studio >/dev/null
  wait_for_state studio stopped 30 || true
}

#######################################
# Assert metrics come from the engine, through the daemon's collector.
#######################################
test_metrics() {
  echo "Metrics from a real engine"
  # studio is the node that has been told what to run — a start needs a
  # config, and only a client can supply one.
  fleet start studio >/dev/null
  wait_for_state studio running 30 || true

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
  # Ensure it is up: an earlier test may have left it running, and a start
  # against a running engine is the daemon's conflict, not a failure here.
  fleet start studio >/dev/null 2>&1 || true
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
  test_untold_node_cannot_start
  echo
  test_routing
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
