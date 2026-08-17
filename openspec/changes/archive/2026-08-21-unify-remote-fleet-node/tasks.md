## 1. Model a remote environment as a fleet node

- [x] 1.1 Add the pure mappers from a control-plane reply to a node's types: a status
      reply → the node's status (state; model and runner when known; last-active and idle
      seconds; the version when present; the engine endpoint derived from the base URL)
      and a stats reply → the shared stats shape
- [x] 1.2 Add a fleet node backed by a `remote.Config`, whose status, metrics, start, stop
      and logs are each a thin wrapper over the exported remote call plus that mapping
- [x] 1.3 Have its node-level wake (a start asked to run on a supplied deploy config)
      refuse with a message naming the deployment path, and map a rejected control call
      onto the same typed outcomes fan-out already classifies
- [x] 1.4 Tests: both mappers — running and not-running, with and without recorded
      activity, and the version present or absent — and that a node reporting a rejected
      credential is a typed outcome rather than an unhandled error or a panic

## 2. Fan-out over an explicit node set

- [x] 2.1 Extract the concurrent fan-out so it runs over an explicit node list and returns
      one result per node, in the order the list is given
- [x] 2.2 Make the fleet-file `FanOut` a convenience that builds its nodes and delegates
      to the extracted fan-out
- [x] 2.3 Tests: a mixed set (a local-shaped node and a remote-shaped node) returns one
      result per member in order, and a node whose call fails is a typed result that does
      not stop the rest of the set

## 3. One source for the shared status facts

- [x] 3.1 Add a status-view value and the fact-to-text helpers (state, what it serves,
      last-active, version, uptime) beside the existing shared metrics renderer
- [x] 3.2 Route the fleet status view's per-node facts through the shared view; keep its
      one-node-per-row table exactly as today
- [x] 3.3 Route the remote status view's shared facts through the same view; keep its
      key-value layout and its `base_url`/`healthy` lines exactly as today
- [x] 3.4 Tests: the two views agree on the shared facts for equivalent inputs, and each
      command's existing output remains byte-identical (existing tests pass unchanged)

## 4. Verification

- [x] 4.1 `gofmt -w`, `go vet ./...`, and `go test ./... -cover` at or above 80%
- [x] 4.2 `openspec validate unify-remote-fleet-node --strict` passes clean
- [x] 4.3 Update `AGENTS.md`: the remote environment as a fleet node, fan-out over an
      explicit node set, and the single source the two status views draw from

## 5. Declare remote nodes in the fleet file

- [x] 5.1 Accept `kind: remote` in the fleet file (an omitted kind still defaults to
      `daemon`); a remote node's name is the registered environment and it needs no `host`
- [x] 5.2 Have the node constructor build a `remoteNode` for a `remote` entry, loading the
      environment's config (keyed by the node's name) from the registry; an unregistered
      environment is a per-node configuration error naming it, not a command failure
- [x] 5.3 Tests: a fleet file listing a registered environment observes it through the
      regular fan-out; an unregistered one is a `config-error` row; a daemon + remote file
      observes in order

## 6. Examples

- [x] 6.1 Add `examples/fleet-remote` — a fleet of remote environments — with a short README:
      how to have environments, how to list them as nodes, and the observe commands
- [x] 6.2 Add `examples/fleet-mixed` — daemons and remote environments in one fleet — with a
      short README and the daemon's token `.env.example`
