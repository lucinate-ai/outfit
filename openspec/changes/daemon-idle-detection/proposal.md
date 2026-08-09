## Why

Whether the remote engine is busy is decided in the control plane, on a single
scrape every five minutes. The stop Lambda curls the on-instance daemon's
`/v1/metrics` over SSM, and `decideIdle` compares the engine's cumulative token
counter against the value it last wrote to an SSM `idle-state` parameter. One
sample that lands in a lull between requests reads as idleness, so a genuinely
busy endpoint can be terminated because nothing happened to be in flight at the
instant the sweep looked. The daemon sits on the box watching the engine
already; it is the thing that should answer "is this engine active?", and it
can sample far more often than any external caller.

## What Changes

- The daemon samples the engine's token counters on its own schedule while an
  engine runs — often enough (tens of seconds, not minutes) that a lull between
  requests cannot be mistaken for idleness.
- The daemon records a **last-active** time, moved forward whenever a sample
  shows requests in flight or the cumulative counter has changed, and set when
  an engine starts so a freshly started engine is never instantly idle.
- `GET /v1/status` gains `lastActiveAt` (RFC 3339) and the derived
  `idleSeconds`, so a caller reads a decision rather than raw counters.
- The stop Lambda's idle sweep reads `idleSeconds` from the daemon's status and
  terminates when it exceeds the threshold. The counter comparison is deleted,
  not kept as a fallback.
- The retention override, the maximum-runtime cap and the post-launch grace
  period stay in the Lambda. They are policy about the instance and the
  session, not statements about engine activity.
- **BREAKING (deployment ordering)**: the control plane no longer understands a
  daemon that reports no `lastActiveAt`, and treats one as showing no activity
  — the same way it already treats an unreachable daemon. The runtime AMIs must
  therefore be re-baked with the new outfit *before* the stop Lambda is
  deployed. There is no wire-format break: `/v1/status` only gains fields.
- The SSM `idle-state` parameter, its seeding, and the start Lambda's
  `last_wake_at` write are removed with the counter path. Nothing reads them
  once the daemon owns the activity history, and the wake race they existed to
  close is now closed on the instance, by the daemon marking an engine start as
  activity.

## Capabilities

### New Capabilities
- `engine-activity`: how the daemon judges engine activity — the background
  sampler and its cadence, what counts as activity, the last-active time it
  maintains across engine starts and stops, and the idle duration derived from
  it.

### Modified Capabilities
- `daemon-api`: the status reply additionally reports when the engine was last
  active and how long it has been idle.
- `remote-engine-host`: the control plane's idle check reads the daemon's
  last-active time instead of its raw counters, and keeps no activity history
  of its own.
- `endpoint-lifecycle`: activity is judged from continuous on-instance
  sampling rather than from a single reading taken at each sweep, so a lull
  between the sweep's ticks is no longer mistaken for idleness.

## Impact

- **New code**: an activity sampler in `internal/daemon` (background loop,
  last-active state, idle duration), reusing `metrics.ScrapeTokenStats` and the
  daemon's existing `ScrapeTarget`.
- **Changed code**: `internal/daemon/daemon.go` (`StatusResponse` gains the two
  fields; engine start marks activity), `cmd/outfit/serve_daemon.go` (start and
  stop the sampler alongside the API listener, for both `outfit daemon` and
  `outfit serve --api`).
- **Changed code (control plane)**: `remote/lambda/shared/daemon.ts` (a status
  type and its SSM curl command), `remote/lambda/shared/idle.ts` (take a
  daemon-reported idle duration; the counter comparison, `IdleState` and the
  `update` decision go away), `remote/lambda/stop/index.ts` (scrape status
  instead of metrics; no SSM state read or write).
- **Removed**: `ensureIdleState` and the `idle-state` parameter in
  `remote/lambda/shared/environments.ts`, the start Lambda's `last_wake_at`
  write, and the stop Lambda's `readState`/`writeState` use.
- **Docs**: `docs/http-api.md` (the new status fields),
  `remote/docs/architecture.md` (the idle flow and its key-files table) and
  `remote/README.md` (idle behaviour).
- **Deployment ordering matters**: re-bake the runtime AMIs before deploying
  the stop Lambda. A new Lambda against an old daemon reads no activity at all
  and terminates a busy instance once the idle threshold passes.
