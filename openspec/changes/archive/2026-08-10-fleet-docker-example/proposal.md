## Why

Two gaps, one artifact closes both.

**Nothing verifies fleet against a real daemon.** `outfit fleet` is covered by unit
tests using `httptest` stubs. Those prove the client logic, but no test starts a
real `outfit daemon`, supervises a real engine process, or crosses a real network
boundary with real auth. That is precisely the shape of gap that produced the
systemd `$HOME` bug: every test green, the wiring wrong in a real process.

**There is nothing to run.** `examples/fleet/` documents a `fleet.yaml` for
machines you already own. Trying fleet management today means owning several
machines and setting up a daemon on each — a steep first step for something
whose whole point is seeing many engines at once.

## What Changes

- **`examples/fleet-docker/`**: a Docker Compose fleet of several containers, each
  running a **real `outfit daemon`**, that a user can bring up with
  `docker compose up -d` and immediately point `outfit fleet status` /
  `outfit fleet metrics -w` at — a real multi-node fleet on a laptop, with no
  GPUs and no cloud.
- **A fake engine the daemon genuinely supervises**: each daemon starts
  [Imposter](https://imposter.sh)'s native engine as its engine process, via a
  `llama-server` shim on `PATH`, so `engineFor("llamacpp")` resolves to it with
  no change to production code. Imposter serves a llamacpp-dialect Prometheus
  `/metrics` and a `/health`, so token stats and readiness are real end to end.
- **The same stack is the integration test**: a `run-tests.sh` drives it
  non-interactively and asserts. One artifact, two jobs — an example exercised
  on every CI run cannot rot, which is the usual fate of examples.
- **CI runs it**, so fleet has end-to-end coverage against real daemons rather
  than only against stubs.

## Capabilities

### New Capabilities

- `fleet-docker-example`: the runnable dockerised fleet — what it brings up, that
  each node is a real daemon supervising a real engine process, that it works
  from cold with nothing running, and the fleet behaviours its test run asserts.

### Modified Capabilities

_None._ The example exercises `fleet-client`, `fleet-config`, and `daemon-api`
exactly as they are; production code is unchanged (the shim exists so no
test-only branch is needed in outfit itself).

## Impact

- New `examples/fleet-docker/`: `compose.yaml`, a `Dockerfile` for the node
  image, the Imposter engine config, the `llama-server` shim, `fleet.yaml`,
  `.env.example`, `run-tests.sh`, and a README.
- New CI job running `run-tests.sh`; adds Docker as a CI dependency.
- One Go fix the harness immediately earned: `sortFlagsBeforeArgs` moved a
  value-taking flag without its value, so `outfit fleet start <node> --fleet
  <path>` bound the node name to `--fleet`. The same latent bug affected
  `remote`/`serve` (`--format json` after a positional). Fixed with the flag
  set consulted for which flags consume a following argument, plus test cases.
  No other production changes — the example otherwise tests outfit as shipped.
- `examples/fleet/` stays as the "real machines" example; the two are
  complementary and cross-link.
