## Context

See proposal.md — Why. What the earlier changes leave in place:

- `internal/daemon` serves the control API and defines the wire shapes:
  `StatusResponse` (state, runner, model, uptime, logPath) and
  `internal/metrics.Stats` from `GET /v1/metrics`. Auth is a bearer token
  (`OUTFIT_API_TOKEN`), constant-time compared.
- `cmd/outfit/remote.go` renders `remote.StatsResponse` in bar/table/json via
  `formatMetricsBar/Table/JSON` (all take an `io.Writer`), and `runMetricsWatch`
  does the clear-screen redraw with a pre-rendered buffer.
  `remote.StatsResponse`'s stat sub-types are already aliases of
  `internal/metrics` types, so the daemon's `/v1/metrics` body and the remote
  Lambda's reply are the same dialect.
- Outfit resolves adjacent `.env` via `opencode.ParseEnvFile`, with the
  precedence environment-over-`.env` (see `applyOutfitEnv`).
- The CLI dispatch and completion tables (`main.go`, `complete.go`) are covered
  by `TestCompletionCoversDispatch`, which scans `main.go` for `case "…":`.

## Goals / Non-Goals

**Goals:**

- One client that renders the whole fleet at a glance, reusing the metrics
  renderers verbatim.
- Fan-out that is resilient: one bad node never blanks the view.
- A `Node` seam that a future remote-environment kind slots into without
  reworking the client.

**Non-Goals:**

- The web dashboard (#50).
- Implementing the remote-environment node kind (left as an interface point).
- Any change to the daemon API or the metrics shape.
- Deploy-config push across the fleet, cross-node orchestration, scheduling —
  status/metrics/start/stop only.

## Decisions

**D1 — `internal/fleet` owns config, client, and the node abstraction.** New
package: `Config`/`Node` (parsed `fleet.yaml`), a `Client` that calls one
daemon's control API, and a `Node` interface (`Status`, `Metrics`, `Start`,
`Stop`) with a `daemonNode` implementation. The CLI layer
(`cmd/outfit/fleet.go`) does fan-out and rendering. Keeping the daemon-calling
in `internal/fleet` mirrors how `internal/remote` holds the Lambda-calling.

**D2 — Reuse the metrics renderers by moving them, not copying.** The
bar/table/json formatters and the watch loop in `remote.go` already take an
`io.Writer` and a stats value. `fleet metrics` renders the same
`internal/metrics.Stats` per node, so the renderers move to a shared home (a
small `cmd/outfit` metrics-render file, or `internal/metrics` if they carry no
remote-only concerns) and both callers use them. The remote-only bits (cost
lookup, instance type) stay behind the remote caller. This is the one
refactor of existing code; it must leave `remote metrics` output byte-identical
(guarded by its existing tests).

**D3 — Node kind is an interface, daemon is the only implementation.** `Node`
is an interface so a `remoteEnvNode` (wrapping a registered `remote`
environment, calling its stats Lambda) can be added later — issue #6's "almost
free" because it already yields `internal/metrics.Stats`. `fleet.yaml` leaves
room for a per-node `kind` (defaulting to `daemon`); an unknown/other kind in
v1 is a clear "not supported yet" error, not a silent skip. No remote-kind code
ships here.

**D4 — Fan-out is concurrent with a per-node timeout; results are a value
type.** Each node is polled in its own goroutine with a short client timeout
(a few seconds — a fleet view must stay snappy). Each yields a `NodeResult`:
either the status/metrics, or a typed failure (`unreachable`, `unauthorized`,
`config-error`). The renderer switches on that, so a failure is a rendered row,
never a returned error. `fleet status`/`metrics` themselves only error on a
problem with the *fleet file* (missing, unparseable), never on node state.

**D5 — Token resolution reuses outfit's env precedence.** A node's
`tokenEnv` (env var name) is resolved from the process environment then the
`.env` beside the `fleet.yaml`, via the same `ParseEnvFile` + getenv path the
remote commands use — so the fleet's secrets live in `.env`, never in
`fleet.yaml`. A named-but-unset token var is a per-node `config-error`, so a
typo surfaces on that node's row rather than as an auth failure.

**D6 — start/stop are single-node by contract.** Fan-out is for observation;
mutating every engine at once is a footgun, so `start`/`stop` demand a node
name and otherwise print the node list. This matches the daemon's own
one-engine focus and keeps the dangerous verbs deliberate.

**D7 — Watch mode reuses the remote watch shape.** `fleet metrics --watch`
pre-renders the whole fleet into a buffer, then clears and writes — the same
technique as `runMetricsWatch`, so a slow node delays a refresh but never
tears the display. Interval matches the remote default; SIGINT/SIGTERM exits
cleanly.

## Risks / Trade-offs

- [Moving the renderers churns `remote.go`] → they already take `io.Writer`;
  the move is mechanical and `remote metrics`'s tests pin the output.
- [A hung node stalls a refresh] → per-node timeout bounds it; the node shows
  `unreachable` once it times out rather than freezing the view.
- [json format shape for a fleet is new] → keyed/labelled by node, unreachable
  nodes included with their error, so a consumer sees the whole fleet; pinned
  by a test.
- [Token in the wrong place] → the spec forbids literal tokens in
  `fleet.yaml`; only a reference is read, mirroring the `.env` rule everywhere
  else.

## Open Questions

- Exact `fleet.yaml` field spelling (`port` vs `apiAddr`, `token` vs
  `tokenEnv`) — settle in implementation against the daemon's `--api-addr`
  vocabulary; does not affect the capability contracts above.
- Whether the fleet json groups by node object or an array of
  `{node, ...}` — a rendering detail, fixed by its test.
