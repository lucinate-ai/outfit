## Context

`outfit remote start` runs two independent output paths that both write through
`startProgress.line` (`cmd/outfit/remote.go`):

1. A **heartbeat goroutine** (`newStartProgress`) with its own 30s `time.Ticker`
   that prints `still starting (Ns elapsed)`. It knows only a start timestamp —
   it never sees the endpoint's state.
2. The **polling loop** `remote.Start` (`internal/remote/remote.go`), which
   re-POSTs to the start URL each iteration and, on a 503, calls the progress
   callback with `instance <state>; retrying in <n>s` before sleeping.

The server returns `state: "no-capacity"` with a 503 when the start Lambda
could not launch an instance in any AZ (`remote/lambda/start/index.ts`). In that
window nothing is booting, yet the heartbeat keeps printing "still starting"
because it has no view of the state the polling loop just saw.

The two writers are already serialised by a mutex, so the only missing piece is
letting the heartbeat read the latest state. Constraints: progress stays on
stderr and exports on stdout; keep `go test ./... -cover` at/above 80%; gofmt.

## Goals / Non-Goals

**Goals:**
- The periodic heartbeat reflects the most recent poll: "waiting for capacity"
  when the last response was `no-capacity`, "still starting" when booting or
  before the first poll returns.
- No change to the retry-notice line, the stdout exports, or the retry cadence.
- Keep the two-writer, single-mutex structure; no new goroutine coordination.

This change also adds a `-t` short alias for the existing `--timeout` flag. That
is mechanically trivial — register the same `timeout` variable under a second
name on the flag set — and needs no architectural decision; it is called out
only so the proposal, spec, and tasks stay in step.

**Non-Goals:**
- No change to `remote.Start`'s retry logic or the server Lambda.
- No new heartbeat wording for other 503 states (e.g. `quota-exceeded`,
  `no-ami`); those keep the generic "still starting" until a follow-up decides
  otherwise. Only `no-capacity` is singled out here.

## Decisions

**Decision: the heartbeat reads a shared last-seen state, updated by the polling
loop.** `startProgress` gains a mutex-guarded field holding the latest state the
polling loop observed. The heartbeat formats its line from that field:
`no-capacity` → "still waiting for capacity (Ns elapsed)"; anything else (or
unset) → "still starting (Ns elapsed)". The existing `mu` already guards `line`;
reuse it to guard the state field too.

- *Alternative — richer progress callback signature:* change the callback from
  `func(string)` to pass a structured state so the client can classify. Rejected
  as a wider blast radius: `remote.Start`'s contract and its tests would all
  change, for a display-only concern that belongs in the CLI layer.
- *Alternative — parse the state back out of the retry-notice string:* brittle
  string-matching on a line meant for humans. Rejected.

**Decision: the polling loop tells the client the state directly.** Rather than
have the heartbeat guess, `remote.Start` reports each poll's state to the client.
Two viable wirings; pick whichever keeps `remote.Start`'s existing progress
callback untouched:

- Have `cmdRemoteStart` set the last-seen state from within the progress
  callback it already passes (parsing avoided by having the callback receive the
  state) — preferred if it stays a display concern in `cmd/outfit`.
- Or add a small, optional state-observer hook alongside the existing progress
  callback. Only if the first proves awkward.

The implementer chooses at coding time; the spec only requires the displayed
line reflect the latest state.

**Decision: keep "still starting" as the default.** Before the first poll
returns and for any non-`no-capacity` state, the wording is unchanged, so the
common cold-start path reads exactly as before.

## Risks / Trade-offs

- **A stale last-seen state after capacity is found** → When a retry finally
  launches an instance, the next poll's state overwrites `no-capacity`, so the
  heartbeat flips back to "still starting" on the following tick. The one-tick
  lag (≤30s) is acceptable for a cosmetic line.
- **Test brittleness on exact wording** → `remote_deploy_test.go` asserts the
  "still starting" string and heartbeat count. Update it to cover both wordings
  driven by the mocked state, rather than loosening the assertion away.
- **Scope creep to other 503 states** → Deliberately limited to `no-capacity`;
  other states keep the generic line, avoiding a wording matrix nobody asked for.
