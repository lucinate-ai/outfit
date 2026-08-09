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
- The stop Lambda's idle path becomes "is `idleSeconds` past the threshold?",
  and nothing else. One way to judge idleness, not two.
- The control plane keeps no activity history: the SSM `idle-state` parameter
  and everything that reads or writes it goes.
- The same idle awareness exists for a purely local `outfit daemon`.

**Non-Goals:**

- Moving the retention override, the maximum-runtime cap or the post-launch
  grace period out of the Lambda. They are about the instance and the session,
  not about engine activity, and the daemon knows nothing about launch time or
  `Retain-Until` tags.
- Persisting the last-active time across daemon restarts. A restarted daemon
  has no running engine, and the grace period covers the window in which that
  matters.
- Supporting an instance whose baked outfit predates this change. Deployment
  ordering handles that (see the migration note), not a compatibility path.
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
baseline. This is what *replaces* the wake race the control plane currently
handles with `last_wake_at` in SSM: a freshly booted instance reports
`idleSeconds` counted from the engine start, so the sweep cannot terminate it
for having been idle "since before it existed". That is why the start Lambda's
`last_wake_at` write can be deleted outright rather than kept alongside (D11).
Stop deliberately leaves `lastActive` alone, so a stopped engine still reports
when real work last happened.

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
neither, and a missing `lastActiveAt` is what the Lambda keys "nothing
observed" off.

**D9 — The Lambda reads `/v1/status` and nothing else.** `shared/daemon.ts`
gains `DAEMON_STATUS_CMD` and `parseDaemonStatus`, mirroring the existing
metrics pair; `idleCheck` scrapes status and passes the reported idle seconds
into `decideIdle`. A reply with no `lastActiveAt` is treated exactly like an
unreachable daemon — no activity observed — which is the policy that already
governs a failed scrape. With nothing observed, `decideIdle` measures idleness
from the launch time instead — the same anchor the counter path fell back on —
so an unreachable instance still gets its grace period and then stops at the
threshold. `/v1/metrics` stays where it is for the stats path; the idle path
simply stops using it. Rejected: keeping the counter scrape as a
fallback for daemons baked before this change. It would double the idle logic
permanently to smooth over a one-off deployment ordering, and a fallback nobody
exercises is a fallback nobody notices breaking.

**D10 — `decideIdle` takes a duration, not counters.** `MetricsResult` becomes
`{ok: true; idleSeconds: number} | {ok: false}`; `IdleState`, the `update`
decision and the counter comparison are all deleted. The precedence chain —
retain override, max runtime, grace, then the threshold — is untouched, and
`decideIdle` becomes a pure function of durations with no state to thread
through it. The existing cases in `remote/test/idle.test.ts` that drive
counters are rewritten to drive `idleSeconds`; the retain, cap and grace cases
are unaffected.

**D11 — The SSM `idle-state` parameter goes with it.** Once the counter path is
gone, nothing reads `IdleState`: `ensureIdleState` seeds a parameter no one
consumes, the stop Lambda's `readState`/`writeState` calls have no subject, and
the start Lambda's `last_wake_at` write is superseded by the daemon marking an
engine start as activity (D5). All of it is removed, along with the
`ssm:PutParameter` grant on the stop Lambda if nothing else needs it. Leaving
a dead parameter behind would be the more confusing outcome, not the safer one.

## Risks / Trade-offs

- **A daemon that samples but whose engine metrics are permanently broken
  reports a stale `lastActiveAt` and looks increasingly idle.** → That is the
  correct outcome: it matches today's "a failed reading is no activity", and
  the instance is terminated at the threshold rather than burning GPU-hours.

- **The 15 s sampler adds a recurring HTTP call and log-free work on the
  instance.** → It is one loopback request to an endpoint the engine already
  serves, at 1/20th the rate of a busy client's own traffic. Negligible against
  a GPU instance.

- **A stop Lambda deployed ahead of the re-baked AMI terminates busy
  instances.** An old daemon reports no `lastActiveAt`, which the new Lambda
  reads as no activity, so a working endpoint is terminated once the idle
  threshold (15 minutes by default) passes. → This is the price of dropping the
  fallback, and it is real. Mitigation is ordering, stated in the migration
  plan: bake first, deploy second. The blast radius is bounded — a terminated
  instance is relaunched on the next request, since the endpoint is
  scale-to-zero by design — and a `Retain-Until` tag pins anything mid-debug.

- **`/v1/status` becoming a decision rather than a report couples the control
  plane to the daemon's judgement.** → That is the point of the change, and it
  is why `lastActiveAt` (the fact) is reported alongside `idleSeconds` (the
  derivation), so a caller that disagrees can still compute its own.

- **Clock skew between the instance and the Lambda.** → The Lambda uses
  `idleSeconds`, a duration measured entirely on the instance, so no comparison
  crosses the two clocks. `lastActiveAt` is reported for humans and logs.

## Migration Plan

The daemon change and the Lambda change are independent deployments, and the
order matters because there is no compatibility path.

1. Land the whole change and cut an outfit release.
2. Bake the runtime AMIs against that release (`pnpm bake`), for every runner.
   Until this is done, nothing has changed for a running fleet: the old Lambda
   is still deployed and still reads counters.
3. Verify: launch an instance from the new AMI and confirm
   `curl 127.0.0.1:4242/v1/status` reports `lastActiveAt` and `idleSeconds`,
   and that `idleSeconds` stays near zero under traffic.
4. Only then `cdk deploy` the runtime stack, which ships the new stop Lambda
   and drops the `idle-state` parameter.

**Rollback:** revert the stack deploy. The old Lambda reads `/v1/metrics`,
which the new daemon still serves unchanged, so a rolled-back control plane
works against a new AMI — the incompatibility runs one way only. Its
`readState` already treats a missing `idle-state` parameter as empty state, and
the old deploy Lambda seeds one on the next deploy call, so nothing has to be
restored by hand.

## Open Questions

None blocking. Worth revisiting after this lands: whether the sampler should
also feed a short in-memory activity history, so a `fleet` client could show
per-node activity over time rather than a single number.
