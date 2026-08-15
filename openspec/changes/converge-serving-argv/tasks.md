## 1. Extract the shared argv assembly

- [x] 1.1 Add `subcommandFor(engine serveEngine, sel outfit.Selection) []string`
  (the engine's subcommand plus its positional model, copied so appending never aliases
  the engine's own slice) and `assembleEngineArgv(engine serveEngine, subcommand
  []string, params []preset.Param, trailing []string) []string` (binary + subcommand +
  `engine.dialect.Flags(params)` + trailing) in `cmd/outfit/serve.go`.
- [x] 1.2 Route the daemon/wake path through it: `argvFromDeployConfig`
  (`cmd/outfit/serve_daemon.go`) keeps reducing its `DeployConfig` to a selection and
  deriving `engine.params`, but assembles via
  `assembleEngineArgv(engine, subcommandFor(engine, sel), params, dc.ServeArgs)` instead
  of the inline `append`s.
- [x] 1.3 Route the preset-less branch of `buildServeArgv` (`cmd/outfit/serve.go`)
  through `assembleEngineArgv(engine, subcommandFor(engine, sel), params, nil)`. The
  preset branch keeps calling `pre.CommandIn`, but sources its subcommand from
  `subcommandFor` too, so all three paths draw the subcommand from one place.

## 2. Pin the emitted command

- [x] 2.1 Golden argv tests: for each servable engine (`llamacpp`, `omlx`, `vllm`),
  assert the exact command assembled for (a) a preset-less Outfit and (b) the equivalent
  deploy config, so the convergence is proven a no-op on the output (a vLLM case also
  pins the positional model and its `serveArgs` trailing).

## 3. Verification

- [x] 3.1 `gofmt -l cmd internal`, `go build ./...`, `go vet ./...`, and
  `go test ./... -cover` — green, coverage >= 80%.
- [x] 3.2 `openspec validate converge-serving-argv --strict` — clean.
