## 1. Activity tracking in the daemon (`internal/daemon`)

- [ ] 1.1 Add `internal/daemon/activity.go` with an `activity` struct
  (`sync.Mutex`, `lastActive time.Time`, `lastCounter int`, `haveCounter bool`)
  and an `observe(tokens *metrics.TokenStats, now time.Time)` method
  implementing the activity rule: in-flight > 0, or a counter that differs from
  the last one; the first counter seen sets the baseline without counting as a
  change; a nil `tokens` (failed sample) changes nothing.
- [ ] 1.2 Add `snapshot() (time.Time, bool)` returning the last-active time and
  whether one has ever been recorded.
- [ ] 1.3 Add `markActive(now time.Time)` (moves `lastActive`, drops the
  counter baseline) for use at engine start.
- [ ] 1.4 Add the fields to `Daemon` in `daemon.go`: the embedded/held
  `activity`, `SampleInterval time.Duration` (zero means
  `DefaultSampleInterval`), and `Now func() time.Time` (nil means `time.Now`),
  with a `now()` helper.
- [ ] 1.5 Define `DefaultSampleInterval = 15 * time.Second`.

## 2. Sampling loop

- [ ] 2.1 Add `func (d *Daemon) SampleActivity(ctx context.Context)` — a
  ticker loop following the `startProgress`/`runMetricsWatch` idiom in
  `cmd/outfit/remote.go`: `defer ticker.Stop()`, `select` on `ctx.Done()` and
  the tick, returning cleanly on cancellation.
- [ ] 2.2 Each tick: skip unless `d.Sup.Status()` reports `StateRunning` and
  the copied `d.scrape.BaseURL` is non-empty; otherwise call
  `metrics.ScrapeTokenStats` with a per-sample context and feed the result
  (including a nil on error) to `observe`.
- [ ] 2.3 Copy `d.scrape` under the mutex and release before the HTTP call,
  matching the existing pattern in `Daemon.Metrics`.

## 3. Wiring activity into the existing paths

- [ ] 3.1 Call `markActive` from `Daemon.StartEngine` after a successful
  `Sup.Start`, so a freshly started engine is never reported as long-idle.
- [ ] 3.2 Have `Daemon.Metrics` feed its on-demand scrape through `observe`, so
  there is one place where a sample becomes activity (design D2).
- [ ] 3.3 Confirm `Sup.Stop` leaves the activity record alone (no code change
  expected — assert it in a test).

## 4. Status reporting (`daemon-api`)

- [ ] 4.1 Add `LastActiveAt string` (`json:"lastActiveAt,omitempty"`, RFC 3339)
  and `IdleSeconds int` (`json:"idleSeconds,omitempty"`) to `StatusResponse`.
- [ ] 4.2 Populate them in `Daemon.Status()` from `snapshot()`, deriving
  `IdleSeconds` at read time; omit both when nothing has ever been recorded.
- [ ] 4.3 Verify `GET /v1/status` needs no handler change (it already writes
  `d.Status()`).

## 5. Daemon lifecycle (`cmd/outfit`)

- [ ] 5.1 In `cmdDaemon` (`cmd/outfit/serve_daemon.go`), create a
  `context.WithCancel`, start `go d.SampleActivity(ctx)` next to
  `go srv.Serve(ln)`, and cancel it in the signal-shutdown path before
  `srv.Shutdown`.
- [ ] 5.2 Do the same in `runServeForegroundAPI`, so `outfit serve --api`
  reports activity too.

## 6. Go tests

- [ ] 6.1 Unit-test `observe` in `internal/daemon`: in-flight counts; counter
  moved counts; counter unchanged does not; a lower counter counts (reset);
  the first sample is a baseline; a nil sample changes nothing.
- [ ] 6.2 Test `Status()` with an injected `Now`: `lastActiveAt`/`idleSeconds`
  present and growing, both absent before any engine has run, and preserved
  after the engine is stopped.
- [ ] 6.3 Test `SampleActivity` against an `httptest.Server` standing in for
  the engine's `/metrics` (following `TestScrapeTokenStats`): a short
  `SampleInterval` drives several samples, activity is recorded without any API
  call, and cancelling the context ends the loop.
- [ ] 6.4 Extend `TestDaemonAPI` in `daemon_test.go` to assert the new status
  fields over HTTP.
- [ ] 6.5 `go test ./... -cover` — keep the total at or above 80%.

## 7. Control plane: reading the daemon's decision

- [ ] 7.1 In `remote/lambda/shared/daemon.ts`, add `DaemonStatus`
  (`state`, `runner?`, `model?`, `uptimeSeconds?`, `logPath?`, `lastActiveAt?`,
  `idleSeconds?`), `DAEMON_STATUS_CMD` (curl `/v1/status` with the same
  `|| echo DAEMON_UNREACHABLE` marker), and `parseDaemonStatus`.
- [ ] 7.2 In `remote/lambda/shared/idle.ts`, widen `MetricsResult` to a
  discriminated union carrying either a daemon-reported `idleSeconds` or the
  existing `running`/`counter` pair, and add `idleFromDaemonStatus(status)`
  alongside `metricsFromDaemon`.
- [ ] 7.3 Teach `decideIdle` the new variant: retain override, max runtime and
  grace period are evaluated first and unchanged; the daemon-reported duration
  then decides `stop` or `wait` directly, never `update` (no state to write).
- [ ] 7.4 In `remote/lambda/stop/index.ts`, scrape `/v1/status` first; use the
  daemon's idle duration when `lastActiveAt` is present; otherwise fall back to
  the existing `/v1/metrics` scrape plus `readState`/`writeState`. Log which
  path was taken in the existing structured log line.
- [ ] 7.5 Leave the SSM `idle-state` parameter, `ensureIdleState` and the start
  Lambda's `last_wake_at` write in place for the fallback path (design D11).

## 8. Control-plane tests

- [ ] 8.1 Extend `remote/test/idle.test.ts`: daemon-reported idle under the
  threshold waits; over it stops; retention, max runtime and grace still beat
  it; the existing twenty counter cases still pass unchanged.
- [ ] 8.2 Extend `remote/test/stats.test.ts` (or add `daemon.test.ts`) for
  `parseDaemonStatus`: a representative reply, the unreachable marker, empty
  output, non-JSON, and a reply with no `lastActiveAt`.
- [ ] 8.3 `pnpm -C remote test` and `pnpm -C remote lint` (or the repo's
  equivalent scripts) pass.

## 9. Docs and validation

- [ ] 9.1 Update `docs/http-api.md`: the `GET /v1/status` field list gains
  `lastActiveAt` and `idleSeconds`, with a line on what counts as activity.
- [ ] 9.2 Update `remote/docs/architecture.md`: the "Idle / stop" flowchart and
  prose now read the daemon's idle time, with the counter comparison noted as
  the compatibility fallback; update the key-files table if needed.
- [ ] 9.3 Update `remote/README.md`'s "Idle behaviour" section to describe
  on-instance sampling.
- [ ] 9.4 `gofmt -w ./...` and `go vet ./...`.
- [ ] 9.5 `openspec validate daemon-idle-detection --strict`.
