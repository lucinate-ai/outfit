## 1. Outfit file format (`internal/outfit`)

- [ ] 1.1 Add `kwParallel = "parallel"` and a `Parallel string` field on
      `Selection`, wired into `canonicalKeyword`, the parse `switch`, and
      `Format` (positioned after `OUTPUT`, before `BASEURL`)
- [ ] 1.2 Tests in `outfit_test.go`: `PARALLEL 2` parses onto `sel.Parallel`;
      duplicate `PARALLEL` errors citing both lines; round-trips through
      `Format`; absent `PARALLEL` leaves the field empty and the exported file
      unchanged from today

## 2. Per-engine translation (`cmd/outfit/serve.go`)

- [ ] 2.1 Add a small shared `parseParallel(s string) (int, error)` helper: a
      plain positive integer (no `k`/`m` suffixes), one error message, reused
      by all three `*ServeParams` functions and the deploy-config path in
      section 3
- [ ] 2.2 `llamacppServeParams`: when `sel.Parallel` is set, emit
      `--parallel <n>`; when `sel.Context` is *also* set (from the Outfit,
      not a preset), scale the emitted `ctx-size` to `context_tokens * n`
      instead of `context_tokens`. `sel.Context` set with no `sel.Parallel`
      is unchanged from today.
- [ ] 2.3 `vllmServeParams`: when `sel.Parallel` is set, emit
      `--max-num-seqs <n>`; `max-model-len` from `sel.Context` is never scaled
- [ ] 2.4 `omlxServeParams`: when `sel.Parallel` is set, emit
      `--max-concurrent-requests <n>`; still no context flag
- [ ] 2.5 Tests in `serve_test.go` (dry-run, asserting the printed command):
      - llama.cpp: `CONTEXT 128k` + `PARALLEL 2` → `--ctx-size 256000
        --parallel 2`; `PARALLEL` alone (no `CONTEXT`) → bare `--parallel n`
        with no ctx-size flag; neither set → today's output, byte-identical
      - llama.cpp + `PRESET` whose section sets `ctx-size` but the Outfit sets
        no `CONTEXT`: `PARALLEL` still emits `--parallel n` but the preset's
        `ctx-size` is left unscaled (documents the non-goal from design.md)
      - vLLM: `CONTEXT 128k` + `PARALLEL 4` → `--max-model-len 128000
        --max-num-seqs 4` (context unscaled)
      - oMLX: `PARALLEL 8` → `--max-concurrent-requests 8`, no context flag
        appears either way
      - invalid `PARALLEL` (`0`, `-1`, `abc`) fails for all three engines with
        one shared error message
      - an Outfit `PARALLEL` overrides a preset's own `np`/`max-num-seqs`/
        `max-concurrent-requests` value (same override-by-canonical-name path
        `CONTEXT` already exercises)

## 3. Daemon, fleet, and cloud-deploy wiring

- [ ] 3.1 Add `Parallel int` to `remote.DeployConfig`
      (`internal/remote/remote.go`), JSON tag `parallel`, optional (0 = unset)
- [ ] 3.2 `deployConfig` (`cmd/outfit/remote.go`): parse `sel.Parallel` into
      `dc.Parallel` the same way `sel.Context` becomes `dc.ContextSize`, for
      both `deployConfigFor` (cloud) and `deployConfigForNode` (fleet wake) —
      no `requireContext`-style gate
- [ ] 3.3 Add `"parallel"`, `"max-num-seqs"`, `"max-concurrent-requests"` to
      `cloudOwnedFlags` so a preset's own value is superseded by the
      Outfit-derived one on both the cloud and fleet-node paths, exactly like
      `ctx-size`
- [ ] 3.4 `argvFromDeployConfig` (`cmd/outfit/serve_daemon.go`): when
      `dc.Parallel > 0`, set `sel.Parallel = strconv.Itoa(dc.Parallel)` before
      calling `engine.params(sel)` — no new translation code, section 2
      already covers it
- [ ] 3.5 Tests: `remote_deploy_test.go` covers `deployConfig`
      deriving `Parallel`, and a preset-set `np`/`max-num-seqs` being dropped
      from `serveArgs` when `PARALLEL` is set; `serve_daemon_test.go`'s
      `TestArgvFromDeployConfigVllm`-style test gets a llama.cpp and a vLLM
      case asserting the scaled/unscaled command from a `DeployConfig` with
      `ContextSize` and `Parallel` both set

## 4. Cloud wire mirror (TypeScript, `remote/lambda`)

- [ ] 4.1 Add an optional `parallel?: number` to the `DeployConfig` type and
      its validation in `remote/lambda/shared/deploy-config.ts` (mirrors
      `contextSize`, but not required)
- [ ] 4.2 `daemon-boot.ts`'s `daemonDeployConfig`: copy `cfg.parallel` through
      to the JSON the instance's daemon reads (only when present, so an
      omitted `parallel` doesn't add a `0` the Go side would treat as "unset"
      versus a JSON `null`/absent — confirm the Go `json` tag's zero-value
      behavior matches what gets omitted here)
- [ ] 4.3 Update `remote/test/deploy-config.test.ts` and
      `remote/test/start.test.ts`/`seed.test.ts` fixtures for the new
      optional field; confirm a `DeployConfig` with no `parallel` still
      validates exactly as today

## 5. Docs and examples

- [ ] 5.1 `docs/outfit-file.md`: add `PARALLEL` to the keyword table and a
      rule paragraph next to `CONTEXT`'s, stating the per-engine mapping and
      the llama.cpp `ctx-size` scaling explicitly (point back to one place
      rather than re-explaining per engine)
- [ ] 5.2 `docs/commands/serve.md`: document the three flags `PARALLEL`
      produces and the llama.cpp scaling behaviour, alongside the existing
      `CONTEXT`/`--ctx-size`/`--max-model-len` mapping table
- [ ] 5.3 README: extend the `Outfit` file example block with a commented
      `PARALLEL` line; add one line under "Serving a local model" cross-
      referencing the new behaviour (not a full re-explanation — link to the
      docs page)
- [ ] 5.4 Add or extend one example under `examples/llamacpp/` showing
      `CONTEXT`/`PARALLEL` together with the resulting `--ctx-size`, so the
      worked example in proposal.md's practical case has a runnable
      counterpart

## 6. Verification

- [ ] 6.1 `go test ./... -cover` >= 80%, `gofmt -w ./...`, `go vet ./...`
- [ ] 6.2 `npm test` (or the repo's TS test command) green under `remote/` for
      the `deploy-config`/`daemon-boot` changes
- [ ] 6.3 Manual `outfit serve --dry-run` check for all three engines with and
      without `PARALLEL`, confirming the printed command matches design.md's
      worked example (`CONTEXT 128k` + `PARALLEL 2` → llama.cpp `--ctx-size
      256000 --parallel 2`)
- [ ] 6.4 `openspec validate configure-parallelism --strict` passes
