## Why

The serve-daemon change made `outfit daemon` the one way an engine
runs and reports — everywhere except the cloud, where the remote instance
still runs a hand-rolled systemd unit and the Lambdas collect metrics by
shelling `nvidia-smi`/`vmstat`/`free`/`curl` over SSM, duplicating in
TypeScript what `internal/metrics` now does in Go. Putting outfit's daemon on
the instance harmonises the two paths: the Lambdas become thin relays to the
same control API a fleet client will speak, the TypeScript collectors are
deleted, and `outfit remote` becomes the end-to-end test bed for the daemon
before the `fleet` change lands.

## What Changes

- **Bake outfit into the runtime AMIs**: a pinned outfit release (version in
  `remote/lib/config.ts`, like `llamacppRelease`/`vllmVersion`) installed by
  the Image Builder components for both runners; recipe versions bump.
- **The instance boots `outfit daemon`**: the start Lambda's user-data stops
  writing per-runner engine units. Instead it writes the daemon's
  `deploy-config.json` (derived from the SSM-stored deploy config, with the
  cloud-owned settings — bind address, API key file, local weights path —
  resolved into it), one `outfit-daemon.service` unit running
  `outfit daemon --api-addr 127.0.0.1:4242`, and then requests the first
  engine start through the control API once the daemon answers — the daemon
  never auto-starts, so the boot start is the same explicit API start any
  client performs. Loopback bind means the tokenless-listen rule is satisfied
  and nothing on the network can reach the control API; only SSM can.
- **`outfit serve` learns vLLM**: the engine table gains `vllm` (`vllm serve`,
  its own flag dialect), so the daemon can host the cloud's other runner — and
  `PROVIDER vllm` becomes locally servable for everyone.
- **Lambdas call the daemon over SSM**: `stats` becomes one
  `curl 127.0.0.1:4242/v1/metrics` merged with instance metadata (id, type,
  uptime, environment); the idle check reads the same reply's token counters.
  The TypeScript metric parsers and per-runner scrape commands are deleted.
- **Engine self-healing preserved**: the boot script installs a small systemd
  timer that asks the daemon's status and POSTs `/v1/start` when the engine is
  `crashed` — keeping today's `Restart=on-failure` behaviour without the
  daemon growing a restart policy (that remains issue #48).
- **Log shipping repointed**: CloudWatch agent and logrotate configs tail the
  daemon's engine log path instead of `/var/log/llm/*.log`; the boot log is
  unchanged.

## Capabilities

### New Capabilities

- `remote-engine-host`: the instance-side contract — outfit baked into the
  runtime AMI, boot writing the daemon's deploy config and running the daemon
  on loopback, Lambdas reaching the control API via SSM, the crash-nudge
  timer, and the engine log living at the daemon's stable path.

### Modified Capabilities

- `local-serving`: the "Choosing the engine" requirement changes — `vllm`
  joins `llamacpp` and `omlx` as a servable engine, and the
  cannot-serve error names the new set.

## Impact

- `cmd/outfit/serve.go`: `vllm` entry in the engine table; a vLLM param
  mapping (model as `vllm serve`'s positional, alias → `--served-model-name`,
  context → `--max-model-len`); `internal/preset` gains the vLLM dialect.
- `remote/lib/config.ts` + `image-stack.ts`: pinned outfit version, install
  component, recipe bumps.
- `remote/lambda/start/index.ts`: user-data rewrite (daemon unit + config +
  nudge timer); health probe unchanged.
- `remote/lambda/stats/index.ts` + `shared/stats.ts` + `shared/idle.ts`:
  collection replaced by the daemon call; parsers and their tests deleted
  (the Go ports in `internal/metrics` are the survivors).
- `outfit remote metrics` output shape is unchanged — the daemon speaks the
  same stats dialect the formatters already render.
- Requires a redeploy of `remote/` (new AMI bake, updated Lambdas) to take
  effect; existing environments keep working until re-baked and re-deployed.
