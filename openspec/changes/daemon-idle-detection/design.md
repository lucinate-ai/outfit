## Context

Today the stop Lambda's `idleCheck` (`remote/lambda/stop/index.ts`) runs on a
`rate(5 minutes)` EventBridge rule. Each tick it curls the on-instance daemon's
`/v1/metrics` over SSM (`DAEMON_METRICS_CMD` in `remote/lambda/shared/daemon.ts`),
lifts `tokens.running` and `tokens.counter` out of the reply, and hands them to
`decideIdle` (`remote/lambda/shared/idle.ts`), which compares the counter
against the value stored in the SSM parameter `/cloud-vm-llm/<env>/idle-state`
and writes a new `last_change_at` whenever it moved.

The whole activity signal is therefore one reading every five minutes, and the
history of those readings lives in the control plane. Two consequences follow.
An endpoint with steady but bursty traffic can present nothing in flight and an
unchanged counter at the exact moment a sweep lands — five minutes of real work
either side of it is invisible. And the same question ("is this engine busy?")
is answered nowhere else, so a local `outfit daemon` has no idea, and neither
would a future fleet client.

The daemon already holds everything needed to answer it: `Daemon.scrape`
(a `metrics.ScrapeTarget`), `metrics.ScrapeTokenStats`, and the supervisor's
state. It just never looks unless someone calls `/v1/metrics`.

## Goals / Non-Goals

**Goals:**

- The daemon samples engine activity on its own schedule, frequently enough
  that a lull between requests cannot read as idleness.
- `GET /v1/status` reports a decision (`lastActiveAt`, `idleSeconds`), not raw
  counters.
- The stop Lambda's idle path becomes "is `idleSeconds` past the threshold?".
- A mixed fleet during a re-bake keeps working: an older daemon that reports no
  `lastActiveAt` is judged the way it is today.
- The same idle awareness exists for a purely local `outfit daemon`.

**Non-Goals:**

- Moving the retention override, the maximum-runtime cap or the post-launch
  grace period out of the Lambda. They are about the instance and the session,
  not about engine activity, and the daemon knows nothing about launch time or
  `Retain-Until` tags.
- Persisting the last-active time across daemon restarts. A restarted daemon
  has no running engine, and the grace period covers the window in which that
  matters.
- Changing the sweep interval, the thresholds, or the metrics/stats path.
- Adding a new endpoint. `/v1/status` gains fields; nothing else moves.

## Decisions

**D1 — The sampler lives in `internal/daemon`, not `internal/metrics`.**
`internal/metrics` is a collection library: it scrapes and parses, and holds no
state. Activity is a property of *the supervised engine*, which is the daemon's
subject, and the sampler needs the supervisor's state to know when to sample at
all. A new `internal/daemon/activity.go` holds an `activity` struct
(mutex-guarded `lastActive time.Time`, `lastCounter int`, `haveCounter bool`)
plus the loop. The alternative — a self-contained sampler in `internal/metrics`
parameterised by a "should I sample?" callback — inverts the dependency for no
gain, since the daemon is the only caller.

**D2 — One `observe` path, fed by both the sampler and `/v1/metrics`.**
`Daemon.Metrics` already scrapes on demand. Rather than have two independent
notions of the latest counter, both call a single `d.observe(tokens *TokenStats,
now time.Time)`. A caller polling `/v1/metrics` therefore also refreshes the
activity record, and there is exactly one place where "does this sample count
as activity?" is decided. Cost: `/v1/metrics` under load produces slightly more
frequent observations, which is harmless.

**D3 — Sample every 15 seconds, as a package constant, not a flag.**
The value only has to be small relative to the idle thresholds (15 minutes in
the cloud default) and large relative to a scrape (5 s client timeout). 15 s
gives ~60 observations per five-minute sweep window, which is the whole point
of the change. It is exported as `DefaultSampleInterval` and settable on the
`Daemon` struct so tests can drive it fast; no CLI flag and no env var, because
nothing about the deployment needs to tune it and every knob is a thing to keep
working. Rejected: deriving it from the idle threshold, which the daemon does
not and should not know.

**D4 — "Changed", not "increased", and the first sample is a baseline.**
This mirrors `decideIdle`'s existing rule: an engine restart resets its
counters, and a lower counter is a sign of life. The first sample after a start
sets `lastCounter` without being read as a change, so a start does not
double-count. Engine start itself sets `lastActive` (D5), so nothing is lost.

