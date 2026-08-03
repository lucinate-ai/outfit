## Why

When running a remote instance there's no visibility into what it's doing or how much it's costing. The idle Lambda scrapes `/metrics` every 5 minutes, but that data is discarded — used only for the stop-or-not decision. A `stats` command lets the user see token counts, resource usage, and (optionally) cost at any time.

## What Changes

- Add `outfit remote stats` subcommand that reports instance state, token usage, CPU/RAM/GPU utilization, and optionally cost
- Add a new Lambda (`lambda/stats`) that runs shell commands on the instance via SSM to scrape `/metrics`, `nvidia-smi`, and `vmstat`
- Wire the new Function URL into the CDK stack (`llm-stack.ts`) and expose it as a stack output
- Add the Go client (`internal/remote`) and CLI handler (`cmd/outfit/remote.go`)
- Add `--cost` flag to `stats` that invokes the AWS Price List API to compute an estimated cost for the current running session

## Capabilities

### New Capabilities

- `remote-stats`: The `outfit remote stats` command — Lambda relay, metric parsing, tabular output, per-GPU reporting, optional cost estimation

### Modified Capabilities

- `remote-endpoint`: Adds `stats` to the recognised subcommands of the `remote` command group

## Impact

- `cmd/outfit/remote.go` — new `cmdRemoteStats`, `stats` case in dispatch switch
- `cmd/outfit/complete.go` — new `remote stats` completion entries
- `internal/remote/remote.go` — new `Stats` client function
- `remote/lambda/stats/` — new Lambda (TypeScript)
- `remote/lib/llm-stack.ts` — new Function URL, IAM policy, CDK output
- `remote/lambda/shared/idle.ts` — metrics parsing reused (possibly extracted)
- Tests: `remote_test.go` (Go), `idle.test.ts` (TypeScript, extended)