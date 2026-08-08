## 1. vLLM as a serve engine (Go)

- [x] 1.1 Add the `VLLM` dialect to `internal/preset` (long-form `--flag`
      spelling) with tests
- [x] 1.2 Add the `vllm` entry to the `serveEngine` table: binary `vllm`,
      subcommand `serve`, positional model handling in the argv build,
      params mapping (alias → `--served-model-name`, ctx →
      `--max-model-len`, BASEURL → host/port), `metricsEngine: "vllm"`,
      install hint
- [x] 1.3 Extend serve/daemon tests: `PROVIDER vllm` dry-run argv, deploy
      config with runner `vllm` accepted by the daemon and built into a
      `vllm serve` command with the model positional

## 2. Bake outfit into the AMIs (remote/)

- [x] 2.1 Add `outfitVersion` to `remote/lib/config.ts`, pinned like the
      runner versions
- [x] 2.2 Extend both Image Builder components to install the pinned outfit
      binary (checksum-verified) and the `vllm` PATH symlink; bake the
      crash-nudge timer unit and the updated logrotate config (daemon engine
      log path); bump recipe versions
- [x] 2.3 Update `remote/` unit tests for the component/recipe changes

## 3. Boot through the daemon (start Lambda)

- [x] 3.1 Rewrite the runner-unit section of `buildUserData`: render the
      daemon's `deploy-config.json` from the stored deploy config (local
      weights path as the model, cloud-owned flags into serveArgs, API-key
      delivery per runner), write `outfit-daemon.service`
      (`outfit daemon --api-addr 127.0.0.1:4242`), enable it and the nudge
      timer, then POST `/v1/start` on loopback, retrying until the daemon
      answers
- [x] 3.2 Delete `buildServeCommand` and the per-runner unit builders once
      user-data no longer uses them; keep the health probe as-is
- [x] 3.3 Point the per-boot CloudWatch agent config's engine-log source at
      the daemon's engine log path
- [x] 3.4 Update start Lambda tests: user-data contains the daemon unit,
      the rendered deploy config, and the nudge timer; no engine unit

## 4. Lambdas read the daemon (stats + idle)

- [x] 4.1 Replace the stats Lambda's collection with one SSM
      `curl 127.0.0.1:4242/v1/metrics`, merged with environment, instance
      id/type and uptime; response shape unchanged
- [x] 4.2 Switch the idle check's activity signals to the daemon reply's
      `tokens.running`/`tokens.counter`; unreachable daemon reads as no
      activity
- [x] 4.3 Delete the TypeScript parsers (`parseGpuStats`, `parseCpuStat`,
      `parseMemoryStat`, `buildTokenStats`, `parseMetrics`, the grep
      patterns and per-metric commands) and their tests
- [x] 4.4 Update Lambda tests to stub the daemon reply and verify the merged
      response

## 5. Post-plan refactors (behaviour unchanged; see design D8)

- [x] 5.1 Rename `buildUserData` to `buildInferenceUserData`, pairing it with
      `buildSeedUserData`
- [x] 5.2 Extract the daemon boot builders into `lambda/runners/` and replace
      every runner conditional in `lambda/` and `lib/` with a
      `Record<Runner, RunnerSpec>` registry (boot fragment, synced model
      path, seed sentinel/download, seed-tooling flag); CDK log groups, IAM
      grants and per-runner env vars follow `RUNNERS`
- [x] 5.3 Rename `vllmPort`/`VLLM_PORT` to `enginePort`/`ENGINE_PORT` — one
      serving port for every runner

## 6. Verification and docs

- [x] 6.1 `go test ./... -cover` >= 80% and `gofmt` clean; `pnpm test` green
      in `remote/`
- [x] 6.2 Verify `outfit remote metrics` renders a stubbed daemon-shaped
      reply identically in bar, table and JSON formats
- [x] 6.3 Update `remote/README.md`/docs and AGENTS.md for the daemon-hosted
      instance and the deleted collectors
- [x] 6.4 `openspec validate remote-on-daemon --strict` passes
