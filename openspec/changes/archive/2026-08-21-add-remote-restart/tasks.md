## 1. Control plane: the stop Lambda honours force

- [x] 1.1 In `remote/lambda/stop/index.ts`, read an optional `force` query parameter (in effect exactly when `force=true`, matching the `action === 'pause'` style) and, in the pause path, skip `stopEngineDaemon` when forced while keeping the stop-time tag, `stopInstance` and the `stopping`/`stopped` replies unchanged
- [x] 1.2 In the terminate path, skip `stopEngineDaemon` when forced while keeping `terminateInstance` and the `terminating` reply unchanged; the idle sweep path never passes the parameter and keeps its behaviour
- [x] 1.3 Update the stop Lambda's doc comment describing the query parameters so `force` is documented beside `action`
- [x] 1.4 In `remote/test/stop.test.ts`: a forced pause skips `stopEngineDaemon` yet still tags and stops (reply `stopping`), a forced terminate skips the engine stop yet still terminates, a forced already-stopped pause is still the noop, and the existing unforced and sweep tests pass unchanged

## 2. Transport: forced pause and restart in `internal/remote`

- [x] 2.1 Give `Pause` a force argument, have `pauseURL` append `force=true` when it is set, and update `runRemotePause` to pass `false` so `outfit remote pause` is unchanged
- [x] 2.2 Add `Restart`: make the pause-style stop (with force when requested); a failed stop returns without waking; then call the existing `Start` with the caller's progress and state callbacks and no retention, and when the wake fails after the stop took effect, wrap the error to say the instance is stopped and that `outfit remote start` will bring it back
- [x] 2.3 In `internal/remote/remote_test.go`: cover the forced and unforced pause query strings against an `httptest` server, and `Restart`'s flows — stop-then-wake success, stop failure never waking, and wake failure after a stop producing the recovery hint

## 3. CLI: the `remote restart` command

- [x] 3.1 Add `remoteRestartCmd` and `runRemoteRestart` in `cmd/outfit/remote.go`: a `--force` flag with a `-F` short form, a `--timeout` flag with a `-t` short form defaulting to 15 minutes as `start` does, an optional Outfit path resolved by `resolveRemoteConfig`, and `aliasSlot` completions
- [x] 3.2 In `runRemoteRestart`: a lenient status call whose reply only feeds a progress line (running, or already stopped / no instance), reuse of the start progress heartbeat for the wait, a progress line up front naming the stop phase and, under `--force`, that the graceful engine stop is skipped, and on success the elapsed time and the base URL
- [x] 3.3 Register `remoteRestartCmd()` in `remoteCmd()` (between `pause` and `stop`) and add `restart` to both lines of `remoteParentFallback` — the `usage:` line and the unknown-subcommand list
- [x] 3.4 Add the `cmdRemoteRestart` test seam and, in `cmd/outfit/remote_test.go`: dispatch through the tree, flag parsing (`--force`, `-F`, `--timeout`), the unknown-subcommand list naming `restart`, and a full flow against an `httptest` server proving the stop call carries `action=pause` with `force=true` exactly when `-F` was given, the stop succeeds and the wake reaches ready, and a restart of an already-stopped environment behaves as a start
- [x] 3.5 Confirm the completion coverage test (`TestCompletionCoversTree`) passes with the new subcommand in the tree

## 4. Documentation

- [x] 4.1 In `docs/commands/remote.md`, add `restart` to the command list and a short section: what a restart does (stop without terminating, wake, block until serving), that the address and weights are unchanged, and what `--force` skips
- [x] 4.2 In AGENTS.md, add `restart` (with the forced-stop note) to the description of the `remote` command group

## 5. Verification

- [x] 5.1 `gofmt -w ./...`, `go vet ./...`, and `go test ./... -cover` with total coverage still at or above 80%
- [x] 5.2 In `remote/`, run the vitest suite and confirm the stop Lambda tests (forced and unforced) pass
