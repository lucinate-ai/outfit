## 1. The logger and its level

- [x] 1.1 Add `internal/daemon/logging.go` with `ParseLevel(string) (slog.Level, error)` — an explicit case-insensitive switch over `debug|info|warn|error`, erroring on anything else with a message naming the accepted values
- [x] 1.2 Add a constructor building a `slog.Logger` over a text handler on a supplied writer at a supplied level, taking the writer as a parameter so a test can capture it and the command can pass `os.Stderr` at call time
- [x] 1.3 Add the nil-safe accessor both `Daemon` and `Supervisor` read their logger through, substituting a discarding logger so a directly-constructed daemon in a test stays silent
- [x] 1.4 Unit-test level parsing: each accepted spelling, mixed case, the empty string, and a rejected value whose error names the accepted set

## 2. The request-summary middleware

- [x] 2.1 Add a `ResponseWriter` wrapper capturing the status code and the bytes written, defaulting the status to 200 when a handler never calls `WriteHeader`, with a comment naming the `Flusher`/`Hijacker` omission for whoever adds a streaming endpoint
- [x] 2.2 Add the summariser middleware: measure the request, then emit one record with method, path (query included), status, duration, bytes and remote address — reading no headers, so no code path can render the token
- [x] 2.3 Grade the record by status: 2xx/3xx at info, 4xx at warn, 5xx at error
- [x] 2.4 Add `Logger` to `Daemon` and wrap in `Handler` as `summarize(logger, authenticated(token, mux))` — outermost, so 401s and unrouted 404s are summarised
- [x] 2.5 Test the middleware against the real handler with a captured buffer: a served request, a 401 with no token in the output, an unrouted path, a bad-input 400 landing at warn, and a summary emitted for every route in `Routes()`
- [x] 2.6 Test that the response a caller receives is byte-identical with the logger set and unset

## 3. Engine lifecycle records

- [x] 3.1 Add `Logger` to `Supervisor`; record the start with the command, and record the exit from inside the existing wait goroutine — `stopped` at info, `crashed` at error — so a crash is logged when it happens rather than when someone polls
- [x] 3.2 Record in `Daemon.StartEngine` what was resolved to serve and a start that failed with its reason, and record a stop
- [x] 3.3 Test lifecycle records with a stub process: a clean start and stop, a failed start naming the reason, and a crash landing at error severity

## 4. Wiring the commands

- [x] 4.1 Add `--log-level` to `outfit daemon`, resolving flag > `OUTFIT_LOG_LEVEL` > info, failing startup before the listener opens when the value is unrecognised
- [x] 4.2 Add the same flag to `outfit serve`, accepted with or without `--api` so one command line works both ways
- [x] 4.3 Build the logger from `os.Stderr` inside each command and assign it to both the `Daemon` and the `Supervisor` where they are constructed, on the `daemon` and `serve --api` paths
- [x] 4.4 Move the operational lines to the log at info — `daemon ready`, `engine log: …`, `control API on …`, and `Serving … from the pushed deploy config` — and leave serve's narration (the formatted command, `Using preset …`, `Serving … from <Outfit>`, everything `--dry-run` prints) on stdout untouched
- [x] 4.5 Rework `apiAddrFromStdout` in `cmd/outfit/serve_daemon_test.go` to read the redirected `os.Stderr`; without it the daemon tests hang for ten seconds and fail confusingly
- [x] 4.6 Add `--log-level` and its values to `cmd/outfit/complete.go` for both commands, and confirm `TestCompletionCoversDispatch` still passes
- [x] 4.7 Test the command wiring end to end: a daemon at the default level summarising a real request on stderr, the same daemon at `warn` staying silent for a success but recording a 401, the flag overriding the environment variable, and an unrecognised level failing before anything listens

## 5. Documentation

- [x] 5.1 Document `--log-level` in `docs/commands/serve.md` for both commands, in that file's voice, naming `warn` as the setting for a node a fleet polls
- [x] 5.2 Add `OUTFIT_LOG_LEVEL` to `docs/env-vars.md`, stating the flag-beats-variable precedence
- [x] 5.3 Describe the request summary in `docs/http-api.md`: the fields it carries, that bodies and the token never appear, and that severity is graded by status so `warn` keeps rejections and failures visible
- [x] 5.4 Say plainly that records go to stderr and nothing rotates them — where they end up is the service manager's business
- [x] 5.5 Update the `internal/daemon` entry in AGENTS.md to note the logger is injected and defaults to discarding, so a future test does not acquire a global one

## 6. Verification

- [x] 6.1 `gofmt -l .` clean, `go build ./...` and `go vet ./...` pass
- [x] 6.2 `go test ./... -cover` passes with total coverage still at or above 80%
- [x] 6.3 Confirm `internal/daemon/openapi_test.go` and `docs/openapi.yaml` needed no edit — the API surface was not to move, and the contract test passing untouched is the check
- [x] 6.4 `bash scripts/check-spec-purposes.sh` and `openspec validate add-daemon-request-logging --strict` pass
- [x] 6.5 Run the dockerised fleet example (`examples/fleet-docker/run-tests.sh`) to confirm real daemons under a real fleet poll still behave, and eyeball the volume of the summaries it produces
- [x] 6.6 Manually run `outfit daemon` and `outfit serve --api`: watch a start, a stop, a crash, a 401 and a poll at info, then repeat at `warn` and confirm only the failures remain
