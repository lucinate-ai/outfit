# HTTP Control API

When running `outfit daemon` (always) or `outfit serve --api` (opt-in), a control API is exposed on `:4242` (or the address specified by `--api-addr`) that allows management of the engine via JSON requests.

> **The machine-readable contract is [`openapi.yaml`](openapi.yaml)** — every route, its
> auth, its request body and the schemas of its replies. Point a client generator
> at that rather than at this page, and use this page for the behaviour a schema
> cannot express. It is also attached to each [GitHub
> release](https://github.com/lucinate-ai/outfit/releases), so a consumer can pin
> the contract to the outfit version it talks to.
>
> It cannot silently fall behind: `internal/daemon/openapi_test.go` compares it
> against the routes the handler registers and the JSON fields of the structs it
> serialises, and fails the build when they disagree. Change a route or a
> response field and the spec has to change with it.

All requests must include a bearer token in the `Authorization` header:
`Authorization: Bearer $OUTFIT_API_TOKEN`

The token is read from the environment (e.g., the `.env` file beside the Outfit). A non-loopback listen with no token refuses to start; a loopback listen may go tokenless.

Under `outfit daemon`, nothing runs until a start request asks, and stopping the engine never ends the daemon — the API keeps answering. Under `serve --api` the engine is foreground-managed: start always fails as already-running, and stopping the engine ends serve itself.

## Endpoints

### GET `/v1/status`
Returns the current state of the engine:
- `state`: `idle`, `running`, `stopped`, or `crashed`
- `runner` / `model`: what is being served, when known
- `logPath`: the path to the engine's log file (when running under the daemon)
- `lastActiveAt`: when the engine last did any work, RFC 3339
- `idleSeconds`: how long it has been since then

While an engine runs, the daemon reads its token counters every 15 seconds on
its own, whether or not anything is calling this API. A reading counts as
activity when it shows requests in flight or when the cumulative counter has
moved since the last one — so a request that starts and finishes between two
readings still counts. Starting an engine counts as activity too, and stopping
one leaves the record alone, so a stopped engine still reports when work last
happened. Both fields are omitted until an engine has run.

This is the daemon answering "is this engine busy?" once, on the box, rather
than each caller re-deriving it from raw counters at whatever rate it polls.
The cloud deployment's idle check reads exactly these two fields.

### POST `/v1/start`
Starts the engine. The request body may carry a deploy config (same JSON as `PUT /v1/deploy-config`) naming what to run — it is validated and persisted exactly like a push, then started. With no body, the stored deploy config, else the Outfit the daemon sits beside, is served.
- Returns `200 OK` on success.
- Returns `409 Conflict` if an engine is already running — a carried config is **not** stored.
- Returns `400 Bad Request` if the config is invalid or the engine fails to start.

### POST `/v1/stop`
Stops the engine.
- Returns `200 OK` on success.
- Returns `500 Internal Server Error` if the engine fails to stop.

### GET `/v1/metrics`
Returns the current metrics:
- Token usage counters (from the engine's Prometheus `/metrics` endpoint)
- Host system metrics (GPU, CPU, RAM)
- `lastActiveAt` and `idleSeconds`, the same pair `/v1/status` reports

The activity pair comes from the same record `/v1/status` reads, so the two
endpoints cannot disagree. Unlike the counters and system figures, it is
reported whatever the engine's state: a stopped engine returns no tokens and
no GPU readings but still says when it last did work. Both fields are omitted
until an engine has run, and `idleSeconds` is omitted at zero as well — so
gate on `lastActiveAt`, never on `idleSeconds`, or you will hide the engine
that is busy right now.

A scrape made to serve this endpoint feeds the shared record exactly as the
background sampler's does. Reading it is not itself activity, so polling in a
loop does not keep an idle engine looking busy.

### GET `/v1/logs`
Returns a slice of the supervised engine's captured output — the file
`/v1/status` reports as `logPath`. Read-only: it never touches the engine, so
it answers whether the engine is running, stopped or crashed, the last of which
is when it is wanted most.

Query parameters, both optional:
- `offset` — byte position to read from, normally the `nextOffset` of a previous
  reply. Omitted, the **end** of the log is returned, since the recent end is
  what diagnosis wants.
- `limit` — maximum bytes to return, capped by the daemon regardless of what is
  asked for.

The reply carries `content`, the `nextOffset` immediately after it, and the
log's current `size`. Passing `nextOffset` back returns only what has been
appended since, which makes following exact — no overlap window and no
de-duplication, because a byte offset means what it says.

Reads are always bounded and a full read is never offered: nothing rotates this
file, so it grows for the daemon's lifetime.

Two states are reported distinctly rather than as an empty log:
- `missing` — there is no log file at all: no engine has ever run, or the
  daemon forwards engine output to its own stdio. Not the same as a log that
  exists and is empty.
- `staleOffset` — the requested `offset` is past the end, so the file was
  truncated or replaced. Resume from the `nextOffset` in the reply rather than
  waiting for a position that will never arrive.

Returns `400 Bad Request` if `offset` or `limit` is not a whole number, or if
`offset` is negative.

### PUT `/v1/deploy-config`
Updates the configuration for the *next* engine start.
- Request body: `remote.DeployConfig` JSON
- Returns `200 OK` with a message indicating if the change is active now or will take effect on the next start.
- Returns `400 Bad Request` if the configuration is invalid or fails to push.
