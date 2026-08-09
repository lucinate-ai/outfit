# HTTP Control API

When running `outfit daemon` (always) or `outfit serve --api` (opt-in), a control API is exposed on `:4242` (or the address specified by `--api-addr`) that allows management of the engine via JSON requests.

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

### PUT `/v1/deploy-config`
Updates the configuration for the *next* engine start.
- Request body: `remote.DeployConfig` JSON
- Returns `200 OK` with a message indicating if the change is active now or will take effect on the next start.
- Returns `400 Bad Request` if the configuration is invalid or fails to push.
