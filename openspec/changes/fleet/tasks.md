## 1. Fleet config (`internal/fleet`)

- [x] 1.1 Create `internal/fleet` with `Config`/`Node` and a `fleet.yaml`
      parser: node list, unique-name validation, host, API port (default to
      the daemon's default port), optional per-node `kind` (default `daemon`),
      optional `tokenEnv` reference
- [x] 1.2 Implement fleet-file resolution: `--fleet <path>` else
      `./fleet.yaml`; a required-but-missing file errors naming the path
- [x] 1.3 Resolve a node's token from the environment then the adjacent
      `.env` (reuse `opencode.ParseEnvFile` + env precedence); an unset named
      var is a config error, not an empty token
- [x] 1.4 Tests: parse a multi-node file, duplicate-name rejection, default
      port, token resolved from `.env`, no literal token accepted

## 2. Daemon client and node abstraction (`internal/fleet`)

- [x] 2.1 Add a `Client` that calls one daemon's control API
      (`GET /v1/status`, `GET /v1/metrics`, `POST /v1/start`, `POST /v1/stop`)
      with the node's bearer token and a short per-request timeout
- [x] 2.2 Define the `Node` interface (`Status`/`Metrics`/`Start`/`Stop`) and
      a `daemonNode` implementation over `Client`; leave the interface ready
      for a future remote-environment kind (no remote kind implemented)
- [x] 2.3 Define `NodeResult` with typed outcomes (ok / `unreachable` /
      `unauthorized` / `config-error`) and classify daemon call failures into
      them (connection vs 401 vs unset-token)
- [x] 2.4 Tests against an `httptest` daemon: ok, connection-refused →
      unreachable, 401 → unauthorized, unset token → config-error

## 3. Fleet command (`cmd/outfit/fleet.go`)

- [x] 3.1 Add the `fleet` subcommand group and dispatch
      (`status`/`metrics`/`start`/`stop`), plus `main.go` usage and the
      `complete.go` table entry (keep `TestCompletionCoversDispatch` green)
- [x] 3.2 Implement concurrent fan-out over the nodes with a value-typed
      result slice, ordered by fleet-file order for stable rendering
- [x] 3.3 `fleet status`: one row per node (name, state, runner/model,
      reachability); unreachable/unauthorized/config-error rendered as rows,
      command still succeeds
- [x] 3.4 `fleet start <node>` / `fleet stop <node>`: single-node by contract
      — no node argument lists the fleet and does nothing; unknown node names
      the known ones; surface the daemon's 409/idempotent stop results

## 4. Fleet metrics + shared renderers

- [x] 4.1 Move the bar/table/json metrics renderers (and the watch redraw) to
      a shared location so `remote metrics` and `fleet metrics` share them;
      verify `outfit remote metrics` output is byte-identical
- [x] 4.2 `fleet metrics`: render each node's `internal/metrics.Stats` in the
      selected `--format`, node name as heading; unreachable nodes reported
      not omitted; json labelled by node including errors
- [x] 4.3 `fleet metrics --watch`/`-w`: pre-render the fleet into a buffer,
      clear-and-redraw on the interval, clean exit on SIGINT/SIGTERM
- [x] 4.4 Tests: multi-node bar/table/json rendering with a mix of reachable
      and unreachable nodes; watch loop exits on interrupt

## 5. Verification and docs

- [x] 5.1 End-to-end test with stub daemons (`httptest`): a fleet of several
      nodes renders status and metrics, one down shows unreachable, start/stop
      drive one node
- [x] 5.2 `go test ./... -cover` >= 80%, `gofmt` and `go vet` clean
- [x] 5.3 Add `docs/commands/fleet.md` and update README and AGENTS.md for the
      fleet command, `fleet.yaml`, and the node abstraction
- [x] 5.4 `openspec validate fleet --strict` passes
- [x] 5.5 Add a `fleet.yaml` example (e.g. under `examples/`) showing a
      LAN/tailscale node list with token references
