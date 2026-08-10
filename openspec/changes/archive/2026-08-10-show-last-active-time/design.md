## Context

See proposal.md — Why. The mechanics that matter for the approach:

- `internal/daemon/activity.go` already holds the whole measurement: an
  `activity` record with `observe`, `markActive` and `snapshot`, sampled every
  15 seconds by `SampleActivity`. Nothing new needs measuring.
- `Daemon.Status` (`internal/daemon/daemon.go:179`) converts a `snapshot()`
  into `LastActiveAt` (RFC 3339 string) and `IdleSeconds` (int). `Daemon.Metrics`
  (`daemon.go:201`) sits next to it, already touching `d.act` — it feeds the
  scrape through `d.act.observe` — but does not read it back.
- `metrics.Stats` is the single shape both metrics views render.
  `remote.StatsResponse` restates it with the control plane's extras, using
  type aliases into `internal/metrics` for the sub-structs, which is why
  `cmd/outfit/metrics_render.go` can serve both.
- The cloud metrics path is a relay: `remote/lambda/stats/index.ts` curls the
  instance's `/v1/metrics` over SSM (`DAEMON_METRICS_CMD`) and merges EC2
  facts. Its `DaemonMetrics` interface is a hand-written mirror of
  `metrics.Stats`.
- The cloud status path is separate and much thinner. `outfit remote status`
  is a `GET` on `cfg.StartURL`, served by the `status(env)` branch of
  `remote/lambda/start/index.ts:80`. It returns `state`, `healthy` and
  `base_url` into `remote.Response` — the control Lambdas' shared reply — and
  has no daemon data at all. For a running instance it already makes one SSM
  round trip (`checkHealth` → `runShellCommand(HEALTH_COMMAND)`), gated behind
  `isSsmAgentOnline`. For a non-running instance it returns before any SSM
  call.
- `remote/lambda/shared/daemon.ts` already has everything the status path
  needs: `DAEMON_STATUS_CMD`, a `DaemonStatus` interface carrying
  `lastActiveAt`/`idleSeconds`, and `parseDaemonStatus`. They exist for the
  auto-stop check in `idle.ts`, which is the only current consumer.
- `docs/openapi.yaml` is enforced by `internal/daemon/openapi_test.go`, which
  compares it against the JSON fields of the structs the handler serialises.
  Adding a field to `metrics.Stats` fails the build until the schema matches.
- `formatDuration(seconds int)` in `cmd/outfit/remote.go:754` is the only
  duration formatter, and `cmd/outfit/fleet.go:109` already renders
  `(last active %s ago)` from `IdleSeconds`, gated on `LastActiveAt != ""`.

There is no `stats` command to consider: `outfit remote stats` was renamed to
`outfit remote metrics`, and "stats" survives only in type and Lambda names.

## Goals / Non-Goals

**Goals:**

- One measurement, one record, reported from both `/v1/status` and
  `/v1/metrics`, with no possibility of the two disagreeing.
- `outfit remote metrics`, `outfit fleet metrics` and `outfit remote status`
  show it without any of them learning anything about how activity is derived.
- Wording and duration formatting identical to `outfit fleet status`, so the
  same fact reads the same way wherever it appears.
- `outfit remote status` stays as quick as it is today, and stays a read.

**Non-Goals:**

- No change to what counts as activity, the sample interval, or the
  `engine-activity` capability. This exposes an existing answer; it does not
  revisit it.
- No new endpoint, and no second round trip on the metrics path: nothing calls
  `/v1/status` to decorate a metrics view. The status path is the one place a
  daemon call is genuinely added, because it makes none today.
- No change to `outfit fleet status`, which already shows this.
- No history or activity series — one timestamp, as today.
- No attempt to report activity for a *stopped instance* in the cloud. Reaching
  the daemon needs a running box; a stopped instance is silent, and this
  change does not invent a control-plane-side record to cover the gap.

## Decisions

### D1: Carry the fields on the metrics payload, not by calling status too

`metrics.Stats` gains `LastActiveAt string` and `IdleSeconds int`, both
`omitempty`, mirroring `daemon.StatusResponse` field for field.

Alternative considered: leave the payload alone and have each caller fetch
status alongside metrics. `fleet.NodeResult` already carries both a `Status`
and a `Metrics`, so `fleet metrics` could populate both with a second fan-out
call. Rejected on three counts — it doubles the fan-out for one figure; it
introduces a window where the status and metrics halves of one screen describe
different moments; and it does nothing for `outfit remote metrics`, where the
Lambda would need a second SSM round trip per request. Putting the fields in
the payload fixes every consumer at once, including the Lambda, which needs no
new command.

