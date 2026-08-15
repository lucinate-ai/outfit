## Context

See `proposal.md` — Why. What shapes the approach:

- The `Node` interface (`internal/fleet`) is the join point. A local daemon node
  implements it over the daemon control API; this change adds a remote node that
  implements the same interface over the remote control plane. The interface returns
  `daemon.StatusResponse`, `metrics.Stats`, and `daemon.LogsResponse`; the remote control
  plane returns `remote.Response` and `remote.StatsResponse`, and `remote.StatsResponse`
  already aliases the `internal/metrics` types, so the metrics mapping is nearly
  structural.
- `internal/fleet` already imports `internal/remote` (for the deploy config), so a remote
  node living in `internal/fleet` keeps the import graph acyclic. `internal/remote` must
  not import `internal/fleet`.
- The remote calls are SigV4-signed through the existing, already-tested
  `remote.Status`/`Stats`/`Start`/`Stop`/`logs` functions. A fleet-package test cannot
  override `remote`'s unexported HTTP client, so it cannot drive a real signed call
  through the node — the testable seam is the *mapping* of a reply into the node types.

## Goals / Non-Goals

**Goals:**

- A remote environment is a first-class fleet node; the same fan-out and render path
  drives it as it drives a local node.
- The overlapping status facts of the two status commands come from one source.
- Non-breaking: both status commands' output is byte-identical to today.

**Non-Goals:**

- Not collapsing the control-plane state vocabulary ("running"/"stopped"/"pending") into
  the engine-state vocabulary ("idle"/"running"/"crashed"). They are mapped honestly.
- Not adding a new CLI surface. The node and the explicit fan-out are the enablement;
  wiring a command to observe a remote environment is a follow-up.
- No changes to the remote control plane, its auth, or the `remote.DeployConfig` payload.

## Decisions

### The remote node lives in `internal/fleet`, backed by `remote.Config`

The two alternatives are putting it in `internal/remote` (which would force an
`internal/remote` to `internal/fleet` import and create a cycle) or in `cmd/outfit` (which
leaves the abstraction where only one caller sees it). `internal/fleet` is where `Node`
and its one implementation live, so the second node belongs beside the first.

### The node is a thin wrapper; the *mapping* is pure and unit-tested

`remote.Status`/`Stats` are already covered by the remote package's own httptest tests,
which exercise the signed call. A fleet test re-driving them would only retest the
transport. So the node's `Status`/`Metrics` are "call the exported remote function, then
map the reply," and the mapping is factored into pure functions that tests exercise
directly. The transport correctness is inherited from the remote package.

### State is mapped pass-through

The control-plane state string goes straight into the node's `status.State`. The
renderers print the state as given, so a remote endpoint's "running"/"stopped" is shown
as the control plane reports it. We do not invent an engineering translation, which would
be lossy and would drift from the source.

### Wake is refused, not implemented

A node-level start-on-a-deploy-config means "wake this machine to serve that." A remote
endpoint's "what to serve" is a different, heavier flow — `outfit remote deploy`, with
provisioning, weight seeding and ingress. Proxying `StartWith` to `deploy` would conflate
the two. Refusing it with a message that names the deploy path is honest. `Start` and
`Stop` map straight onto `remote.Start`/`remote.Stop`.

### Fan-out generalizes to an explicit node set

`Config.FanOut` becomes a convenience that builds its nodes from the fleet file and
delegates to a package-level fan-out over an explicit `[]Node`. This is the seam a mixed
set (local nodes plus a remote environment) needs, and it is what makes "one node driver"
more than a type alias: the same function observes any set.

### The shared status source is additive in the client

A small status-view value plus fact-to-text helpers live beside the existing shared
metrics renderer. Both the remote and fleet status paths populate the view from their
native types and then render their own layout on top. Existing output is preserved; what
becomes single-sourced is the computation and wording of the shared facts.

## Risks / Trade-offs

- [The node's `Status` must not fetch the version, which only the stats reply carries] →
  Only fetch it when the endpoint is running, mirroring today's remote status; otherwise
  leave the field empty.
- [Mapping the control-plane state into the node's status shape is lossy] → Keep it
  pass-through and re-add the remote-specific facts (address, health) in the remote view,
  so nothing a user sees today is lost.
- [A test that constructs a real-configured remote node would hit AWS signing] → Tests
  use a fake node and the pure mappers; the thin remote calls remain covered by the remote
  package.
- [Two layouts remain (key-value lines vs. a table)] → Intentional: each carries facts the
  other does not. The drift being removed is the shared-fact logic, which is single-sourced.
