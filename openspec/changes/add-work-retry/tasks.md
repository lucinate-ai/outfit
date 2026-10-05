## 1. Work list API

- [ ] 1.1 Add `WorkList.Retry(id)` in `internal/orchestrator/worklist.go` and verify with unit tests: failed item becomes backlog and the state is saved, running/done/backlog items are refused with a conflict naming the state, an unknown id is a miss, the items file is unchanged
- [ ] 1.2 Add `POST /v1/items/{id}/retry` to `ServeHTTP`, `pathsServed` and a `handleRetry` in `internal/orchestrator/api.go`; verify in `api_test.go` for 200, 409, 404, wrong method, and token rules
- [ ] 1.3 Verify the loop re-admits a retried item on its next pass with a test in `orchestrator_test.go`

## 2. Command

- [ ] 2.1 Add `workRetryCmd` to `cmd/spinloop/work.go` and register it; verify in `work_test.go` for the request path, the success message, a refusal, a missing `--url`, and the argument count
- [ ] 2.2 Add `retry` to the work subcommands' item id completion and verify with the existing completion tests

## 3. Work board

- [ ] 3.1 Add the `workRetry` verb, the `t` key and its request and status line wording to `work_board_model.go`; verify in `work_board_test.go` that retrying a failed card sends the request and a refusal reaches the status line
- [ ] 3.2 Show `t retry` in `boardKeys` on a failed card only, in `work_board_render.go`; verify with a render test for failed, running, backlog and done cards

## 4. Docs and finish

- [ ] 4.1 Document the retry path in `docs/commands/orchestrator.md`, the `work retry` command and the board key in the work command docs
- [ ] 4.2 Run `gofmt`, `go vet ./...`, `go test ./... -cover` and `openspec validate add-work-retry --strict`, and verify all pass
