## 1. Resolution

- [x] 1.1 Add an `OUTFIT_ALIAS` resolver in `cmd/outfit/main.go` beside
  `resolveAlias`: read the variable, return early when unset or empty, reject a
  value that is not name-shaped (`config.ValidAliasName`), look it up with
  `config.Load`/`File.Alias`, and `os.Stat` the target. Each failure names
  `OUTFIT_ALIAS` — unregistered points at `outfit alias --list`, dangling
  suggests re-pointing with `outfit alias -n <name> <path>` or `outfit unalias
  <name>`. No shadowing check.
- [x] 1.2 Call it from `readOutfit` in the `path == ""` branch, before the
  `./Outfit` default, and print `Using OUTFIT_ALIAS "<name>" (<path>)` to
  stderr when it decides the path.
- [x] 1.3 Make `cmdAlias` pass `outfit.DefaultFile` instead of an empty path
  when it has no positional argument, so registering ignores the variable, and
  say why in a comment.
- [x] 1.4 Extend the "no `Outfit` found in the current directory" error to
  mention `OUTFIT_ALIAS` alongside the path and alias it already suggests.
- [x] 1.5 Update the doc comments on `readOutfit` and `resolveAlias` to cover
  the environment source and why it is not shadowed by a file on disk.

## 2. Tests

- [x] 2.1 Add a `TestMain` to `cmd/outfit` that unsets `OUTFIT_ALIAS` before
  running, so a developer's exported value cannot reach the suite.
- [x] 2.2 In `cmd/outfit/alias_test.go`, cover resolution: the variable
  supplies the Outfit for `apply` in a directory with no `Outfit`, and the
  stderr note names the variable, the alias and the path.
- [x] 2.3 Cover precedence: an explicit path argument and an explicit alias
  argument both beat the variable; the variable beats a `./Outfit` present in
  the working directory.
- [x] 2.4 Cover the no-shadowing rule: a file named the same as the value in
  the working directory does not displace the registry lookup, and no shadowing
  note is printed.
- [x] 2.5 Cover the errors: unset behaves exactly as today, empty is ignored, a
  path-shaped value, an unregistered value and a dangling one each fail with
  `OUTFIT_ALIAS` named.
- [x] 2.6 Cover the exclusions: `outfit harness` with no argument and no
  `-O` applies nothing, `outfit harness -O` applies the variable's Outfit, and
  `outfit alias` beside a different `Outfit` registers the local one.
- [x] 2.7 Cover one non-`apply` caller end to end (`serve --dry-run` or a
  `remote` subcommand against the existing `httptest.Server` harness) to prove
  the choke point reaches every command.

## 3. Documentation

- [x] 3.1 Add `OUTFIT_ALIAS` to `docs/env-vars.md` and the env-var table in
  `docs/README.md`, stating the precedence and that it is a registry name.
- [x] 3.2 Document it in `docs/commands/alias.md`, and note it on the pages for
  the commands it reaches: `apply.md`, `unapply.md`, `serve.md`, `harness.md`,
  `remote.md`.
- [x] 3.3 Cover it in `README.md` under "Aliases", including that it changes
  which Outfit is the default rather than whether one is applied.
- [x] 3.4 Update the "Alias registry" paragraph in `AGENTS.md` with the
  environment source and the two rules that differ from an argument (no
  shadowing, `outfit alias` opted out).

## 4. Verification

- [x] 4.1 `gofmt` the touched files and run `go test ./... -cover`, keeping
  coverage at or above 80%.
- [x] 4.2 Sanity-check by hand in a scratch directory: export `OUTFIT_ALIAS`,
  run `outfit apply` and `outfit serve --dry-run` from an unrelated directory,
  then unset it and confirm the previous behaviour returns.
- [x] 4.3 `openspec validate add-outfit-alias-env --strict`.
