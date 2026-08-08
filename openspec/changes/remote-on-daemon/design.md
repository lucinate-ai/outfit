## Context

See proposal.md — Why. Current state that shapes the approach:

- The start Lambda's user-data builds the engine command Lambda-side
  (`buildServeCommand` in `remote/lambda/shared/deploy-config.ts`) and writes
  a per-runner systemd unit with `Restart=on-failure`; weights are synced to
  `/opt/llm/model`; the API key comes from Secrets Manager (vLLM: env file;
  llama.cpp: root-only `--api-key-file`).
- Metrics: the stats Lambda runs `nvidia-smi`/`vmstat`/`free` and a gated
  `curl /metrics` over SSM and parses in TypeScript; the idle (auto-stop)
  check scrapes the same `/metrics` for running/counter signals.
- serve-daemon (previous change, this branch) gives us the daemon: one engine,
  states, `/v1/*` API, tokenless loopback allowed, deploy-config persistence
  at `~/.config/outfit/daemon/deploy-config.json`, engine log beside it.
- The CloudWatch agent and the baked logrotate config currently point at
  `/var/log/llm/*.log`.

## Goals / Non-Goals

**Goals:**

- One engine-hosting path: the instance's engine lifecycle and metrics go
  through the same daemon contract a fleet client will use.
- Delete the TypeScript collection (`shared/stats.ts` parsers, the per-metric
  SSM commands, the engine-scrape plumbing in `shared/idle.ts`).
- `outfit remote` as the daemon's end-to-end proving ground on a real GPU box.

**Non-Goals:**

- fleet.yaml / `outfit fleet` (next change).
- A daemon restart policy (#48) — cloud self-healing is instance plumbing here.
- Multi-engine instances (#49); Apple GPU stats (#47).
- Changing deploy/env/stop Lambda behaviour or the health-probe contract.

## Decisions

**D1 — The boot writes the daemon's deploy-config.json directly, then asks
for the start over the API.** The start Lambda already holds the deploy
config (SSM parameter); user-data renders it into the daemon's state file —
runner, the *local* model path (`/opt/llm/model/model.gguf` or the model dir;
the serve-daemon argv builder already treats a path-shaped model correctly),
servedModelName, contextSize, and serveArgs carrying the cloud-owned flags
(`--host 0.0.0.0`, `--port`, key delivery) — enables `outfit-daemon.service`,
and then POSTs `/v1/start` on loopback (retrying until the daemon answers).
The daemon never auto-starts, so the boot start is the standard API start —
no cloud special case inside outfit. Alternative — pushing the config via
`PUT /v1/deploy-config` instead of writing the file — rejected: the file *is*
the API's storage, and writing it avoids ordering the push before the start;
only the start itself needs the daemon up.

**D2 — vLLM joins the engine table as a first-class serve engine.** New
`serveEngine` entry: binary `vllm`, subcommand `serve`, model as the
positional argument (a new optional `positionalModel` handling in the argv
build — vLLM takes the model after `serve`, not behind a flag), params mapping
alias → `--served-model-name`, ctx → `--max-model-len`, BASEURL → host/port;
`internal/preset` gains a `VLLM` dialect (flags spell `--long-form`).
`metricsEngine: "vllm"` wires the existing scraper dialect in. The cloud's
venv path (`/opt/llm/venv/bin/vllm`) is handled with a baked PATH symlink, not
an outfit special case. Alternative — keep vLLM Lambda-built and daemon-run
only llamacpp — rejected: it would leave two engine-hosting paths alive, which
is the thing this change exists to end.

**D3 — Stats Lambda becomes metadata + one SSM curl.** It keeps discovering
the instance and reporting `environment`/`state`/`instanceId`/`instanceType`/
`uptimeSeconds` (control-plane knowledge), and merges the daemon's
`/v1/metrics` JSON for the rest — the field names already agree because the
daemon speaks the shape the formatters render. `shared/stats.ts` shrinks to
the response types; parsers and their fixtures are deleted with their tests
(the Go ports in `internal/metrics` carry the behaviour now).

**D4 — Idle detection reads the daemon reply.** `running` and `counter` come
straight from the daemon's `tokens` object over the same SSM curl; the
runner-aware grep patterns and `parseMetrics` go. An unreachable daemon reads
as "no activity observed", preserving the wedged-server-still-stops property.

**D5 — Crash recovery is a baked nudge timer.** A systemd timer (baked into
the AMI, enabled by user-data) runs a one-line check: status `crashed` →
`POST /v1/start`. This preserves today's `Restart=on-failure` healing without
growing a daemon restart policy (#48 stays the real fix). The timer nudges
only on `crashed` — a deliberate stop stays stopped.

**D6 — Log shipping repoints, logrotate follows.** The daemon runs as root, so
the engine log lives at `/root/.config/outfit/daemon/engine.log` — a stable
path; the per-boot CloudWatch agent config and the baked logrotate config name
it instead of `/var/log/llm/<engine>.log`. The boot log path is untouched.
Alternative — teach the daemon a log-path flag — rejected for now: nothing
needs the flexibility yet, and the spec pins behaviour ("stable path"), not
the path itself.

**D7 — Outfit reaches the AMI as a pinned release artifact.** `config.ts`
gains `outfitVersion`; the Image Builder components download that release
binary (checksum-verified) during the bake, exactly how `llamacppRelease`
pins the engine. Recipe versions bump so a version change re-bakes.

## Risks / Trade-offs

- [The baked outfit predates a daemon API change] → the AMI pins outfit;
  Lambdas and binary ship from the same repo, so a contract change lands as a
  version bump in `config.ts` plus a re-bake, and `outfit remote bootstrap
  --wait` already reports bake completion.
- [First release without a published outfit binary artifact] → the bake needs
  a fetchable release; if releases lag, the component can build from the
  pinned tag with Go in the builder (slower bake, same pin). Decide at
  implementation from what the release pipeline provides.
- [vLLM positional-model handling churns the serve argv builder] → confined to
  the engine table + one argv-assembly branch; existing llamacpp/omlx tests
  pin the current behaviour.
- [Nudge timer races a deliberate API stop] → the timer acts only on
  `crashed`; `stopped` is never nudged, so a stop stays stopped.
- [Daemon down ⇒ metrics and idle blind] → same failure mode as today's
  failed SSM scrape; idle's "no activity" default still stops the box, which
  is the safe direction for cost.

## Open Questions

- Whether the outfit binary is fetched as a GitHub release asset or built
  from the pinned tag during the bake (depends on the release pipeline;
  either satisfies D7 and changes only the component script).
