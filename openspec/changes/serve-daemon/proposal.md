## Why

Fleet management — orchestrating engines across several machines (LAN,
tailscale, cloud) — needs every machine to run engines and report metrics the
same way. Today those jobs are split: `outfit serve` launches an engine in the
foreground and knows nothing else about it, while metrics collection lives in
the remote stack's TypeScript stats Lambda, which shells onto the instance via
SSM to run `nvidia-smi`, `vmstat` and `free`. Neither piece is reusable on a
home-lab node. This change moves both jobs into outfit itself, making
`outfit serve --daemon` the one consistent way to run an engine and expose its
state — the foundation two follow-up changes build on (`remote-on-daemon`:
Lambdas call the on-instance daemon; `fleet`: multi-node client and dashboard).

## What Changes

- **In-process metrics collection** (new `internal/metrics` package): scrape
  the engine's `/metrics` endpoint for token/request stats, and collect system
  stats — GPU via `nvidia-smi`, CPU via `vmstat`, RAM via `free` — as a Go port
  of the stats Lambda's collectors. On platforms missing those commands
  (macOS), degrade gracefully: engine stats plus basic CPU/RAM, no GPU
  (Apple GPU visibility is issue #47).
- **Engine supervisor**: `serve` gains a supervised mode that starts the engine
  detached, captures its logs, tracks `running`/`stopped`/`crashed`, and stops
  it on request. No auto-restart of crashed engines (issue #48); one engine per
  daemon (issue #49).
- **`outfit serve -d/--daemon`**: run serve as a long-lived daemon that
  supervises the engine and serves the control API, starting the engine on
  boot when it has something to serve (its Outfit or a stored deploy config).
- **`outfit daemon`**: the same daemon as a pure agent — it never starts an
  engine on boot; the engine starts only on an API request, and `/v1/start`
  can carry the deploy config (runner, model, etc.) to start. Stopping the
  engine over the API always leaves the daemon running for subsequent calls.
  This is the mode a fleet node runs.
- **`outfit serve -a/--api`**: expose the control HTTP API — on by default
  under `--daemon`, off otherwise; `--api` alone enables it for a foreground
  serve.
- **Control HTTP API**: `status`, `start`, `stop`, `metrics`, and
  `deploy-config` endpoints, authenticated by a bearer token. Deploy config
  pushed to the daemon reuses the existing `DeployConfig` shape produced by
  `deployConfigFor` (Outfit + preset resolved client-side), so presets never
  cross the wire — only their resolved flags.

## Capabilities

### New Capabilities

- `engine-metrics`: in-process collection of engine token/request stats and
  system GPU/CPU/RAM stats, with platform-graceful degradation, exposed as the
  same stats shape the existing `remote metrics` formatters render.
- `serve-daemon`: the supervised engine lifecycle — daemon mode (via
  `serve --daemon` and the agent-style `outfit daemon` command), detached
  start, log capture, state tracking (`running`/`stopped`/`crashed`), stop,
  and stored deploy config as the source of what to serve.
- `daemon-api`: the control HTTP API — endpoint surface, request/response
  shapes, bearer-token auth, and the `-a/--api` exposure rules.

### Modified Capabilities

- `local-serving`: `outfit serve` accepts `-d/--daemon` and `-a/--api`;
  foreground stdio-forwarded behaviour remains the default and is unchanged
  without those flags.

## Impact

- New packages: `internal/metrics` (collectors), `internal/daemon`
  (supervisor + HTTP API).
- `cmd/outfit/serve.go`: new flags and daemon/API wiring; the engine-command
  construction (`engineFor`, params, presets) is reused unchanged.
- `cmd/outfit/main.go` + `complete.go`: the new top-level `daemon` command in
  the dispatch, usage text, and completion table.
- `cmd/outfit/remote.go`: `deployConfigFor` becomes shared with the daemon
  push path (no behaviour change to `remote deploy` in this change).
- Secrets/auth: API token read from the environment/`.env` (never a flag),
  honouring the 0600-for-secrets invariant.
- No changes yet to the `remote/` CDK stack or Lambdas — that is the
  `remote-on-daemon` follow-up.
- Test coverage must stay >= 80% (`go test ./... -cover`).
