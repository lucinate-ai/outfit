## Why

The `serve-daemon` and `remote-on-daemon` changes made `outfit daemon` the one
way an engine runs and reports its state — locally, on a home-lab box, and on a
cloud instance. But there is still no way to *see* more than one at a time: each
daemon is queried one node at a time with `curl`. Fleet management is the point
of that groundwork — one `outfit` observing every engine you run, wherever it
runs. This change adds the client half: a `fleet.yaml` naming the nodes and an
`outfit fleet` command family that fans out over their control APIs and renders
the cluster at a glance.

## What Changes

- **`fleet.yaml`**: a file listing nodes — each with a `name`, a `host`, the
  daemon's API address/port, and an optional bearer-token reference (resolved
  from the environment/`.env`, never written inline). Resolved from the working
  directory (or `--fleet <path>`), the same way `Outfit`/`preset.ini` are.
- **`outfit fleet status`**: fan out over every node's `GET /v1/status`, render
  a one-line-per-node table of engine state, what each is serving, and
  reachability.
- **`outfit fleet metrics`**: fan out over `GET /v1/metrics`, render each node's
  engine + system metrics using the existing bar/table/json formatters, with a
  `--watch` mode reusing the clear-screen redraw already built for
  `remote metrics`.
- **`outfit fleet start [node]` / `outfit fleet stop [node]`**: drive one named
  node's engine (or, with no node, refuse and list the nodes rather than acting
  on all — start/stop are not fan-out operations). These call the daemon's
  `POST /v1/start` / `POST /v1/stop`.
- **Unreachable nodes degrade, never fail the view**: a node whose daemon can't
  be reached (or rejects the token) is shown as `unreachable`/`unauthorized`
  with its error, and the rest of the fleet still renders.
- **A node-kind seam left open for cloud environments**: v1 implements only
  daemon nodes, but the node abstraction is defined so a registered `remote`
  environment can become a second node kind later at little cost (both already
  speak the same stats dialect). Not implemented here.

Web UI is explicitly out of scope (issue #50); this is TUI-first.

## Capabilities

### New Capabilities

- `fleet-config`: the `fleet.yaml` format and its resolution — node list,
  per-node connection details, token references, and how the file is found.
- `fleet-client`: the `outfit fleet` command family — the fan-out client that
  polls each node's daemon control API (bearer auth), the per-node
  reachability/error handling, and the status/metrics/start/stop subcommands
  including the metrics watch dashboard.

### Modified Capabilities

_None._ The daemon control API (`daemon-api`) and the metrics shape
(`engine-metrics`) are consumed exactly as they already exist; this change adds
a client and does not alter their contracts.

## Impact

- New package `internal/fleet` (parse `fleet.yaml`, a `Client` calling the
  daemon API, a `Node` abstraction with a daemon kind and room for a remote
  kind).
- New `cmd/outfit/fleet.go` (the `fleet` subcommand group), plus dispatch,
  usage, and completion entries in `main.go`/`complete.go`.
- The metrics formatters in `cmd/outfit/remote.go` are reused; where a shared
  renderer needs to serve both `remote metrics` and `fleet metrics`, it moves
  to a shared spot rather than being duplicated.
- Reuses `internal/daemon`'s status/metrics response shapes and
  `internal/metrics.Stats` as the wire types — no new metrics dialect.
- Docs: a new `docs/commands/fleet.md`, plus README and AGENTS.md entries.
- Test coverage must stay >= 80% (`go test ./... -cover`).
