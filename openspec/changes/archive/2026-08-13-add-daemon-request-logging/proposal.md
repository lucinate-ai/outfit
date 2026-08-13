## Why

The control API says almost nothing about itself. Under `outfit daemon` it
prints three lines at startup (`daemon ready`, the engine log path, the API
address); under `outfit serve --api` it prints one, and then both go silent for
the rest of the process's life. Every request that follows — a fleet client
polling status, a control-plane Lambda pushing a deploy config, a start that
failed, a 401 from a caller with the wrong token — leaves no trace at all. The
engine's output is captured and served over `/v1/logs`; what the API itself did
is recorded nowhere.

That gap is felt exactly when something is wrong. "The node says `idle` but I
started it" and "this token used to work" are both answerable in one line of
log and unanswerable without one. The counter-pressure is real too: a fleet
polling `/v1/status` every few seconds would turn an always-on request log into
noise, so a summary per request is only acceptable if the operator can turn the
volume down.

## What Changes

- Give outfit a **levelled logger** (`log/slog`, stdlib — outfit takes no
  runtime dependencies), writing to stderr, replacing the bare `fmt.Printf`
  startup lines on both API-hosting paths.
- Log a **one-line summary of every control-API request**, wherever the API is
  exposed — `outfit daemon` and `outfit serve --api` share one handler and so
  share this: method, path, status, duration, response size, and the caller's
  address. Never the bearer token, and never a request or response body.
- **Grade the summary by outcome** so silencing routine traffic does not
  silence problems: a 2xx/3xx summary at `info`, a 4xx at `warn` (a rejected
  token or a malformed cursor is worth seeing), a 5xx at `error`.
- Log the **engine lifecycle** on the same logger — start requested, engine
  started with its argv, stop, and the exit the supervisor records — at `info`,
  with a crash at `error`. This too is shared: `serve --api` supervises an
  engine as surely as the daemon does.
- Make the level **configurable**: `--log-level debug|info|warn|error` on both
  `outfit daemon` and `outfit serve`, `OUTFIT_LOG_LEVEL` in the environment,
  flag beating env, defaulting to `info`. `--log-level warn` is the setting
  that keeps a polled fleet quiet while still reporting failures.
- Keep `outfit serve`'s existing foreground behaviour intact: the engine's own
  stdout and stderr stay forwarded untouched, and outfit's records go to stderr
  as structured lines beside them rather than replacing or reformatting them.
- Keep the logger **injected, not global**: a `Logger` field on `daemon.Daemon`
  that defaults to discarding, so the CLI is the single place that decides
  where outfit's own output goes and the test suite stays quiet.
- Document the flag in `docs/commands/serve.md`, the variable in
  `docs/env-vars.md`, and the request-summary format in `docs/http-api.md`.

## Capabilities

### New Capabilities

- `api-logging`: the control API's own diagnostic output, on every host that
  exposes it — that it logs a summary of every request and the engine
  lifecycle, how those records are graded by severity, what they must never
  contain, and how an operator sets the level.

### Modified Capabilities

None. `daemon-api` describes what the endpoints accept and return, and none of
that changes: no route, request, response or status code moves. Logging is what
the process says about itself while serving them, which is why it is its own
capability rather than an amendment to the API contract. `serve-daemon` and
`local-serving` are likewise untouched — the new flag adds to how the two
commands are invoked without altering the supervision or launch behaviour those
specs describe.

## Impact

- `internal/daemon`: a new `logging.go` (level parsing, logger construction, the
  request-summary middleware and its status/size-capturing `ResponseWriter`), a
  `Logger` field on `Daemon`, and the middleware wired into `Handler` — so both
  hosts get it from the one place the handler is built.
- `internal/daemon/supervisor.go` / `daemon.go`: lifecycle records at the points
  the state already changes — start, stop, recorded exit.
- `cmd/outfit/serve_daemon.go`: build the logger, replace the startup
  `fmt.Printf` lines, hand it to the daemon on both the `daemon` and
  `serve --api` paths.
- `cmd/outfit/serve.go` and `cmd/outfit/complete.go`: the `--log-level` flag on
  both commands, and its completion.
- No change to `Routes()`, `docs/openapi.yaml` or the contract test — the API
  surface is untouched, so the drift test should pass without being edited,
  which is itself a check that this change stayed on its own side of the line.
- No new module dependency: `log/slog` is stdlib.
- `docs/commands/serve.md`, `docs/env-vars.md`, `docs/http-api.md`.
