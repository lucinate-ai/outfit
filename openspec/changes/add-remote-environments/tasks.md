## 1. Registry paths and resolution (`internal/remote`)

- [ ] 1.1 Add `EnvDir(name string) string` → `${XDG_CONFIG_HOME:-~/.config}/outfit/remotes/<name>` and `EnvConfigPath(name string) string` → `<EnvDir>/remote.json`, mirroring `ConfigPath()`'s XDG logic.
- [ ] 1.2 Add `IsEnvName(value string) bool`: true when the value has no path separator and no `.json` suffix (a bare name); false (→ treat as a file path) otherwise.
- [ ] 1.3 Add `ListEnvironments() ([]EnvInfo, error)`: scan `.../outfit/remotes/`, read each `<name>/remote.json`, return name + base URL + region + a readable/missing flag; empty (not an error) when the dir is absent.
- [ ] 1.4 Add tests: `IsEnvName` table (names vs `./x`, `/abs`, `x.json`); `EnvConfigPath`; `ListEnvironments` over a temp `remotes/` incl. a dir with no/invalid `remote.json`.

## 2. REMOTE resolution: name or path

- [ ] 2.1 In `resolveRemoteConfig` (`cmd/outfit/remote.go`), when a `REMOTE` value is a bare name (`IsEnvName`), load from `EnvConfigPath(name)`; otherwise resolve as a file via `remoteConfigPath` as today.
- [ ] 2.2 Apply the same name-or-path rule in `remoteBaseURL` so apply's base-URL lookup matches the control commands.
- [ ] 2.3 Replace the no-Outfit single-file fallback (`remote.LoadConfig`) with the `default` environment (`EnvConfigPath("default")`).
- [ ] 2.4 Add read-through migration: when `default`'s `remote.json` is absent but the legacy `~/.config/outfit/remote.json` exists, use the legacy file as `default` (no rewrite).
- [ ] 2.5 Tests: an Outfit with `REMOTE <name>` resolves via the registry; `REMOTE ./remote.json` still resolves beside the Outfit; no-Outfit uses `default`; legacy file read-through works.

## 3. `outfit remote ls`

- [ ] 3.1 Add `cmdRemoteList(args []string) error` that prints each environment from `ListEnvironments()` with base URL + region, marking missing/unreadable ones, and a plain "no environments" line when empty; contacts no endpoint.
- [ ] 3.2 Add the `case "ls": return cmdRemoteList(rest)` to `cmdRemote` and widen its usage string and unknown-subcommand error to include `ls`.
- [ ] 3.3 Tests: listing two environments; a missing-`remote.json` marker; empty registry message. Hermetic (temp `remotes/`), no AWS.

## 4. Help text and completion

- [ ] 4.1 Update `usage()` and the package doc comment in `cmd/outfit/main.go` to list `ls` under the `remote` group and describe the environment-name form of `REMOTE`.
- [ ] 4.2 Add `ls` to the `remote` entry's subcommands in `cmd/outfit/complete.go`; confirm `TestCompletionCoversDispatch` passes.

## 5. Docs

- [ ] 5.1 Document the environment-name form of `REMOTE`, the `~/.config/outfit/remotes/<name>/` layout, the `default` fallback, and the legacy-file migration in `docs/commands/remote.md` and `docs/outfit-file.md`.
- [ ] 5.2 Add `outfit remote ls` to `docs/commands/remote.md`.

## 6. Validation

- [ ] 6.1 `go test ./... -cover` (keep ≥ 80%), `gofmt`.
- [ ] 6.2 Manual: register two environments under `~/.config/outfit/remotes/`, confirm `outfit remote ls` lists both, an Outfit with `REMOTE <name>` resolves, and `REMOTE ./remote.json` still works.
