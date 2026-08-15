## 1. Outfit file format (`internal/outfit`)

- [x] 1.1 Add `kwParallel = "parallel"` and a `Parallel string` field on
      `Selection`, wired into `canonicalKeyword`, the parse `switch`, and
      `Format` (positioned after `OUTPUT`, before `BASEURL`)
- [x] 1.2 Tests in `outfit_test.go`: `PARALLEL 2` parses onto `sel.Parallel`;
      duplicate `PARALLEL` errors citing both lines; round-trips through
      `Format`; absent `PARALLEL` leaves the field empty and the exported file
      unchanged from today

## 2. Per-engine translation (`cmd/outfit/serve.go`)

- [x] 2.1 Add a small shared `parseParallel(s string) (int, error)` helper: a
      plain positive integer (no `k`/`m` suffixes), one error message, reused
      by all three `*ServeParams` functions and the deploy-config path in
      section 3
- [x] 2.2 `llamacppServeParams`: when `sel.Parallel` is set, emit
      `--parallel <n>`; when `sel.Context` is *also* set (from the Outfit,
      not a preset), scale the emitted `ctx-size` to `context_tokens * n`
      instead of `context_tokens`. `sel.Context` set with no `sel.Parallel`
      is unchanged from today.
- [x] 2.3 `vllmServeParams`: when `sel.Parallel` is set, emit
      `--max-num-seqs <n>`; `max-model-len` from `sel.Context` is never scaled
- [x] 2.4 `omlxServeParams`: when `sel.Parallel` is set, emit
      `--max-concurrent-requests <n>`; still no context flag
- [x] 2.5 Tests in `serve_test.go` (dry-run, asserting the printed command):
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

- [x] 3.1 Add `Parallel int` to `remote.DeployConfig`
      (`internal/remote/remote.go`), JSON tag `parallel`, optional (0 = unset)
- [x] 3.2 `deployConfig` (`cmd/outfit/remote.go`): parse `sel.Parallel` into
      `dc.Parallel` the same way `sel.Context` becomes `dc.ContextSize`
      (including falling back to a preset's own `np`/`parallel` value, since
      that value is dropped from `serveArgs` by 3.3 and would otherwise be
      silently lost), for both `deployConfigFor` (cloud) and
      `deployConfigForNode` (fleet wake) — no `requireContext`-style gate
- [x] 3.3 Add `"parallel"`, `"max-num-seqs"`, `"max-concurrent-requests"` to
      `cloudOwnedFlags` so a preset's own value is superseded by the
      Outfit-derived one on both the cloud and fleet-node paths, exactly like
      `ctx-size`
- [x] 3.4 `argvFromDeployConfig` (`cmd/outfit/serve_daemon.go`): when
      `dc.Parallel > 0`, set `sel.Parallel = strconv.Itoa(dc.Parallel)` before
      calling `engine.params(sel)` — no new translation code, section 2
      already covers it
- [x] 3.5 Tests: `remote_deploy_test.go` covers `deployConfig`
      deriving `Parallel`, the preset-`np` fallback, and a preset-set
      `np`/`max-num-seqs` being dropped from `serveArgs` when `PARALLEL` is
      set; `serve_daemon_test.go` gets a llama.cpp (`TestArgvFromDeployConfigLlamacppScalesContext`)
      and a vLLM (`TestArgvFromDeployConfigVllmParallel`) case asserting the
      scaled/unscaled command from a `DeployConfig` with `ContextSize` and
      `Parallel` both set
- [x] 3.6 (found during implementation) `internal/daemon`'s
      `TestOpenAPISchemasMatchGoTypes` caught `daemon.StartRequest` (which
      embeds `remote.DeployConfig`) and `DeployConfig` itself now serialising
      a `parallel` field absent from `docs/openapi.yaml` — added it to the
      `DeployConfig` schema there
- [x] 3.7 (found while tracing callers for test coverage) Fixed a regression
      3.3 introduced: adding all three spellings to `cloudOwnedFlags` made
      `dropOwned` strip a vLLM preset's `max-num-seqs`, while the 3.2 fallback
      read only llama.cpp's `parallel` — so the value was dropped from
      `serveArgs` and captured by nothing, silently losing a setting that
      passed straight through before this change. The fallback now reads the
      runner's own spelling via a new `parallelPresetKey`

