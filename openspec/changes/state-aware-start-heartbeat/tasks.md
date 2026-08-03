## 1. Surface the latest poll state to the heartbeat

- [x] 1.1 In `cmd/outfit/remote.go`, add a mutex-guarded last-seen-state field to
  `startProgress`, defaulting to empty (treated as "starting").
- [x] 1.2 Wire `remote.Start`'s per-poll state to that field so the heartbeat can
  read it — either by having the progress callback in `cmdRemoteStart` record the
  state, or via a small state-observer hook, keeping `remote.Start`'s existing
  progress-callback signature unchanged (see design.md).

## 2. Make the heartbeat wording state-aware

- [x] 2.1 Change the heartbeat goroutine so it formats "still waiting for capacity
  (Ns elapsed)" when the last-seen state is `no-capacity`, and "still starting
  (Ns elapsed)" otherwise (including before the first poll).
- [x] 2.2 Leave the retry-notice line (`instance <state>; retrying in <n>s`) and
  the stdout exports unchanged.

## 3. Short alias for the start timeout

- [x] 3.1 In `cmdRemoteStart` (`cmd/outfit/remote.go`), register `-t` as a second
  name bound to the same `timeout` variable as `--timeout`, keeping the 15m
  default and duration parsing (e.g. `-t 5m`).
- [x] 3.2 Confirm the `remote start` usage/help lists both `--timeout` and `-t`.

## 4. Tests

- [x] 4.1 Update `cmd/outfit/remote_deploy_test.go` so the mocked start endpoint
  can return a `no-capacity` 503, and assert the heartbeat says "waiting for
  capacity" during that window and "still starting" while booting.
- [x] 4.2 Add a test that `-t <dur>` sets the wait — e.g. a very short timeout
  makes a never-ready start fail promptly with the timeout error.
- [x] 4.3 Keep the existing assertions that progress goes to stderr, exports go to
  stdout, and the heartbeat count stays bounded.
- [x] 4.4 Run `gofmt` and `go test ./... -cover`, confirming coverage stays at or
  above 80%.
