## Why

When the Lambda calls EC2 `StopInstances`, the running inference engine (vLLM, llama.cpp, or any future engine) blocks the instance shutdown, leaving it stuck in `stopping` for up to 12 minutes before EC2 force-kills it. A force-stop is unreliable (sometimes it never completes) and leaves the instance in a state where re-wake can fail. We need a mechanism that works for all supported engines to ensure a clean, fast stop.

## What Changes

- The stop Lambda (both manual pause/terminate and idle sweep) now stops the engine via the on-instance daemon's API (`POST /v1/stop`) before calling EC2 `StopInstances`.
- The daemon's existing SIGTERM handler (which calls `sup.Stop()`) ensures the engine exits cleanly after the API call.
- The Lambda's stop call is best-effort: if the daemon is unreachable, the Lambda proceeds with the EC2 stop as before, rather than failing the operation.
- A new shared helper (`stopEngineDaemon`) in `shared/aws.ts` wraps the SSM command, used by both manual and idle stop paths.

## Capabilities

### Modified Capabilities

- `endpoint-lifecycle`: The stop phase now includes an engine shutdown step via the daemon API before EC2 `StopInstances`. Works for any supported engine; daemon failure is handled gracefully (proceeds with EC2 stop anyway).

## Impact

- `remote/lambda/shared/aws.ts` — new `stopEngineDaemon` helper
- `remote/lambda/stop/index.ts` — `pauseInstance` and `idleCheck` call the helper before `stopInstance`
- `remote/lambda/shared/daemon.ts` — new `DAEMON_STOP_CMD` constant
- Tests: stop.test.ts needs to cover the graceful stop path (engine stops, then EC2 stops; daemon unreachable, EC2 still stops)
