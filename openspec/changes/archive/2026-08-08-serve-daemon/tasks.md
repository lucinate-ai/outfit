## 1. Metrics foundation (`internal/metrics`)

- [x] 1.1 Create `internal/metrics` with the canonical stats types (state,
      runner, model, GPU, CPU, RAM, token stats), moved from
      `internal/remote`; leave aliases in `internal/remote` so its API and
      tests keep compiling
- [x] 1.2 Port the Prometheus token-stats parser (`buildTokenStats`) from
      `remote/lambda/shared/stats.ts` to Go, with fixture tests against real
      llama-server `/metrics` output
- [x] 1.3 Port the system-stat parsers — `nvidia-smi` (GPU, MiB→bytes),
      `vmstat` (CPU), `free` (RAM) — as pure output-string parsers with
      fixture tests matching the Lambda parsers' results
- [x] 1.4 Add the collector runner: per-platform command sets (Linux:
      nvidia-smi/vmstat/free; darwin: sysctl+vm_stat for RAM, top for CPU, no
      GPU), absent-command → omitted stat, never an error
- [x] 1.5 Add the engine `/metrics` scraper (HTTP GET on the engine's serving
      address, optional API key), unreachable engine → omitted engine stats
- [x] 1.6 Switch the `cmd/outfit` metrics formatters (bar/table/json) to the
      `internal/metrics` types and verify `outfit remote metrics` output is
      unchanged

## 2. Supervisor (`internal/daemon`)

- [x] 2.1 Implement the engine supervisor: start detached in its own process
      group, wait goroutine, mutex-guarded state machine
      (`idle`/`running`/`stopped`/`crashed`; non-zero unprompted exit =
      crashed, zero = stopped)
- [x] 2.2 Implement stop with SIGTERM-to-process-group then SIGKILL after a
      10s grace; idempotent when nothing is running
- [x] 2.3 Enforce one engine per daemon: start while running fails naming the
      running engine
- [x] 2.4 Capture engine stdout/stderr to `~/.config/outfit/daemon/engine.log`
      and expose the path in status; move `configHome()` somewhere shared
- [x] 2.5 Implement deploy-config persistence: store pushed `DeployConfig` as
      `deploy-config.json` (0600), load on daemon boot, precedence over the
      Outfit; validate the runner via `engineFor`
- [x] 2.6 Build the engine argv from a stored deploy config's serveArgs, and
      from the Outfit path otherwise, reusing serve's existing construction;
      append the engine's metrics flag (new field in the `serveEngine` table)

## 3. Control API (`internal/daemon`)

- [x] 3.1 Implement the HTTP server (stdlib) with routes `GET /v1/status`,
      `POST /v1/start`, `POST /v1/stop`, `GET /v1/metrics`,
      `PUT /v1/deploy-config`; JSON errors with meaningful statuses (401,
      409 start-while-running, 400 bad config)
- [x] 3.2 Implement bearer-token middleware: token from `OUTFIT_API_TOKEN`,
      constant-time compare, 401 without; refuse non-loopback listen with no
      token, allow tokenless loopback
- [x] 3.3 Wire `/v1/metrics` to the collector (system + engine scrape) and
      `/v1/status` to supervisor state, served model/runner, and log path

## 4. Serve wiring (`cmd/outfit/serve.go`)

- [x] 4.1 Add `-d`/`--daemon`, `-a`/`--api`, and `--api-addr` flags; API
      defaults on under `--daemon`, off otherwise; plain foreground serve
      byte-for-byte unchanged
- [x] 4.2 Implement daemon mode: resolve what to serve (stored deploy config,
      else Outfit), start the engine when there is one, idle otherwise; clean
      shutdown on SIGINT/SIGTERM stops the engine first
- [x] 4.3 Implement foreground `--api`: same server over the foreground
      engine (status/metrics work, start fails as already-running, stop
      terminates the engine and serve exits)
- [x] 4.4 Ensure the Outfit-adjacent `.env` loading runs before the API token
      is read, matching the remote commands' local-environment behaviour

## 5. The `outfit daemon` command and serve cleanup

- [x] 5.1 Add the top-level `outfit daemon [path] [--api-addr]` command:
      dispatch case, usage text, completion-table entry; hosts the daemon
      stack with no engine start on boot (stored config and Outfit are
      bare-start fallbacks only), API always on, same token rules
- [x] 5.2 Remove `-d`/`--daemon` from `serve` (flags, completion entry,
      usage text), keeping `-a`/`--api` and `--api-addr`; rehome the daemon
      runtime under the `daemon` command and drop serve's boot-start path
- [x] 5.3 Accept an optional deploy config body on `POST /v1/start`:
      validate and persist via the push path, then start; the already-running
      check runs first so a 409 stores nothing
- [x] 5.4 Tests: the daemon idles beside an Outfit until started; stop over
      the API leaves the daemon answering and restartable; start-with-body
      serves the carried config; start-with-body while running is a 409 that
      stores nothing; `serve -d` is an unknown flag; `serve -a` stays
      foreground
- [x] 5.5 Update README, AGENTS.md, `docs/commands/serve.md` and
      `docs/http-api.md` for `outfit daemon`, the start payload, and serve's
      foreground-only contract

## 6. Verification and docs

- [x] 6.1 End-to-end test with a stub engine binary: daemon boots, starts,
      reports running, crash reported not restarted, stop idempotent,
      deploy-config push applies on next start
- [x] 6.2 `go test ./... -cover` >= 80%, `gofmt` clean
- [x] 6.3 Update README and AGENTS.md for daemon mode, the API surface, the
      token, and the new packages
- [x] 6.4 `openspec validate serve-daemon --strict` passes
