## Why

The daemon already works out when its engine last did anything — it samples the
engine's counters every 15 seconds and keeps a last-active time — but that
answer only ever comes out of `GET /v1/status`, and only `outfit fleet status`
reads it. So one command shows "last active 12s ago" while
`outfit fleet metrics` and `outfit remote metrics` — the views people leave
open to watch a box — show token counters and utilisation bars with no
indication of whether anything is happening, and `outfit remote status`, the
first thing you type when you want to know how an endpoint is doing, reports
three lines that say nothing about it either.

That is the wrong way round. These views exist to answer "is this thing doing
any work?", and right now they make you read the running-request count and
guess. The information is already collected, already exposed on a sibling
endpoint, and already rendered by one command — it just is not on the screens
where it would be most useful.

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
- `outfit remote status` reports it too, beside the `state` and `healthy` lines
  it already prints. Its Lambda has no daemon data today, so it gains a fetch
  of the instance's `/v1/status` — run alongside the health check it already
  makes rather than after it, so the command does not get slower.
- The wording is "last active", matching `outfit fleet status` and
  deliberately avoiding "idle" — that word is already an engine *state*
  meaning "nothing started", and one screen should not carry two meanings of
  it.
- A node or endpoint with no recorded activity shows nothing rather than a
  figure implying it has sat unused since it started. On the cloud side that
  includes a *stopped instance*: reaching the daemon needs a running box, so
  there is nothing to report and nothing is claimed. This is not the same as a
  stopped *engine* on a live host, which does still report.

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
- `remote-endpoint`: `outfit remote status` reports when the endpoint last did
  work, alongside the instance state and health it reports today.

## Impact

- `internal/metrics/metrics.go` — two fields on `Stats`.
- `internal/daemon/daemon.go` — `Daemon.Metrics` reads the activity record
  before its not-running early return.
- `internal/remote/remote.go` — two fields on `StatsResponse`, and two on
  `Response` (the control Lambdas' shared reply, which `status` uses).
- `remote/lambda/shared/stats.ts`, `remote/lambda/shared/daemon.ts`,
  `remote/lambda/stats/index.ts` — relay the fields.
- `remote/lambda/start/index.ts` — the `status(env)` handler gains a daemon
  fetch beside its health check.
- `cmd/outfit/metrics_render.go`, `cmd/outfit/remote.go` — render in bar and
  table, and in `cmdRemoteStatus`; `fleet metrics` inherits it.
- `docs/openapi.yaml` is a build-enforced contract:
  `internal/daemon/openapi_test.go` compares it against the serialised struct
  fields and fails when they disagree, so the `Stats` schema must be updated in
  the same change. `docs/http-api.md`, `docs/commands/serve.md`,
  `docs/commands/remote.md` and `docs/commands/fleet.md` describe the output
  and need the same edit.
- No breaking changes: both fields are omitted when empty, so existing JSON
  consumers see the payload they see today until an engine has run.