Alternative considered: a `time.Time` rather than an RFC 3339 string.
Rejected: the value crosses a TypeScript relay on its way to
`outfit remote metrics`, and `StatusResponse` already settled on a string for
that reason. Matching it means the Lambda's mirror interfaces stay
copy-paste-consistent and the two endpoints serialise identically.

### D2: Populate in `Daemon.Metrics`, above the not-running early return

`Daemon.Metrics` returns early when the state is not `running`, skipping the
collector and the scrape. The activity fields are set before that return, so a
stopped or crashed engine still reports when it last worked — which is what the
record surviving a stop is for (`engine-activity`: "Stopping the engine
preserves the history"). The conversion is the same three lines as
`Daemon.Status`; extracting it into a small helper on `Daemon` keeps the two
endpoints textually incapable of drifting.

Note what is deliberately *not* changed: `Metrics` already calls
`d.act.observe(tokens, ...)`, so a metrics poll feeds the shared record exactly
as before. Reading the record back does not make polling count as activity —
`observe` still decides that from the counters alone, so a `--watch` loop does
not keep an idle engine looking busy.

### D3: `idleSeconds` stays omitted at zero; `lastActiveAt` is the gate

`Daemon.Status` sets `IdleSeconds` only when it is greater than zero, and both
fields are `omitempty`. So an engine active this very second serialises
`lastActiveAt` with no `idleSeconds`, and "0 seconds idle" is indistinguishable
from "absent" in the JSON. This is existing behaviour, and `fleet.go:109`
already handles it correctly by gating the render on `LastActiveAt != ""` and
letting `formatDuration(0)` produce `0s`.

Every new render site follows that rule: **gate on `lastActiveAt`, never on
`idleSeconds`**. The alternative — dropping `omitempty` from `IdleSeconds` so
zero serialises — would make the metrics payload disagree with the status
payload for no gain, and would break the openapi test's field comparison for
`StatusResponse` if applied consistently.

### D4: Rendering — same words, same formatter, one place per format

The wording is `last active <d> ago`, from
`formatDuration(stats.IdleSeconds)` — identical to `fleet status`. Per format:

- **bar**: a line between the header and the resource bars, indented to the
  same column as the bar labels so the block reads as one unit. Not a bar:
  elapsed time has no ceiling to fill against.
- **table**: a `last active:` row in the key-value block, next to `uptime:`,
  padded to the existing column.
- **json**: the fields as they arrive, no formatting — a consumer wanting a
  duration has `idleSeconds` and a consumer wanting a fact has `lastActiveAt`.

The bar and table lines go into `cmd/outfit/metrics_render.go` as a shared
helper, alongside `renderTokenLines` and friends. That is the file's stated
purpose — the parts both metrics views draw — and it is what makes
`fleet metrics` pick this up for free rather than by a parallel edit.

`fleet metrics`'s per-node heading needs no change: the shared helper renders
under it. `fleet status` is untouched.

### D5: The Lambda relays, it does not collect

`DaemonMetrics` and `StatsResult` in `remote/lambda/shared/` gain optional
`lastActiveAt?: string` and `idleSeconds?: number`, and `stats/index.ts` copies
them through with the other daemon fields. No new SSM command: `idle.ts`
already reads `/v1/status` for the auto-stop decision and stays as it is —
that path is about deciding, this one is about displaying, and they should not
be made to share a request.

A control plane deployed before this change simply omits the fields, and the
CLI omits the line — the same absence it shows for an engine that has done no
work. So `outfit remote metrics` degrades to today's output rather than
erroring, and the Lambda redeploy is not a hard prerequisite for upgrading the
CLI.

### D6: `remote status` asks the daemon in parallel with the health check

The `status(env)` handler's running-instance branch currently reads:

```
const healthy = (await isSsmAgentOnline(id)) && (await checkHealth(id));
```

It gains a second SSM run of `DAEMON_STATUS_CMD`, issued **concurrently** with
`checkHealth` once `isSsmAgentOnline` has passed, so the branch costs the
slower of the two rather than their sum. `status` is the command people type
repeatedly while waiting for a box, and making it visibly slower to add a
convenience line would be a poor trade.

Alternative considered: fold `DAEMON_STATUS_CMD` into `HEALTH_COMMAND` as one
SSM invocation and split the output. One round trip instead of two, but it
couples two independent questions — "is the engine serving?" and "has it done
anything?" — behind a delimiter-parsing step, and a change to either command
risks breaking the other's parse. Not worth saving a round trip that
concurrency already hides.

Alternative considered: have `status` call the stats Lambda, or share its
`/v1/metrics` fetch. Rejected: `/v1/metrics` does the full system collection
(`nvidia-smi`, CPU, memory) to answer a question `/v1/status` answers from an
in-memory record. `status` should ask the cheap endpoint.

The daemon fetch is wrapped so that any failure — SSM error, unreachable
daemon, unparseable reply — leaves the figure absent and the rest of the
report untouched. It must not be able to turn a working `status` into a
failing one, which is why it is not folded into the `healthy` expression.

### D7: The status reply carries the same two fields, on `remote.Response`

`remote.Response` — the shared reply struct for `start`, `stop`, `status` and
`deploy` — gains `LastActiveAt` and `IdleSeconds`, and `cmdRemoteStatus`
prints a `last active:` line gated on the timestamp, using `formatDuration`
exactly as the metrics formatters do (D3, D4).

The JSON tags are camelCase (`lastActiveAt`, `idleSeconds`) rather than the
snake_case of that struct's older neighbours (`base_url`,
`retry_after_seconds`). `Response` is already mixed — `seedInstanceId`,
`modelId`, `contextSize` are camelCase — and these two fields are relayed
verbatim from the daemon, so matching the daemon's names keeps the whole hop
copy-paste-consistent and avoids a rename in the Lambda purely to satisfy a
convention the struct does not hold to anyway.

