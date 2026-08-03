## Why

During a cold start, `outfit remote start` prints a 30s heartbeat that always
reads "still starting (Ns elapsed)". When AWS has no GPU capacity in any zone,
the start Lambda launches nothing and returns `no-capacity`; the instance is not
booting at all — it is blocked waiting for capacity to exist. The heartbeat has
no knowledge of this state, so it reports "still starting" throughout, which
misleads the user into thinking a boot is underway when none is.

## What Changes

- The 30s heartbeat becomes state-aware: it reflects the most recent poll
  outcome instead of a fixed string.
- When the last poll returned a non-booting `no-capacity` response, the
  heartbeat SHALL report that it is waiting for capacity rather than "still
  starting".
- The heartbeat SHALL only say "still starting" once an instance is actually
  booting (a `starting` response, or before the first poll has returned).
- The retry-notice line (`instance no-capacity; retrying in 120s`) and the
  stdout exports are unchanged.
- `start` gains a short `-t` alias for the existing `--timeout` flag (which
  already sets the overall wait, defaulting to 15m), so a user can shorten the
  wait with e.g. `-t 5m`. The configurable timeout becomes a documented
  requirement rather than an undocumented flag.

## Capabilities

### New Capabilities

<!-- none -->

### Modified Capabilities

- `remote-endpoint`: the "Reporting a start in progress" requirement gains that
  the periodic progress line SHALL reflect the endpoint's most recently reported
  state, distinguishing waiting-for-capacity from booting. The "Remote command
  group" requirement gains that `start`'s overall wait SHALL be configurable via
  `--timeout`/`-t`, defaulting to 15m.

## Impact

- `cmd/outfit/remote.go`: the `startProgress` heartbeat goroutine and the
  progress callback wiring into `remote.Start`; and registering `-t` as a second
  name for the existing `--timeout` flag in `cmdRemoteStart`.
- `internal/remote/remote.go`: `Start` already surfaces each poll's state via
  the progress callback; the client needs the state made available to the
  heartbeat (not just formatted into the retry line).
- `cmd/outfit/remote_deploy_test.go`: the existing test asserts the "still
  starting" wording and heartbeat count; it needs updating for the state-aware
  wording.
- User-facing terminal output only; no config, API, or stdout-contract changes.
