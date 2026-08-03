## 1. Fix EnvResolver precedence

- [ ] 1.1 In `internal/opencode/opencode.go`, swap the two branches of the closure returned by `EnvResolver` so it consults `os.Getenv(name)` first and falls back to `readEnvFileVar(...)` only when the process value is empty
- [ ] 1.2 Update the `EnvResolver` doc comment (lines 17–26) to state the process environment wins and the `.env` fills gaps, matching the `remote` rule
- [ ] 1.3 Update existing tests that assert the old `.env`-wins order (search `internal/opencode` and `cmd/outfit` for `EnvResolver`/`.env` precedence assertions), and add a test proving an exported variable beats a `.env` value and that the `.env` still fills a gap

## 2. Harness launches with the full local environment

- [ ] 2.1 In `cmd/outfit/main.go`, build the launched agent's environment as: `os.Environ()`, then the whole adjacent `.env` (via `opencode.ParseEnvFile`) for keys not already present, then `sel.Env` entries appended unconditionally so they override — keeping `harnessEnv`'s catalogue-key resolution for keys still unset afterwards
- [ ] 2.2 Thread the worn Outfit's `Selection` (its `Env`) and directory into the launch-env construction; do this only when an Outfit is worn (`outfitPath.set`), leaving the no-Outfit path on `os.Environ()` alone
- [ ] 2.3 Ensure outfit's own process environment is never mutated on this path (no `os.Setenv`); the values live only in the child's `cmd.Env`
- [ ] 2.4 Confirm append ordering gives the precedence `ENV` > process env > `.env` (a later assignment to the same key in `cmd.Env` wins), and that provider-key gap-filling still runs

## 3. Tests for the harness launch environment

- [ ] 3.1 Test that a variable in the adjacent `.env`, unset in the environment, reaches the launched agent (gap-fill)
- [ ] 3.2 Test that an exported variable is not overridden by the `.env`
- [ ] 3.3 Test that an Outfit `ENV` instruction overrides both an exported variable and the `.env`
- [ ] 3.4 Test that launching with no Outfit adds no `.env`/`ENV` values
- [ ] 3.5 Guard test: outfit's own process environment is unchanged after a harness launch (the values only shaped the child's env)

## 4. Docs and defect

- [ ] 4.1 Update `README.md` and `docs/outfit-file.md` to state the single precedence rule (`ENV` > process env > `.env`) and that `outfit harness` respects the adjacent `.env` and the Outfit's `ENV`
- [ ] 4.2 Update the env-resolution note in `AGENTS.md` to describe the corrected `EnvResolver` precedence and the harness launch-env behaviour
- [ ] 4.3 Close (or update) the GitHub defect filed from `remote-respect-local-env` about `EnvResolver` resolving `.env` before the process environment, referencing this change

## 5. Verification

- [ ] 5.1 `gofmt`/`go vet ./...` and `go test ./... -cover` (keep coverage >= 80%)
- [ ] 5.2 Manual smoke: with an Outfit plus a `.env` setting a variable, run `outfit harness ./Outfit -- <agent print-env command>` and confirm the value reaches the agent; export the same variable and confirm it wins; add an `ENV` line and confirm it overrides both
