## 1. Dependency and log-source plumbing

- [ ] 1.1 Add `github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs` to go.mod and tidy
- [ ] 1.2 Create `internal/remote/logs.go` with a `logGroupPrefix` constant sited next to the existing `cloud-vm-llm` naming, the supported runner list, and helpers returning the engine group per runner and the boot group
- [ ] 1.3 Add a test asserting the Go runner list matches the runners the deploy path already accepts, so a new engine cannot be added on one side only
- [ ] 1.4 Define the `LogEvent` type (timestamp, source, instance, message, event id) and a `streamInstance` helper parsing `<env>/<instance-id>` stream names

## 2. Fetching from CloudWatch Logs

- [ ] 2.1 Define the fetch interface over `FilterLogEvents` and wire a real client built from `remote.LoadAWSConfig(ctx, cfg.Region)`
- [ ] 2.2 Implement paged fetch for one group: stream prefix `<env>/`, start time from the window, paging until exhausted or the in-process ceiling is hit
- [ ] 2.3 Implement multi-group fetch: both engine groups for `engine`, the boot group for `boot`, both for `all`; tolerate `ResourceNotFoundException` per group and fail only when every requested group is missing
- [ ] 2.4 Implement merge by timestamp with `eventId` tiebreak, `--instance` post-filter, and the trailing `--limit` cap that reports how many earlier events were omitted
- [ ] 2.5 Classify the actionable failures: empty `cfg.Environment`, all groups missing, access denied, and no matching events
- [ ] 2.6 Unit-test the above against a substituted fetcher — ordering, cap and omission count, instance filter, per-group not-found tolerance, all-missing error, access-denied message, empty result

## 3. The `logs` subcommand

- [ ] 3.1 Create `cmd/outfit/remote_logs.go` with `cmdRemoteLogs`, parsing `--source`, `--since`, `--limit`, `--instance`, `--follow`/`-f`, `--format` via `sortFlagsBeforeArgs`, and resolving the environment with `resolveRemoteConfig(outfitArg(fs))`
- [ ] 3.2 Validate flag values (`--source` one of engine/boot/all, `--format` one of text/json, positive `--since` and `--limit`) with messages matching the style of `remote metrics`
- [ ] 3.3 Implement text rendering: oldest first, timestamp per line, source/instance prefix only when more than one source or instance is present
- [ ] 3.4 Implement `--format json` emitting the structured events
- [ ] 3.5 Implement the follow loop: poll from the last seen timestamp less a small overlap, suppress already-printed event ids from a bounded set, cancel on SIGINT/SIGTERM and exit nil on a cancelled context, following `runMetricsWatch`
- [ ] 3.6 Add the `logs` case to `cmdRemote`, update its usage string and the unknown-subcommand error text
- [ ] 3.7 Add `logs` to the remote subcommand list in `cmd/outfit/complete.go`
- [ ] 3.8 Test the command end to end with a substituted fetcher: default engine source, boot and all, labelling on and off, json output, flag validation, instance filter, and a follow loop that terminates on a cancelled context without duplicates

## 4. Documentation

- [ ] 4.1 Document `outfit remote logs` and its flags in `docs/commands/remote.md`, including the `logs:FilterLogEvents` permission the caller needs
- [ ] 4.2 Update the `outfit remote <...>` usage line in `README.md` and mention reading logs in the remote section
- [ ] 4.3 Note in the docs that logs remain readable after an instance terminates, and what the "re-deploy the shared layer" error means

## 5. Verification

- [ ] 5.1 `gofmt -l .` clean and `go build ./...` passes
- [ ] 5.2 `go test ./... -cover` passes with coverage at or above 80%
- [ ] 5.3 Manually verify against a real environment: engine logs for a running instance, boot logs, `--follow` during a start, and logs for an environment whose instance has terminated
- [ ] 5.4 Run `openspec validate add-remote-logs-command` and confirm the spec's scenarios are all covered by tests or the manual check
