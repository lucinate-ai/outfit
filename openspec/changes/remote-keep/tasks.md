## 1. Lambda: UpdateFn — new Lambda for arbitrary instance commands

- [ ] 1.1 Create `remote/lambda/update/index.ts` with a handler that dispatches on the `cmd` query parameter
- [ ] 1.2 Implement `set-keep` command: resolve the environment instance, read the `retainUntil` query parameter, validate it as ISO-8601, call `tagInstance(instance.instanceId, RETAIN_UNTIL_TAG, retainUntil)`, return the deadline
- [ ] 1.3 Handle missing instance: return error when no instance is found for the environment
- [ ] 1.4 Handle missing/invalid `retainUntil` param: return 400 with a clear error
- [ ] 1.5 Handle unknown `cmd` value: return 400 naming the accepted commands

## 2. Lambda: start Lambda — retainUntil query param

- [ ] 2.1 In `remote/lambda/start/index.ts`, add `RETAIN_UNTIL_TAG` import from `shared/aws.ts`
- [ ] 2.2 Parse optional `retainUntil` query parameter in the `wake` handler
- [ ] 2.3 After the instance is running (post health check), if `retainUntil` was provided, call `tagInstance(instanceId, RETAIN_UNTIL_TAG, retainUntil)` — best-effort (log error, do not fail the wake)
- [ ] 2.4 Include `retainUntil` in the `ready` response when the tag was set

## 3. CDK: UpdateFn Lambda and Function URL

- [ ] 3.1 In `remote/lib/llm-stack.ts`, add `UpdateFn` (NodejsFunction) pointing to `remote/lambda/update/index.ts`
- [ ] 3.2 Add `ec2:CreateTags` permission (broad ARN — no tag scoping available for this action) and `ec2:DescribeInstances` (to resolve the instance by tag)
- [ ] 3.3 Add Function URL with `AWS_IAM` auth and expose the URL as a stack output (`UpdateUrl`)
- [ ] 3.4 Include `update_url` in the `OutfitRemoteConfig` output JSON

## 4. Go client: Keep function

- [ ] 4.1 In `internal/remote/remote.go`, add `UpdateURL` to the `Config` struct and load it from the config file
- [ ] 4.2 Add `Keep(ctx, cfg, retainUntil time.Time)` function that calls the update Lambda URL with `cmd=set-keep&retainUntil=<iso8601>` query params
- [ ] 4.3 Parse the JSON response and return the deadline
- [ ] 4.4 Modify `Start(ctx, cfg, progress, onState, retainUntil *time.Time)` to accept optional `retainUntil` parameter — when non-nil, append `retainUntil=<iso8601>` to the start URL query

## 5. CLI: keep subcommand

- [ ] 5.1 In `cmd/outfit/remote.go`, add `keep` case to the `cmdRemote` switch
- [ ] 5.2 Implement `cmdRemoteKeep(args)` — parse duration from positional arg using `time.ParseDuration`, compute `time.Now().Add(d)`, call `remote.Keep`, print the deadline
- [ ] 5.3 Validate that the duration argument is present and valid
- [ ] 5.4 Update the usage error message to include `keep` in the list of accepted subcommands

## 6. CLI: start --keep flag

- [ ] 6.1 In `cmd/outfit/remote.go`, add `--keep`/`-k` flag to `cmdRemoteStart` accepting a duration string
- [ ] 6.2 Parse the duration with `time.ParseDuration`, compute the deadline, pass to `remote.Start`
- [ ] 6.3 Report the retention deadline in the output after the instance is ready

## 7. CLI: status retainUntil line

- [ ] 7.1 In the Go `Response` struct (`internal/remote/remote.go`), add `RetainUntil string` field for the JSON response
- [ ] 7.2 In `cmdRemoteStatus`, print the `retain_until` line when present, formatted as a human-readable duration or absolute time
- [ ] 7.3 In the start Lambda (`remote/lambda/start/index.ts`), include `retainUntil` in the JSON reply when the instance has the tag

## 8. Tests

- [ ] 8.1 Lambda update tests: `cmd=set-keep` sets the tag, missing `retainUntil` returns 400, no instance returns error, unknown `cmd` returns 400
- [ ] 8.2 Lambda start tests: `retainUntil` query param tags the instance after wake
- [ ] 8.3 Go client: test `Keep` function with httptest server; test `Start` with `retainUntil`
- [ ] 8.4 CLI tests: `keep` subcommand dispatch, duration parsing, error cases
- [ ] 8.5 Run `go test ./... -cover` to verify coverage remains >= 80%

## 9. Verification

- [ ] 9.1 Run `go build ./...` to ensure compilation
- [ ] 9.2 Run `go vet ./...` to check for issues
- [ ] 9.3 Run `go test ./... -cover` to verify tests and coverage
- [ ] 9.4 Run remote/ tests: `pnpm test` (if applicable to changed Lambda files)
