## Context

See proposal.md — Why. The relevant current state:

- `cmd/outfit/serve.go` builds an engine command from an Outfit (+ optional
  preset) via `engineFor` and runs it foreground with `cmd.Run()`. It has no
  notion of process state, logs, or metrics.
- All metric collection lives in `remote/lambda/shared/stats.ts`: token stats
  parsed from the engine's Prometheus `/metrics` text, GPU from `nvidia-smi`,
  CPU from `vmstat`, RAM from `free`, executed over SSM. The Go client
  (`internal/remote.StatsResponse` + the formatters in `cmd/outfit/remote.go`)
  only renders.
- `deployConfigFor` (cmd/outfit/remote.go) already flattens Outfit + preset
  into the `DeployConfig` shape (runner, model, context, alias, serveArgs);
  the cloud path injects cloud-owned flags (`--metrics`, ports) itself.
- Constraints: coverage >= 80%, secrets never on the command line, files that
  may hold secrets written 0600, no new runtime binary dependencies.

## Goals / Non-Goals

**Goals:**

- One Go implementation of metrics collection, fixture-testable, shared by
  daemon mode now and the remote Lambdas in the follow-up change.
- A supervisor whose observable states and API are exactly what the fleet
  client and the rewired Lambdas will consume — no redesign later.
- Zero disturbance to plain `outfit serve` foreground behaviour.

**Non-Goals:**

- Rewiring the remote Lambdas or baking outfit into the AMI
  (`remote-on-daemon` change).
- `fleet.yaml`, the `outfit fleet` command family, TUI (`fleet` change).
- Auto-restart (#48), multi-engine (#49), web UI (#50), Apple GPU stats (#47).
- Log rotation; daemon detach/service installation.

## Decisions

**D1 — Package layout: `internal/metrics` and `internal/daemon`.**
`internal/metrics` owns the canonical stats types and collectors. The stats
types currently in `internal/remote` (StatsResponse and friends) move here;
`internal/remote` keeps aliases so its API surface is unchanged, and the
formatters in `cmd/outfit` switch to the metrics types. Alternative — duplicate
shapes and convert — rejected: the whole point is one dialect everywhere.

**D2 — Collectors are command-output parsers, not a system library.**
Each system stat is `(command, parser)` where the parser takes captured output —
a direct port of the Lambda's `parseGpuStats`/`parseCpuStat`/`parseMemoryStat`,
testable against fixture strings. Per-platform command sets: Linux uses
`nvidia-smi`/`vmstat`/`free` (byte-for-byte parity with the Lambda, which the
follow-up change deletes); macOS uses `sysctl`/`vm_stat` for RAM and `top -l 1`
for CPU, with GPU omitted (#47). A missing command yields an absent stat
(pointer/omitted JSON field), never an error. Alternative — gopsutil —
rejected: a large dependency for four numbers, and parity with the Lambda
parsers matters for the follow-up's delete-with-confidence.

**D3 — Engine stats come from the engine's `/metrics`, and the daemon
guarantees it's on.** The scraper GETs the engine's Prometheus endpoint on its
serving address and ports `buildTokenStats`. When the daemon builds the engine
command it appends the engine's metrics flag (as the cloud path's cloud-owned
flags already do) and passes the API key if the engine has one configured.
Engines with no metrics endpoint (oMLX today) simply yield no engine stats.

**D4 — Supervisor: one `exec.Cmd`, a wait goroutine, a mutex-guarded state
machine.** States `idle`/`running`/`stopped`/`crashed` per the spec. The engine
runs in its own process group; stop sends SIGTERM to the group and escalates to
SIGKILL after a 10s grace. An unprompted exit is `crashed` on non-zero status,
`stopped` on zero. The argv comes from the existing serve construction path
(engineFor + params + preset), from the stored deploy config's serveArgs when
one has been pushed. Engine stdout/stderr go to a log file opened by the
daemon.

**D5 — Daemon state lives under outfit's config home,
`~/.config/outfit/daemon/`** (deploy-config.json written 0600 — serveArgs
could carry sensitive flags — plus engine.log). The existing `configHome()`
helper moves from `internal/remote` to a spot both packages can use. One
daemon per machine is the v1 assumption (matches one engine per node);
a second daemon fails to bind the API port, which is the collision report.

**D6 — API: stdlib `net/http`, versioned routes, token middleware.**
`GET /v1/status`, `POST /v1/start`, `POST /v1/stop`, `GET /v1/metrics`,
`PUT /v1/deploy-config`. JSON in/out; errors as `{"error": "..."}` with a
meaningful status (409 for start-while-running, 400 for bad config, 401 for
auth). Token read from `OUTFIT_API_TOKEN` (reached by the same `.env` loading
the Outfit resolution already performs), compared constant-time. Default
listen `:4242`, overridable with `--api-addr`; a non-loopback bind with no
token refuses to start (spec: daemon-api). No TLS in v1: tailscale/LAN plus
bearer token is the threat model the proposal accepts.

**D7 — `--api` under foreground serve exposes the same server, and the
one-engine rule does the rest.** Foreground serve with `--api` reports
status/metrics for the foreground engine; `start` always fails (an engine is
already running — and when it exits, serve itself exits); `stop` terminates
the foreground engine, after which serve exits as it always has on engine
exit. No special foreground mode in the API.

**D8 — Deploy-config push validates against `engineFor`.** The daemon accepts
the existing Go `DeployConfig` shape, rejects a runner `engineFor` doesn't
know, persists, and applies on next start — never touching a running engine.
`deployConfigFor` stays where it is and is reused by the pusher side later
(fleet deploy); this change only consumes its output shape.

## Risks / Trade-offs

- [Engine metrics flags differ per engine/version (llama-server `--metrics`)]
  → the metrics flag lives in the `serveEngine` table next to the other
  per-engine knobs, so a new engine states its own or none.
- [CPU sampling via `vmstat`/`top` is a point-in-time snapshot] → same
  trade-off the Lambda already accepts; parsers are shared so any later
  improvement lands everywhere.
- [Port 4242 may be taken] → bind failure at startup with a clear error and
  `--api-addr` in the message.
- [Killing the process group could orphan engine children that detach] → both
  supported engines run as single processes today; grace-then-SIGKILL bounds
  the damage.
- [Stats-type move could churn `internal/remote`'s tests] → aliases keep the
  old names compiling; only the type's home moves.

## Open Questions

- Exact `top`/`vm_stat` field parsing on macOS (fixture-driven; can be tuned
  in implementation without spec impact).
- Log file size over long daemon uptimes — rotation is explicitly deferred;
  revisit with the fleet change if it bites.
