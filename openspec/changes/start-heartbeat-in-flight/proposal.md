## Why

When `outfit remote start` hits a `no-capacity` reply and retries, the next
attempt — the one that finally finds capacity — holds its request open while
the instance boots, and reports no state until it is ready. The CLI's periodic
progress line keys off the most recently *reported* state, so it keeps saying
"waiting for capacity" for the entire multi-minute boot, misdescribing exactly
the window a user is most interested in. The earlier state-aware-heartbeat
change accepted this as a one-tick lag; in practice it lasts until ready.

## What Changes

- `remote.Start` tells its state observer when a new attempt is issued, not
  only when a poll returns: a start request in flight means the endpoint has
  not reported a refusal for this attempt, and a no-capacity refusal returns
  within seconds of trying each zone — a successful attempt instead blocks for
  minutes while the instance boots.
- The periodic progress line therefore reads as starting while an attempt is
  in flight, and reads as waiting for capacity only while actually between
  attempts after a no-capacity reply. The per-poll retry notices are unchanged.
- `startProgress` (the CLI's heartbeat) already renders every non-`no-capacity`
  state as "still starting", so the fix is a contract change in
  `internal/remote` plus the observer call; no new display logic.

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities
- `remote-endpoint`: the "Reporting a start in progress" requirement changes —
  the periodic line must reflect the situation the client is actually in,
  including an attempt that is in flight, not only the state of the most
  recent returned poll.

## Impact

- `internal/remote/remote.go` — `Start`'s state-observer contract gains an
  in-flight notice (a sentinel state the endpoint itself never reports) and a
  call at the start of each attempt.
- `internal/remote/remote_test.go` — the onState contract test updates to the
  new sequence, and gains coverage for the capacity-wait-then-boot sequence.
- `cmd/outfit/remote.go` — no change expected (the heartbeat already renders
  the sentinel correctly); verified by the existing heartbeat tests.
- No change to the Lambda, the retry cadence, the retry-notice wording, the
  stdout exports, or any other command.
- Tests: `go test ./... -cover` stays at/above 80%; gofmt.