## 4. Cloud wire mirror (TypeScript, `remote/lambda`)

- [x] 4.1 Add an optional `parallel?: number` to the `DeployConfig` type and
      its validation in `remote/lambda/shared/deploy-config.ts` (mirrors
      `contextSize`, but not required)
- [x] 4.2 `daemon-boot.ts`'s `daemonDeployConfig`: copy `cfg.parallel` through
      to the JSON the instance's daemon reads (`JSON.stringify` drops an
      `undefined` property entirely, so an unset `parallel` is simply absent
      — matching the Go side's `omitempty`, confirmed by the round-trip test)
- [x] 4.3 Update `remote/test/deploy-config.test.ts` and
      `remote/test/start.test.ts` fixtures for the new optional field;
      confirm a `DeployConfig` with no `parallel` still validates exactly as
      today (`seed.test.ts` is unaffected — weight seeding has no context or
      parallelism concern)

## 5. Docs and examples

- [x] 5.1 `docs/outfit-file.md`: add `PARALLEL` to the keyword table and a
      rule paragraph next to `CONTEXT`'s, stating the per-engine mapping and
      the llama.cpp `ctx-size` scaling explicitly (point back to one place
      rather than re-explaining per engine)
- [x] 5.2 `docs/commands/serve.md`: document the three flags `PARALLEL`
      produces and the llama.cpp scaling behaviour, alongside the existing
      `CONTEXT`/`--ctx-size`/`--max-model-len` mapping table (added a new
      "Parallelism" section, plus the vLLM section the doc was missing
      entirely — needed a home for vLLM's own `CONTEXT`/`PARALLEL` mapping)
- [x] 5.3 README: extend the `Outfit` file example block with a commented
      `PARALLEL` line; add one line under "Serving a local model" cross-
      referencing the new behaviour (not a full re-explanation — link to the
      docs page)
- [x] 5.4 Extended `examples/llamacpp/qwen3.6-27b/` (Outfit + README) with a
      commented `PARALLEL 2` line and the resulting `--ctx-size 65536
      --parallel 2`, verified against the built binary; left `gemma4`
      untouched since its MTP draft model does not support parallel
      sequences (`np = 1` is required there, per `remote/preset.ini`'s own
      comment) and would be a misleading place to demonstrate this

## 6. Verification

- [x] 6.1 `go test ./... -cover` >= 80%, `gofmt -w ./...`, `go vet ./...`
      (scoped to `./cmd/...`/`./internal/...` — `remote/node_modules` carries
      gitignored CDK template files with `%`-containing names that are not
      valid Go, so a bare `./...` from the repo root trips on them; this is
      pre-existing and unrelated to this change)
- [x] 6.2 `npm test` (or the repo's TS test command) green under `remote/` for
      the `deploy-config`/`daemon-boot` changes — 114/114 passed
- [x] 6.3 Manual `outfit serve --dry-run` check for all three engines with and
      without `PARALLEL`, confirming the printed command matches design.md's
      worked example (`CONTEXT 128k` + `PARALLEL 2` → llama.cpp `--ctx-size
      256000 --parallel 2`; vllm → `--max-model-len 128000 --max-num-seqs 2`;
      omlx `PARALLEL 8` → `--max-concurrent-requests 8`)
- [x] 6.4 `openspec validate configure-parallelism --strict` passes (also
      re-ran `validate --all --strict` and `check-spec-purposes.sh` clean)
- [x] 6.5 Coverage pass over the callers rather than the changed functions
      alone, which is what surfaced 3.7. Added: the per-runner preset-key
      tests (both halves — the right spelling is read, another engine's is
      not); the daemon's store→reload round trip, so a slot count survives a
      restart between the push and the start; the `omitempty` wire contract,
      since the deploy Lambda rejects a `parallel` that is present and zero;
      the two fleet-wake outcomes, where deriving a start config is fatal to
      a wake but deliberately not to routing at a node already serving; a
      slot count with no context size, which only the fleet path can reach;
      and vLLM's unparseable-CONTEXT guard. The `bindAddressParams` error
      branches in the three params functions stay uncovered on purpose:
      reaching them needs a BASEURL that `url.Parse` rejects, and it accepts
      almost anything, so a test would pin a string's parseability rather
      than any behaviour of this change