**D5 — Starting an engine counts as activity; stopping it does not clear the
record.** `StartEngine` stamps `lastActive = now` and drops the counter
baseline. This is what closes the wake race the control plane currently handles
with `last_wake_at` in SSM: a freshly booted instance reports `idleSeconds`
counted from the engine start, so the sweep cannot terminate it for having been
idle "since before it existed". Stop deliberately leaves `lastActive` alone, so
a stopped engine still reports when real work last happened.

**D6 — A failed sample is a non-observation.** It does not move `lastActive`
and it does not clear the counter baseline. The "unreachable means idle" policy
stays in the control plane, where it belongs: a daemon that cannot be reached
at all reports nothing, and the Lambda already treats that as no activity. A
daemon that *is* reachable but whose engine scrape failed should not be made to
lie in either direction.

**D7 — An injectable clock on `Daemon`, as a `now func() time.Time` field.**
There is no clock abstraction anywhere in the Go tree today; the daemon tests
poll with `waitForState` instead. Idle duration is arithmetic on wall-clock
time, and polling cannot test "idle for 20 minutes". A nil field means
`time.Now`, matching how `Collector.Run`, `Collector.GOOS` and
`Daemon.BuildArgv` are already injected. Rejected: a package-level `var now =
time.Now`, which is not safe across parallel tests.

**D8 — `idleSeconds` is derived at read time, not stored.** `Status()` computes
`int(now.Sub(lastActive).Seconds())`. Storing it would need the sampler to tick
just to keep a number fresh. `lastActiveAt` is the fact; `idleSeconds` is a
convenience for a caller that would otherwise parse a timestamp in a shell
pipeline. Both are `omitempty`, so a daemon that has never run an engine emits
neither and the Lambda's fallback triggers on a missing `lastActiveAt`.

**D9 — The Lambda reads `/v1/status`, with `/v1/metrics` as the fallback.**
`shared/daemon.ts` gains `DAEMON_STATUS_CMD` and `parseDaemonStatus`, mirroring
the existing metrics pair. `idleCheck` scrapes status first; when the reply
carries `lastActiveAt`, it passes the reported idle seconds into `decideIdle`
and no SSM state is read or written. When it does not — an older baked AMI —
it falls back to the existing metrics scrape and counter comparison. Two SSM
round-trips in the fallback case only, which is transient and rare.

**D10 — `decideIdle` keeps one signature, with a third `MetricsResult`
variant.** `MetricsResult` becomes
`{ok: true; idleSeconds: number} | {ok: true; running; counter} | {ok: false}`
(discriminated by a `source` field). The precedence chain — retain override,
max runtime, grace, activity, threshold — is untouched, so all twenty existing
cases in `remote/test/idle.test.ts` keep passing; the daemon-reported variant
short-circuits the counter comparison and returns `wait`/`stop` without an
`update` action, since there is no state to write. Rejected: a second
`decideIdleFromDaemon` function, which would duplicate the retain/cap/grace
ordering — the part that is easiest to get subtly wrong.

**D11 — SSM `idle-state` stays.** The parameter, `ensureIdleState`, and the
start Lambda's `last_wake_at` write are all left in place: they are what the
fallback path needs. Removing them is a follow-up once every AMI in every
environment is baked past this change, and is not worth coupling to it.

## Risks / Trade-offs

- **A daemon that samples but whose engine metrics are permanently broken
  reports a stale `lastActiveAt` and looks increasingly idle.** → That is the
  correct outcome: it matches today's "a failed reading is no activity", and
  the instance is terminated at the threshold rather than burning GPU-hours.

- **The 15 s sampler adds a recurring HTTP call and log-free work on the
  instance.** → It is one loopback request to an endpoint the engine already
  serves, at 1/20th the rate of a busy client's own traffic. Negligible against
  a GPU instance.

- **Two code paths for idleness in the Lambda until every AMI is re-baked.** →
  Bounded and deliberate (D11). Both are covered by tests, and the fallback is
  the code that ships today, so the risk is that it goes untested rather than
  that it breaks. The status-path tests assert the fallback triggers on a
  missing `lastActiveAt`.

- **`/v1/status` becoming a decision rather than a report couples the control
  plane to the daemon's judgement.** → That is the point of the change, and it
  is why `lastActiveAt` (the fact) is reported alongside `idleSeconds` (the
  derivation), so a caller that disagrees can still compute its own.

- **Clock skew between the instance and the Lambda.** → The Lambda uses
  `idleSeconds`, a duration measured entirely on the instance, so no comparison
  crosses the two clocks. `lastActiveAt` is reported for humans and logs.

## Open Questions

None blocking. Worth revisiting after this lands: whether the sampler should
also feed a short in-memory activity history (for a `fleet` client to show a
sparkline), and when to delete the SSM `idle-state` parameter and the
counter-comparison path once no old AMI remains.