Only the `status` branch of the start Lambda populates them. `start`'s ready
reply deliberately does not: the daemon counts an engine start as activity, so
a freshly started endpoint would report "last active 0s ago" — technically
true, and useless.

### D8: `docs/openapi.yaml` is part of the change, not follow-up

`internal/daemon/openapi_test.go` fails the build when the `Stats` schema and
the struct disagree, so the schema edit lands with the struct edit. The
description text should say what `StatusResponse` already says about these two
fields, including that they are absent until an engine has run.

## Risks / Trade-offs

- **The same figure now has two sources on a `fleet` screen** — `fleet status`
  reads it from `/v1/status`, `fleet metrics` from `/v1/metrics`, and a user
  running both sees two timestamps taken moments apart. → Both derive
  `idleSeconds` at read time from one `lastActive` fact, so they differ only by
  the seconds between the two requests, which is the same skew any two polls
  have. Nothing to mitigate beyond keeping the derivation in one helper (D2).

- **`idleSeconds` absent at zero is a trap for a new consumer** — someone
  gating on `idleSeconds` rather than `lastActiveAt` silently hides the line
  for the busiest engine there is. → D3 fixes the rule; a test that renders a
  zero idle with a present `lastActiveAt` and asserts `last active 0s ago`
  pins it for every format.

- **A stopped endpoint now prints a line where the format previously printed
  nothing** — anything parsing bar or table output positionally could be
  surprised. → These formats are for humans and already vary by which stats a
  host can supply; `json` is the contract for scripts and gains only fields.

- **`formatDuration` has no day unit**, so an endpoint idle for two days reads
  `48h 0m 0s`. → Pre-existing, and shared with `uptime` and `fleet status`.
  Changing it would change those too; out of scope here, and better done once
  for all three than smuggled into this change.

- **`remote status` gains an SSM round trip it did not make before**, on every
  call against a running instance — a small latency and cost per invocation,
  and one more thing that can fail. → D6 runs it concurrently with the health
  check so the wall-clock cost is zero in the common case, and isolates its
  failures so the worst outcome is a missing line. For a non-running instance
  no call is added at all, because that branch returns before any SSM.

- **A stopped cloud instance can never show the figure, while a stopped engine
  on a live host can** — the same words describe two situations with different
  answers, which could read as a bug. → The distinction is real and worth
  keeping: the daemon knows, and an instance that is off cannot be asked. The
  specs state it for both `remote status` and `remote metrics`, and the docs
  task says it in the one place a user would look.

- **The Lambda's `DaemonMetrics` is a hand-maintained mirror of a Go struct**
  with nothing enforcing the match. → Pre-existing; this change adds two more
  fields to the same manual seam. The graceful-absence behaviour in D5 means a
  mirror that falls behind degrades to a missing line rather than a failure.

## Migration Plan

No data migration and no breaking change — both fields are `omitempty`, so
today's consumers see today's payload until an engine has run.

Deploy order does not matter, because every combination degrades safely:

- New CLI against an old daemon or an old control plane: fields absent, line
  omitted.
- Old CLI against a new daemon: two unknown JSON fields, ignored.

The daemon and fleet paths work as soon as the binary is updated.
`outfit remote metrics` and `outfit remote status` show the figure once the
control plane is redeployed (`pnpm deploy`); until then both render exactly as
they do today, because an old Lambda omits the fields and the CLI omits the
line.

The two Lambdas are independent — the stats Lambda serves `remote metrics`,
the start Lambda serves `remote status` — so a partial deploy shows the figure
in one command and not the other. That is untidy but harmless, and a normal
`pnpm deploy` updates both.

Rollback is reverting the binary and, if desired, the control plane — there is
no persisted state to unwind.
