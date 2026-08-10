## Why

Instances already ship their engine and boot logs to CloudWatch Logs, but there
is no way to read them from outfit — you have to open the AWS console or reach
for the AWS CLI and know the group and stream naming by heart. The logs matter
most exactly when the instance is gone (a start that failed before the engine
came up, a crash that ended the instance), which is when `outfit remote status`
and `outfit remote metrics` have nothing left to tell you.

## What Changes

- Add an `outfit remote logs [path]` subcommand that prints an environment's
  shipped logs, selecting the environment the same way the other remote
  commands do (an Outfit's `REMOTE`, `./Outfit`, or the `default` environment).
- Read the logs straight from CloudWatch Logs with the caller's own AWS
  credentials — the same credentials that already sign the control Lambda
  calls — so logs are readable whether or not the instance still exists, and no
  redeploy of the shared layer is needed.
- Cover both log sources: the engine log and the boot (user-data) log, chosen
  with `--source engine|boot|all`, defaulting to the engine log.
- Bound what is fetched with `--since <duration>` and `--limit <n>`, narrow to
  one instance with `--instance <id>`, and stream new lines with
  `--follow`/`-f`.
- Print events oldest-first with their timestamp, tagging each line with its
  source and instance when more than one is in play; `--format json` emits the
  raw events for scripting.
- Report the fixable causes plainly: an environment whose config predates
  environment names, a shared layer deployed before log shipping existed
  (no log group), missing `logs:FilterLogEvents` permission, and an
  environment that has simply never produced logs.
- Extend shell completion and the `outfit remote` usage line with the new
  subcommand, and document it in `docs/commands/remote.md` and the README.

## Capabilities

### New Capabilities

- `remote-logs`: reading an environment's shipped engine and boot logs from the
  CLI — which environment and sources are read, how the output is bounded and
  ordered, following live output, and the failure messages that tell the
  operator what to fix.

### Modified Capabilities

None. `shell-completion` already requires that every dispatched subcommand is
offered, so adding `logs` to the completion surface satisfies the existing
requirement rather than changing it.

## Impact

- `cmd/outfit/remote.go`: new `logs` case in `cmdRemote`, its usage string, and
  the unknown-subcommand error.
- New `cmd/outfit/remote_logs.go` (flag parsing, rendering, follow loop) plus
  tests.
- New `internal/remote/logs.go`: log group naming by convention
  (`/cloud-vm-llm/<runner>`, `/cloud-vm-llm/boot`), stream prefix `<env>/`, and
  the CloudWatch Logs fetch, behind an interface tests can substitute.
- `cmd/outfit/complete.go`: add `logs` to the remote subcommand list.
- `go.mod`: adds `github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs`.
- Docs: `docs/commands/remote.md`, `README.md`, and `docs/env-vars.md` if a new
  variable is introduced.
- No change to `remote/` infrastructure: the groups, streams and retention it
  already creates are what this reads.
