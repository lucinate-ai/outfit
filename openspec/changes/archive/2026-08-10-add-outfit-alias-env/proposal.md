## Why

An alias saves typing a path, but only if you type the alias — every command
still needs `qwen3.6-27b` spelled out, or a `cd` into the directory that holds
the `Outfit`. There is no way to say "this shell is wearing qwen" once and have
`apply`, `serve`, `daemon` and `remote` all agree. Every other machine-local
runtime choice already has an environment variable (`OUTFIT_HARNESS`,
`OUTFIT_BASE_URL`, `OUTFIT_PROVIDERS`, `OUTFIT_CONFIG_DIR`); the Outfit itself
is the one missing.

## What Changes

- Add `OUTFIT_ALIAS`: a registered alias name that stands in for the `[path]`
  argument when a command is given none. `export OUTFIT_ALIAS=qwen3.6-27b` then
  makes `outfit apply`, `outfit serve`, `outfit daemon` and the `remote`
  subcommands act on that Outfit from any directory.
- Precedence for the Outfit a command acts on: explicit path or alias argument,
  then `OUTFIT_ALIAS`, then `./Outfit`. The variable changes **which** Outfit is
  the default — it never causes a command to act on an Outfit it would
  otherwise have left alone.
- `OUTFIT_ALIAS` holds a registry name only, never a path, and is looked up
  directly: unlike an argument of the same spelling, a same-named file in the
  working directory does not shadow it. A value that is not registered, or that
  points at a file that has gone, fails naming the variable instead of dropping
  through to a confusing "no such file".
- When `OUTFIT_ALIAS` decides the Outfit, the command says so on stderr, the
  same way an alias argument already does.
- `outfit alias [path]` is excluded: its bare form means "register the Outfit
  here", so honouring the variable there would only ever re-register what is
  already registered.
- Documentation: `README.md`, `docs/commands/alias.md`, the command pages that
  take a path, and `AGENTS.md`.

## Capabilities

### New Capabilities

None — this extends the existing alias registry rather than introducing a
capability of its own.

### Modified Capabilities

- `alias-registry`: alias resolution gains an environment source — a new
  requirement covering `OUTFIT_ALIAS`, its direct (unshadowed) lookup, its
  error cases and its stderr report.
- `outfit-files`: the "Outfit path resolution" requirement gains the
  `OUTFIT_ALIAS` step between an explicit argument and the `./Outfit` default,
  and records that `outfit alias` and a bare `outfit harness` are unaffected.

## Impact

- `cmd/outfit/main.go` — `readOutfit`, the single choke point every Outfit
  command shares, gains the environment fallback; `cmdAlias` opts out of it.
  The not-found hint for a missing `./Outfit` mentions the variable.
- Behaviour of `outfit apply`/`unapply`/`serve`/`daemon`/`harness --outfit`/
  `remote *` when no path is given — only in a shell that sets the variable.
- Tests: `cmd/outfit/alias_test.go` (resolution, precedence, errors),
  plus coverage that a bare `outfit harness` and `outfit alias` stay unchanged.
- No new dependencies; `internal/config` is unchanged.
