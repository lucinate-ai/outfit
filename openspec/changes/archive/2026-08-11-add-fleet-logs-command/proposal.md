## Why

`outfit remote logs` made a remote environment's output readable from the CLI,
and the same question — "what did the engine actually say?" — has no answer for
a fleet node. `outfit fleet status` reports that a node's engine crashed;
nothing shows why. The daemon already captures the engine's stdout and stderr to
a file and reports that file's *path* in `/v1/status`, but nothing serves its
*content*, so the only way to read it is to SSH to the box.

## What Changes

- Add `GET /v1/logs` to the daemon's control API: returns a bounded slice of the
  supervised engine's log file, with a byte offset cursor so a caller can resume
  exactly where it left off.
- Add `outfit fleet logs [node]` which reads that endpoint across the fleet —
  every node by default, one node when named — labelling each line with the node
  it came from when more than one is in play.
- Support `--follow`/`-f` by polling from the last offset, `--limit` to bound
  how much is read, and `--format text|json`, matching the shape `outfit remote
  logs` established so the two commands feel like one idea.
- Report the fleet's usual failure modes per node rather than failing the whole
  command: a node that is unreachable, one whose engine has never run (so there
  is no log file), and one whose daemon predates this endpoint.
- Extend `docs/openapi.yaml`, `Routes()` and the contract test's schema table
  together, since the daemon API is a published contract with non-human
  consumers.
- Document the command in `docs/commands/fleet.md` and the endpoint in
  `docs/http-api.md`.

## Capabilities

### New Capabilities

None. Both halves extend capabilities that already exist.

### Modified Capabilities

- `daemon-api`: its "Control endpoints" requirement gains a read-only endpoint
  serving the engine's captured output, with the cursor and bounding rules that
  make it safe to poll.
- `fleet-client`: gains an `outfit fleet logs` requirement — reading engine
  output across nodes, how lines are attributed, and how a node that cannot
  answer degrades rather than failing the command.

## Impact

- `internal/daemon`: a `LogsResponse` type, a `handleLogs` handler, the route in
  `Routes()` and `Handler()`, and a bounded reader over the supervisor's log
  file.
- `internal/daemon/openapi_test.go`: a line in `schemaFor()` for the new
  response type — without it the type has no contract coverage.
- `docs/openapi.yaml`: the path and schema, hand-written to match.
- `internal/fleet`: a `Logs` method on `Client` and on the `Node` interface, and
  a fan-out `Call` for it.
- `cmd/outfit/fleet.go` plus a new `cmd/outfit/fleet_logs.go`: the subcommand,
  its flags, rendering and follow loop; `cmd/outfit/complete.go` for completion.
- No change to `remote/` and no change to `outfit remote logs`. The rendering
  the two commands share is small and is extracted rather than reinvented — see
  the design.
