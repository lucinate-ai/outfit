# HTTP Control API

When running `outfit serve --daemon` or `outfit serve --api`, a control API is exposed on `:4242` (or the address specified by `--api-addr`) that allows management of the engine via JSON requests.

All requests must include a bearer token in the `Authorization` header:
`Authorization: Bearer $OUTFIT_API_TOKEN`

The token is read from the environment (e.g., the `.env` file beside the Outfit).

## Endpoints

### GET `/v1/status`
Returns the current state of the engine:
- `state`: `idle`, `running`, `stopped`, or `crashed`
- `served`: The configuration of what is currently being served
- `log_path`: The path to the engine's log file (when running in daemon mode)

### POST `/v1/start`
Starts the engine.
- Returns `200 OK` on success.
- Returns `409 Conflict` if an engine is already running.
- Returns `400 Bad Request` if the engine fails to start.

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
