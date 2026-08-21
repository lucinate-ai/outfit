## Why

`outfit remote` and `outfit fleet` are two parallel clients of the same daemon — one
through the cloud control plane, one through each node's control API — and each family
keeps its own copy of the "ask about it and render the answer" logic. `fleet.Node` was
designed to admit a remote kind, but only a local daemon node implements it. As a result
the status, metrics, version and last-active facts are computed and rendered in separate
code, so a change lands on one side and the other drifts again.

## What Changes

- **A remote environment becomes a fleet node.** Add a fleet node backed by a
  `remote.Config` implementing the existing node contract (`Name`/`Status`/`Metrics`/
  `Start`/`Stop`/`Logs`), mapping the control-plane replies onto the same status and
  metrics shapes a local node yields. A remote environment that cannot be reached, or
  whose call is rejected (including a rejected credential), is a typed outcome, not an
  error that fails the command.
- **Fan-out runs over an explicit node set.** Extract the concurrent fan-out so it is
  driven over any explicit set of nodes rather than only a fleet file's; the fleet-file
  fan-out delegates to it. A set mixing local nodes and a remote environment is observed
  identically to a set of local nodes alone.
- **Status facts render from one source.** The remote and fleet status views derive their
  overlapping facts — state, what it is serving, how long since it last did work, and the
  outfit version — from a single shared source in the client, so the logic and wording
  cannot fork. **Non-breaking:** each command's existing output layout is preserved (the
  remote keeps its `base_url`/`healthy` lines; the fleet keeps its one-node-per-row table).
- **Waking a remote environment is refused.** A node-level "start on this deploy config"
  is refused for a remote environment with a message naming the deploy path: a remote
  endpoint is provisioned by `outfit remote deploy`, not woken like a node.
- **The fleet file declares remote nodes.** A fleet-file node's kind now accepts `remote`
  (an omitted kind still defaults to `daemon`). A remote node's *name* is the registered
  environment it drives — env-shaped (no `/`, no `.json`) — and it needs no host, so a
  fleet of remote environments, or of daemons and remote environments mixed, observes and
  drives through the same fan-out as a fleet of daemons. An unregistered environment is a
  per-node configuration error naming it, not a command failure.
- **Examples.** `examples/fleet-remote` (a fleet of remote environments) and
  `examples/fleet-mixed` (daemons and remote environments), each with a short README of how
  to run it.

## Capabilities

### New Capabilities
- `remote-node`: a remote, scale-to-zero environment represented and driven as a fleet
  node — the node contract implemented over the remote control plane, fan-out over an
  explicit node set, and the single shared source the two status views draw from.

### Modified Capabilities
<!-- None. The change is additive: no existing requirement's externally-visible
     behaviour changes, because both status commands keep their current output. -->

## Impact

- `internal/fleet`: the node file gains a remote-backed node and its constructor; the
  fan-out file gains the explicit node-list fan-out, with the fleet-file fan-out
  delegating to it. Import graph stays acyclic — `internal/fleet` already imports
  `internal/remote`. The fleet-file config accepts a `remote` node kind and the node
  constructor loads that environment's config from the registry, so status, metrics, start
  and stop all reach a remote node with no change to the `fleet` command itself.
- `cmd/outfit`: a shared status-view value and fact helpers added beside the existing
  shared metrics renderer, used by both the `remote` status and `fleet` status paths. No
  change to either command's output.
- `examples/`: two new runnable guides (`fleet-remote`, `fleet-mixed`) and a short note in
  `docs/commands/fleet.md` on the `remote` node kind.
- No change to the `remote` control plane, its auth, or the `remote.DeployConfig` payload.
  No new dependencies.
