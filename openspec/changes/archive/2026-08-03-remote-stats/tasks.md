## 1. Lambda: stats handler

- [x] 1.1 Create `remote/lambda/stats/index.ts` with SSM commands: `curl /metrics`, `nvidia-smi`, `vmstat`/`free`
- [x] 1.2 Reuse `metricsGrepPattern` and `parseMetrics` from `lambda/shared/idle.ts` to extract token/request metrics
- [x] 1.3 Parse `nvidia-smi` output for per-GPU model, utilization, memory used/total
- [x] 1.4 Parse `vmstat` for CPU utilization and `free` for RAM used/total
- [x] 1.5 Read API key from Secrets Manager (for llama.cpp auth on `/metrics`)
- [x] 1.6 Return JSON response with all metrics, instance metadata, and state
- [x] 1.7 Handle stopped/undeployed instance — return early with state only
- [x] 1.8 Add unit tests for metric parsing (extend `test/idle.test.ts` or create `test/stats.test.ts`)

## 2. CDK: wire Lambda into stack

- [x] 2.1 Add `StatsFn` NodejsFunction to `remote/lib/llm-stack.ts`
- [x] 2.2 Grant IAM permissions: SSM SendCommand (tag-scoped), SecretsManager GetSecretValue (env-scoped), EC2 DescribeInstances (tag-scoped)
- [x] 2.3 Add Function URL with IAM auth (`addFunctionUrl`)
- [x] 2.4 Add CDK output for `StatsUrl`
- [x] 2.5 Include `StatsUrl` in the `OutfitRemoteConfig` JSON output (or add `stats_url` to `remote.Config`)

## 3. Go client: remote package

- [x] 3.1 Add `StatsURL` field to `remote.Config` struct
- [x] 3.2 Add `Stats` function to `internal/remote/remote.go` — SigV4-signed POST to stats URL
- [x] 3.3 Define `StatsResponse` struct matching the Lambda's JSON reply
- [x] 3.4 Add `--cost` flag handling: invoke AWS Price List API to get on-demand price for instance type in region
- [x] 3.5 Add tests for the `Stats` client (mock HTTP server in `remote_test.go`)

## 4. CLI: stats command

- [x] 4.1 Add `stats` case to `cmdRemote` dispatch in `cmd/outfit/remote.go`
- [x] 4.2 Implement `cmdRemoteStats`: parse `--cost` flag, call `remote.Stats`, render tabular output
- [x] 4.3 Format output: key-value table with aligned columns, per-GPU lines, aggregate for multi-GPU
- [x] 4.4 Handle stopped instance — show environment and state, no metrics
- [x] 4.5 Add `stats` to `cmd/outfit/complete.go` completions for `remote` subcommand
- [x] 4.6 Add tests in `cmd/outfit/remote_test.go` for dispatch and output formatting

## 5. Integration & verification

- [x] 5.1 Run `go test ./... -cover` — verify >= 80% coverage
- [x] 5.2 Run `go vet ./...` — verify no issues
- [x] 5.3 Run `gofmt -w ./...` — verify formatting
- [x] 5.4 Run `pnpm test` in `remote/` — verify Lambda tests pass