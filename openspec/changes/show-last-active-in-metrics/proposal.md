## Why

The daemon already works out when its engine last did anything — it samples the
engine's counters every 15 seconds and keeps a last-active time — but that
answer only ever comes out of `GET /v1/status`. So `outfit fleet status` can
show "last active 12s ago" while `outfit fleet metrics` and
`outfit remote metrics`, the views people actually leave open to watch a box,
show token counters and utilisation bars with no indication of whether anything
has happened recently.

That is the wrong way round. A metrics view exists to answer "is this thing
doing any work?", and right now it makes you read the running-request count and
guess. The information is already collected, already exposed on a sibling
endpoint, and already rendered elsewhere — it just is not on the screen where
it would be most useful.

## What Changes

- `metrics.Stats` — the shape both `outfit remote metrics` and
  `outfit fleet metrics` render — gains `lastActiveAt` (RFC 3339) and
  `idleSeconds`, on the same terms `/v1/status` already uses: both present or
  both absent, absent until an engine has run.
- The daemon fills those fields on `GET /v1/metrics` from the same activity
  record that feeds `GET /v1/status`, so the two endpoints can never disagree.
  Unlike the rest of the metrics payload, they are reported for a stopped
  engine too — the point of keeping the record across a stop is that it still
  answers "when did work last happen?".
- The stats Lambda relays the two fields through to `outfit remote metrics`,
  alongside the environment and instance facts only the control plane knows.
- All three formats show it: `bar` adds a line under the header, `table` adds a
  `last active:` row, `json` carries the fields as they arrive. `fleet metrics`
  picks this up through the shared renderers.
- The wording is "last active", matching `outfit fleet status` and
  deliberately avoiding "idle" — that word is already an engine *state*
  meaning "nothing started", and one screen should not carry two meanings of
  it.
- A node or endpoint with no recorded activity shows nothing rather than a
  figure implying it has sat unused since it started.

## Capabilities

### New Capabilities

None — this exposes an existing daemon measurement in views that already
render its neighbours.

### Modified Capabilities

- `engine-metrics`: the rendering-compatible stats shape gains the engine's
  last-active time and idle duration, so the formatters have them to draw.
- `daemon-api`: `GET /v1/metrics` reports `lastActiveAt` and `idleSeconds` on
  the same terms as `GET /v1/status`, including for an engine that is not
  running.
- `remote-stats`: `outfit remote metrics` reports how long since the endpoint
  last did work, in every format.
- `remote-metrics-bar-format`: the bar format shows the last-active figure, and
  shows it for a stopped endpoint where it draws no bars.

## Impact

- `internal/metrics/metrics.go` — two fields on `Stats`.
- `internal/daemon/daemon.go` — `Daemon.Metrics` reads the activity record
  before its not-running early return.
- `internal/remote/remote.go` — two fields on `StatsResponse`.
- `remote/lambda/shared/stats.ts`, `remote/lambda/shared/daemon.ts`,
  `remote/lambda/stats/index.ts` — relay the fields.
- `cmd/outfit/metrics_render.go`, `cmd/outfit/remote.go` — render in bar and
  table; `fleet metrics` inherits it.
- `docs/openapi.yaml` is a build-enforced contract:
  `internal/daemon/openapi_test.go` compares it against the serialised struct
  fields and fails when they disagree, so the `Stats` schema must be updated in
  the same change. `docs/http-api.md`, `docs/commands/serve.md`,
  `docs/commands/remote.md` and `docs/commands/fleet.md` describe the output
  and need the same edit.
- No breaking changes: both fields are omitted when empty, so existing JSON
  consumers see the payload they see today until an engine has run.
