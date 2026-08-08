## 1. vLLM as a serve engine (Go)

- [ ] 1.1 Add the `VLLM` dialect to `internal/preset` (long-form `--flag`
      spelling) with tests
- [ ] 1.2 Add the `vllm` entry to the `serveEngine` table: binary `vllm`,
      subcommand `serve`, positional model handling in the argv build,
      params mapping (alias → `--served-model-name`, ctx →
      `--max-model-len`, BASEURL → host/port), `metricsEngine: "vllm"`,
      install hint
- [ ] 1.3 Extend serve/daemon tests: `PROVIDER vllm` dry-run argv, deploy
      config with runner `vllm` accepted by the daemon and built into a
      `vllm serve` command with the model positional

## 2. Bake outfit into the AMIs (remote/)

- [ ] 2.1 Add `outfitVersion` to `remote/lib/config.ts`, pinned like the
      runner versions
- [ ] 2.2 Extend both Image Builder components to install the pinned outfit
      binary (checksum-verified) and the `vllm` PATH symlink; bake the
      crash-nudge timer unit and the updated logrotate config (daemon engine
      log path); bump recipe versions
- [ ] 2.3 Update `remote/` unit tests for the component/recipe changes

## 3. Boot through the daemon (start Lambda)

- [ ] 3.1 Rewrite the runner-unit section of `buildUserData`: render the
      daemon's `deploy-config.json` from the stored deploy config (local
      weights path as the model, cloud-owned flags into serveArgs, API-key
      delivery per runner), write `outfit-daemon.service`
      (`outfit daemon --api-addr 127.0.0.1:4242`), enable it and the nudge
      timer, then POST `/v1/start` on loopback, retrying until the daemon
      answers
- [ ] 3.2 Delete `buildServeCommand` and the per-runner unit builders once
      user-data no longer uses them; keep the health probe as-is
- [ ] 3.3 Point the per-boot CloudWatch agent config's engine-log source at
      the daemon's engine log path
- [ ] 3.4 Update start Lambda tests: user-data contains the daemon unit,
      the rendered deploy config, and the nudge timer; no engine unit

## 4. Lambdas read the daemon (stats + idle)

- [ ] 4.1 Replace the stats Lambda's collection with one SSM
      `curl 127.0.0.1:4242/v1/metrics`, merged with environment, instance
      id/type and uptime; response shape unchanged
- [ ] 4.2 Switch the idle check's activity signals to the daemon reply's
      `tokens.running`/`tokens.counter`; unreachable daemon reads as no
      activity
- [ ] 4.3 Delete the TypeScript parsers (`parseGpuStats`, `parseCpuStat`,
      `parseMemoryStat`, `buildTokenStats`, `parseMetrics`, the grep
      patterns and per-metric commands) and their tests
- [ ] 4.4 Update Lambda tests to stub the daemon reply and verify the merged
      response

## 5. Verification and docs

- [ ] 5.1 `go test ./... -cover` >= 80% and `gofmt` clean; `pnpm test` green
      in `remote/`
- [ ] 5.2 Verify `outfit remote metrics` renders a stubbed daemon-shaped
      reply identically in bar, table and JSON formats
- [ ] 5.3 Update `remote/README.md`/docs and AGENTS.md for the daemon-hosted
      instance and the deleted collectors
- [ ] 5.4 `openspec validate remote-on-daemon --strict` passes
- [ ] 5.5 End-to-end on a real environment (user-run): re-bake, deploy,
      `outfit remote start`, `metrics`, crash-nudge, `stop`
