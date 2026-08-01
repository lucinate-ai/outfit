## Why

[oMLX](https://omlx.ai) is a local inference server for Apple Silicon, built on Apple's
MLX framework, exposing an OpenAI-compatible API. `outfit` today knows two local engines
it can point a harness at (`llamacpp`, `vllm`) and exactly one it can *launch*
(`llama-server`), so Mac users get half the flow: they can dress the agent, but not start
the server behind it from the same file.

Adding oMLX also settles an inconsistency the code already claims is settled. `runnerFor`
documents itself as "PROVIDER already names the engine — `outfit serve` starts that engine
locally", and the CLI usage says "PROVIDER picks the engine, just as it does for serve".
Neither is true: `cmdServe` never reads `PROVIDER` and unconditionally runs
`llama-server`, so an Outfit naming any other provider silently starts the wrong server.

## What Changes

- Add an `omlx` provider to the catalogue: an OpenAI-compatible endpoint defaulting to
  `http://localhost:8000/v1`, overridable with `OMLX_BASE_URL`, keyless on localhost and
  key-bearing when pointed elsewhere (`apiKeyOptional`, like `llamacpp`), and Pi-capable
  via `openai-completions`.
- **BREAKING**: `outfit serve` selects its engine from `PROVIDER` rather than always
  running `llama-server`. `llamacpp` and `omlx` are served; every other provider is now a
  loud error instead of a silently wrong command.
- Teach `internal/preset` that flag *spelling* is per-engine. Parsing stays shared; a
  `Dialect` carries the alias and boolean tables, with `LlamaCpp` the default so existing
  callers are unaffected, and `OMLX` passing long-form keys through untouched.
- Give `serve` a per-engine install hint, and find the oMLX CLI on `PATH` or in the macOS
  app bundle, since oMLX ships as a signed app rather than a `PATH` install.
- `serve` never passes an API key to oMLX. It prints the command it runs, and oMLX takes
  its key as a flag, so a resolved secret would land on screen and in the process table.

Non-goals:

- `outfit remote deploy` stays closed to oMLX. The cloud side is CUDA/NVIDIA EC2 and oMLX
  is Apple Silicon only, so `runnerFor` keeps rejecting it — correctly.
- No new Outfit keyword. `PROVIDER` names the engine, as the codebase already intends.

## Impact

- Affected specs: `provider-catalog`, `local-serving`
- Affected code: `internal/catalog/providers.yaml`, `internal/preset/preset.go`,
  `cmd/outfit/serve.go` (extracted from `main.go`), `cmd/outfit/main.go` (usage text)
- Migration: an Outfit that named a non-engine `PROVIDER` and relied on `serve` running
  `llama-server` anyway must change its `PROVIDER` to `llamacpp`. That configuration was
  already producing a command for an engine the Outfit did not name.
