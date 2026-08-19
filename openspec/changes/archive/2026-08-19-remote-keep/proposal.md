## Why

The idle sweep can be prevented from terminating an instance via the `Retain-Until` EC2 tag, but there is no CLI command to set it. Users who want to keep an instance alive for a known period — debugging overnight, a long-running task — must manually tag the instance from the AWS console.

## What Changes

- Add `outfit remote keep DURATION` subcommand that sets the `Retain-Until` tag on the current environment's instance to `now + DURATION` (e.g. `4h`, `60m`).
- Add `--keep DURATION` flag to `outfit remote start` that sets the same tag at wake time, so the instance is retained from the moment it boots.
- Add `retainUntil` to the `status` output when the tag is present, so the user can see the active retention deadline.

## Capabilities

### New Capabilities
- `remote-keep`: The `keep` subcommand and `start --keep` flag for setting the instance retention deadline. Defines duration parsing, tag application, and status reporting of an active retention.

### Modified Capabilities
- `remote-endpoint`: Adds `keep` to the recognised subcommands and documents `--keep` on `start`. Adds `retainUntil` to `status` output.
- `endpoint-lifecycle`: Clarifies how the retention override tag is set from the CLI — it was previously an undocumented console operation.

## Impact

- **CLI** (`cmd/outfit/remote.go`): New `cmdRemoteKeep` function; `--keep` flag on `cmdRemoteStart`; `retainUntil` line in `cmdRemoteStatus`.
- **Go client** (`internal/remote/remote.go`): New `Keep` function; optional `retainUntil` parameter on `Start`.
- **Lambda** (`remote/lambda/update/index.ts`): New `UpdateFn` Lambda with a `cmd` dispatch table; first command `set-keep` tags the instance with `Retain-Until`.
- **Lambda** (`remote/lambda/start/index.ts`): Reads `retainUntil` query parameter and tags the instance on wake.
- **CDK** (`remote/lib/llm-stack.ts`): New `UpdateFn` Function URL, IAM role with `ec2:CreateTags` and `ec2:DescribeInstances`.
- **Tests**: CLI dispatch, Lambda handler paths, Go client function.
