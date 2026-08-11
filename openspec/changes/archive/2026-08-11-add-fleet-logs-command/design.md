## Context

`outfit remote logs` shipped in #60 and reads a remote environment's logs from
CloudWatch. This is the fleet half of the same question, and almost none of the
remote implementation transfers — the transport, the data model and the ordering
guarantees are all different.

What exists today:

- `internal/daemon/supervisor.go` opens `LogPath` with `O_APPEND|O_CREATE`, 0600,
  and points the engine's stdout and stderr at it. Nothing rotates it, and
  nothing truncates it between engine restarts — the file grows for the daemon's
  lifetime. An empty `LogPath` means output goes to the daemon's own stdio, so
  there is no file at all (the foreground `serve --api` case).
- `/v1/status` reports `logPath`, so callers already know a log exists, and can
  do nothing with that.
- `internal/daemon/api.go` registers routes in a table (`Routes()`), each naming
  its response schema, behind an optional bearer token.
- `internal/daemon/openapi_test.go` compares `Routes()` against
  `docs/openapi.yaml` and each schema's property names against the Go struct's
  JSON tags. The comment is explicit that the API has non-human consumers — the
  control-plane Lambdas curl it over SSM against hand-written TypeScript mirrors
  — so the spec is a contract, not documentation.
- `internal/fleet` has `Client` (one method per endpoint), a `Node` interface,
  and `FanOut` running a `Call` across nodes concurrently, collecting a
  `NodeResult` per node with an `Outcome` that already models unreachable and
  failed.

## Goals / Non-Goals

**Goals:**

- Read a node's engine output over the daemon API, bounded and resumable.
- `outfit fleet logs [node]`, fanning out by default, degrading per node.
- Exact following: never reprint a line, never miss one.
- Keep the daemon's contract honest — route table, OpenAPI document and schema
  table move together.

**Non-Goals:**

- Streaming transports. See the decision below: polling with a cursor, not
  WebSocket, not SSE.
- Log rotation or retention on the node. The daemon's log growth is a real
  problem, but it is a separate one and predates this change — see Risks.
- Search or filtering. `grep` composes with this fine.
- Changing `outfit remote logs`, or unifying the two behind one command. They
  answer the same question about different things, and their data models differ
  enough that a forced union would serve neither.

## Decisions

### `GET /v1/logs` with a byte-offset cursor, polled

Request: `?offset=<n>&limit=<bytes>`. Response: the bytes read, the offset
immediately after them, and the log's current size.

The engine log is a local append-only file, which gives an exact cursor for
free. That is a strictly better position than the remote command is in: there,
CloudWatch forced a timestamp window with a 10-second overlap and event-id
de-duplication, because events can arrive late and out of order. Here, byte
offset N means byte offset N — following resumes precisely, with no overlap and
no dedupe.

Considered and rejected:

- **WebSocket.** Logs flow one way; the client never talks back mid-stream, so
  the bidirectional half is dead weight. It would add a second protocol to the
  daemon, a separate upgrade-time authentication path, and N long-lived
  connections when the fleet fans out across N nodes. Decisively: OpenAPI cannot
  describe it, so the endpoint would fall out of the contract test that makes
  this API trustworthy.
- **SSE.** Lighter than WebSocket and genuinely one-way, but it still cannot be
  expressed in the contract document, and it buys latency that log reading does
  not need. If polling ever proves too slow, SSE can be added later *alongside*
  the polling endpoint without invalidating it.
- **Returning the whole file.** An unrotated log on a long-lived daemon can be
  arbitrarily large; a fleet-wide read would pull all of it across the network N
  times over.

With no offset the endpoint returns the *tail*, not the head — the recent end is
what diagnosis wants, and it also means the first request of a follow is
naturally the backlog.

### Adding a requirement rather than modifying "Control endpoints"

`daemon-api`'s "Control endpoints" requirement enumerates the endpoints, so
adding one arguably modifies it. This change adds standalone requirements
instead, because a delta's MODIFIED form must restate the entire requirement —
seventy-odd lines here — and a hand-copied restatement is a transcription risk
for no gain. The logs endpoint changes nothing about how status, start, stop,
metrics or deploy-config behave; its rules (bounding, cursor, the states it must
distinguish) are its own.

No delta is needed for `daemon-api-contract`: it already requires the published
description to cover the implementation and be verified against it, so the
OpenAPI work is mandated by the spec as it stands.

### What is actually shared with `outfit remote logs`

Less than "mirroring" suggests, and the design is honest about it rather than
forcing reuse:

- **Shared:** the flag vocabulary (`--follow`/`-f`, `--limit`, `--format
  text|json`), the follow loop's shape, and the rule that per-line attribution
  appears only when output genuinely mixes origins.
- **Not shared:** `remote.LogEvent` and everything built on it. CloudWatch
  returns discrete events with a timestamp and an id; the daemon returns a byte
  range of raw text. There are no per-line timestamps to sort by and no ids to
  deduplicate, so the merge-and-cap machinery has nothing to operate on.

The consequence is stated in the fleet spec: fleet output is **not** interleaved
across nodes by time. Merging raw text from several machines into one
chronological stream would mean inventing an order the data does not carry.
Each node's output stays in its own order, labelled.

Practically, the shared part is small enough that the sensible move is to lift
the two or three rendering helpers into a place both commands can call, not to
build an abstraction over two dissimilar fetches.

### Fan-out reuses what fleet already has

`Logs` becomes a method on `Client` and on the `Node` interface, plus a `Call`
for `FanOut`. `NodeResult`'s existing `Outcome` already models the degradation
the spec requires; "no engine has ever run here" and "this daemon is too old"
are new detail on that existing shape rather than a new mechanism. A 404 from a
node identifies the too-old daemon, which is the one case worth naming
specifically — the operator's fix is to upgrade that node.

## Risks / Trade-offs

- [The daemon never rotates its engine log, so a long-lived node's file grows
  without bound] → this change does not cause it, but it does make it visible,
  and the endpoint is designed not to make it worse: reads are bounded and
  default to the tail. Rotation deserves its own change; noted rather than
  smuggled in here.
- [A byte offset is meaningless if the file is replaced or truncated] → the
  response reports a position beyond the current end rather than returning
  nothing, so a follow resumes from the end instead of hanging on a position
  that will never arrive.
- [Bytes are not lines: a bounded read can start or end mid-line] → the reader
  trims to line boundaries and reports the offset it actually stopped at, so the
  cursor stays exact even though the rendering is line-clean.
- [Log content crosses the network to whoever holds the fleet's token] → it is
  the same trust boundary as `start`/`stop`, which already let a caller run and
  kill engines; the endpoint is read-only and adds no new credential path. Worth
  stating plainly in the docs because logs can carry prompts or model output.
- [Polling N nodes on a follow is N requests per interval] → the interval is a
  few seconds, each request is bounded, and a node with nothing new returns an
  empty body. `outfit fleet logs <node>` narrows it to one when that matters.

## Migration Plan

Additive on both sides. A daemon that predates the endpoint keeps working; the
fleet client reports those nodes as needing an upgrade and carries on with the
rest, which is the mixed-version state any fleet will actually be in during a
rollout. No config change, no state, nothing to roll back beyond reverting.

## Open Questions

- Whether `--limit` should be expressed in bytes (what the endpoint speaks) or
  lines (what the operator thinks in). Leaning towards lines at the CLI,
  translated to a byte budget at the client, with the endpoint staying bytes.
- Whether a fleet-wide follow should print a node's backlog on the first poll,
  as the remote command does, or start from "now" to avoid N backlogs at once.
