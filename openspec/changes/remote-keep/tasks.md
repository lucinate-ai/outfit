## 1. Lambda: UpdateFn — new Lambda for arbitrary instance commands

- [x] 1.1 Create `remote/lambda/update/index.ts` with a handler that dispatches on the `cmd` query parameter
- [x] 1.2 Implement `set-keep` command: resolve the environment instance, read the `retainUntil` query parameter, validate it as ISO-8601, call `tagInstance(instance.instanceId, RETAIN_UNTIL_TAG, retainUntil)`, return the deadline
- [x] 1.3 Handle missing instance: return error when no instance is found for the environment
- [x] 1.4 Handle missing/invalid `retainUntil` param: return 400 with a clear error
- [x] 1.5 Handle unknown `cmd` value: return 400 naming the accepted commands

## 2. Lambda: start Lambda — retainUntil query param

- [x] 2.1 In `remote/lambda/start/index.ts`, add `RETAIN_UNTIL_TAG` import from `shared/aws.ts`
- [x] 2.2 Parse optional `retainUntil` query parameter in the `wake` handler
- [x] 2.3 After the instance is running (post health check), if `retainUntil` was provided, call `tagInstance(instanceId, RETAIN_UNTIL_TAG, retainUntil)` — best-effort (log error, do not fail the wake)
- [x] 2.4 Include `retainUntil` in the `ready` response when the tag was set

## 3. CDK: UpdateFn Lambda and Function URL

- [x] 3.1 In `remote/lib/llm-stack.ts`, add `UpdateFn` (NodejsFunction) pointing to `remote/lambda/update/index.ts`
- [x] 3.2 Add `ec2:CreateTags` permission (broad ARN — no tag scoping available for this action) and `ec2:DescribeInstances` (to resolve the instance by tag)
- [x] 3.3 Add Function URL with `AWS_IAM` auth and expose the URL as a stack output (`UpdateUrl`)
- [x] 3.4 Include `update_url` in the `OutfitRemoteConfig` output JSON

## 4. Go client: Keep function

- [x] 4.1 In `internal/remote/remote.go`, add `UpdateURL` to the `Config` struct and load it from the config file
- [x] 4.2 Add `Keep(ctx, cfg, retainUntil time.Time)` function that calls the update Lambda URL with `cmd=set-keep&retainUntil=<iso8601>` query params
- [x] 4.3 Parse the JSON response and return the deadline
- [x] 4.4 Modify `Start(ctx, cfg, progress, onState, retainUntil *time.Time)` to accept optional `retainUntil` parameter — when non-nil, append `retainUntil=<iso8601>` to the start URL query

## 5. CLI: keep subcommand

- [x] 5.1 In `cmd/outfit/remote.go`, add `keep` case to the `cmdRemote` switch
- [x] 5.2 Implement `cmdRemoteKeep(args)` — parse duration from positional arg using `time.ParseDuration`, compute `time.Now().Add(d)`, call `remote.Keep`, print the deadline
- [x] 5.3 Validate that the duration argument is present and valid
- [x] 5.4 Update the usage error message to include `keep` in the list of accepted subcommands

## 6. CLI: start --keep flag

- [x] 6.1 In `cmd/outfit/remote.go`, add `--keep`/`-k` flag to `cmdRemoteStart` accepting a duration string
- [x] 6.2 Parse the duration with `time.ParseDuration`, compute the deadline, pass to `remote.Start`
- [x] 6.3 Report the retention deadline in the output after the instance is ready

## 7. CLI: status retainUntil line

- [x] 7.1 In the Go `Response` struct (`internal/remote/remote.go`), add `RetainUntil string` field for the JSON response
- [x] 7.2 In `cmdRemoteStatus`, print the `retain_until` line when present, formatted as a human-readable duration or absolute time
- [x] 7.3 In the start Lambda (`remote/lambda/start/index.ts`), include `retainUntil` in the JSON reply when the instance has the tag

## 8. Tests

- [x] 8.1 Lambda update tests: `cmd=set-keep` sets the tag, missing `retainUntil` returns 400, no instance returns error, unknown `cmd` returns 400
- [x] 8.2 Lambda start tests: `retainUntil` query param tags the instance after wake
- [x] 8.3 Go client: test `Keep` function with httptest server; test `Start` with `retainUntil`
- [x] 8.4 CLI tests: `keep` subcommand dispatch, duration parsing, error cases
- [x] 8.5 Run `go test ./... -cover` to verify coverage remains >= 80%

## 9. Verification

- [x] 9.1 Run `go build ./...` to ensure compilation
- [x] 9.2 Run `go vet ./...` to check for issues
- [x] 9.3 Run `go test ./... -cover` to verify tests and coverage
- [x] 9.4 Run remote/ tests: `pnpm test` (if applicable to changed Lambda files)
