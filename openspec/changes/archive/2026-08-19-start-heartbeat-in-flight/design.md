## Context

`outfit remote start` shows progress through two writers (`cmd/outfit/remote.go`):
a heartbeat goroutine that repeats a line every 30s, and per-poll retry notices
from the polling loop `remote.Start` (`internal/remote/remote.go`). The
heartbeat's wording comes from a shared last-seen state that `Start` reports
through its `onState` observer — but only **when a poll returns a response**
(the contract added by the archived `state-aware-start-heartbeat` change).

The start Lambda does not co-operate with that model. It returns early only for
refusals (`no-capacity` within seconds of trying each zone, `quota-exceeded`,
`no-ami`, unconfigured/undeployed). Once it has found capacity it holds the one
request open through EC2 boot, engine start and weight load, and replies 200
only when the model is serving. So the attempt that finally finds capacity
never calls `onState` at all — until it succeeds. The last-seen state stays
`no-capacity` for the whole boot, and the heartbeat says "still waiting for
capacity" for minutes. The earlier design recorded this as an accepted
one-tick (≤30s) lag; the lag actually lasts until ready.

Constraints: progress goes to stderr and exports to stdout; the heartbeat and
the retry notices share one serialising mutex; `go test ./... -cover` stays at
or above 80%; no change to the Lambda or to the retry cadence.

## Goals / Non-Goals

**Goals:**
- The periodic line reflects the situation the client is actually in: an
  attempt in flight reads as starting; a no-capacity reply reads as waiting
  for capacity only while the client is still between attempts.
- The state observer's contract stays a single `func(string)`; the fix lives
  in `internal/remote` and needs no new display logic in the CLI.

**Non-Goals:**
- No change to the per-poll retry notice, the stdout exports, the retry wait,
  or the start Lambda.
- No new wording for other 503 states (`quota-exceeded`, `no-ami`, …); they
  keep the generic "still starting" line.
- No polling of the status endpoint (GET) from the heartbeat to learn the
  instance's EC2 state.

## Decisions

**Decision: report the in-flight attempt through the existing `onState`
observer, using a sentinel state the endpoint itself never reports.** `Start`
already has exactly the right observer hook; extending its contract — "called
with the raw state of every poll that returns a response, and with the
in-flight sentinel when a new attempt is issued" — keeps the signature stable.
The sentinel is a package-level constant (e.g. `StateInFlight = "in-flight"`)
documented as client-side only: no Lambda reply carries it.

- *Alternative — a second callback (`onAttempt func()`):* rejected; it widens
  `Start`'s signature (one production call site plus the test suite) for one
  boolean fact that the state channel already carries, and splits the
  observer's knowledge across two hooks.
- *Alternative — sentinel as the empty string:* rejected; `""` currently means
  "nothing observed yet", and conflating it with "an attempt is in flight"
  would make the observer's log read ambiguously in tests.
- *Alternative — a time threshold ("no reply for 60s ⇒ booting"):* rejected;
  it guesses at server internals from the client, which is the same class of
  mistake this codebase has already paid for (judging readiness from the
  daemon's word instead of the engine answering).

**Decision: emit the sentinel at the top of `Start`'s loop, before each
`call`.** That single spot covers every re-issue: the first attempt, the retry
after a 503 refusal, and the re-attach after a dropped connection. After a
no-capacity reply the client sleeps the requested wait — the sentinel fires
only when the next request actually goes out, so the heartbeat keeps saying
"waiting for capacity" through the wait itself, where no instance is booting.

**Decision: the CLI display logic is unchanged.** `startProgress.heartbeat`
already renders any non-`no-capacity` state as "still starting (Ns elapsed)";
the sentinel flows through the existing `setState` and renders correctly. The
spec pins the observable line, so if the CLI ever special-cases states
differently, the heartbeat tests (which drive `setState` directly) catch it
without this change touching `cmd/outfit` at all.

## Risks / Trade-offs

- **Reverse-direction mis-description for one tick** → While a new attempt is
  still probing zones, the line says "still starting", and if that attempt
  meets no capacity again it flips back to "waiting for capacity" as soon as
  the refusal returns (seconds, not the minutes of the previous bug). Bounded
  by the zone-probe time, self-correcting, and accepted as the honest reading
  of "a start request is in flight".
- **The sentinel collides with a real endpoint state** → The constant's value
  is one no Lambda reply carries, and the heartbeat treats only `no-capacity`
  specially, so even a future state with the same spelling would degrade to
  the generic "still starting" line, never to a wrong capacity claim.
- **Observer contract drift** → `TestStart_ReportsEachPollState` asserts the
  exact reported sequence; it updates to the new sequence (sentinel around
  each attempt) rather than loosening, and a new test covers the
  no-capacity → in-flight → ready sequence that is the shape of the bug.
- **Stale docs** → `Start`'s doc comment is updated in the same commit as the
  behaviour, so the contract change is visible at the call site.

## Migration Plan

Pure client-side display change in one release; no config, no data, no server
change. Rollback is reverting the commit — old binaries show the stale
capacity line as they do today.
