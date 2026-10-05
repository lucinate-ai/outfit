## Why

A remote environment only starts when someone asks for it and is stopped by the idle sweep. A team that works office hours has to start it by hand each morning and pay for the idle time before the sweep stops it each evening. Issue 178 asks for cloud nodes to start and stop on one or more cron schedules.

## What Changes

- Each remote environment can hold a list of schedules. A schedule is a cron expression, a time zone and an action: `start` or `stop`.
- The schedules are stored and run in the AWS control plane (`remote/`), so they fire with no machine of the user's switched on. EventBridge Scheduler runs them.
- A new schedule Lambda accepts a replacement list of schedules for one environment, returns the current list, and clears it.
- A scheduled start runs the same start as `spinloop remote start`. A scheduled stop pauses the instance (stops it without terminating it), unless the instance has a `Retain-Until` deadline in the future, in which case the stop is skipped.
- New CLI commands: `spinloop remote schedule set`, `show` and `clear`.
- `spinloop remote schedule show` (and `set`) report the next scheduled start and stop when the environment has schedules.

## Capabilities

### New Capabilities

- `remote-schedule`: storing, running and reporting the cron schedules that start and stop a remote environment.

### Modified Capabilities

None. The existing start, stop and keep behaviour is reused unchanged; a scheduled run calls the same code paths.

## Impact

- `remote/`: new schedule Lambda and Function URL, an EventBridge Scheduler schedule group and a role the schedules assume, new handling in the start and stop Lambdas for a scheduled invocation, new stack output and `SpinloopRemoteConfig` field (`schedule_url`).
- `internal/remote` and `cmd/spinloop/remote.go`: the client calls and the `schedule` command group, which prints the next runs.
- `docs/`: command reference, remote guide and the control plane's HTTP API.
- The remote CLI user's IAM policy gains permission to invoke the schedule Lambda. Existing deployments need `spinloop remote bootstrap` re-run to get the feature; the CLI reports a clear error when `schedule_url` is missing.
