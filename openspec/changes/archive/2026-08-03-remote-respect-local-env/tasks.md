## 1. ENV keyword in the Outfit parser

- [x] 1.1 Add `EnvVar` (`struct{ Key, Value string }`) and `Env []EnvVar` to `outfit.Selection` in `internal/outfit/outfit.go`
- [x] 1.2 Add `kwEnv = "env"` to the keyword constants and to `canonicalKeyword`, and include `ENV` in the "unknown keyword" error message
- [x] 1.3 In `Parse`, special-case `ENV` before the single-set `seen` check so it may repeat; split its value on the first `=`, requiring a non-empty key, and append to `sel.Env`; fail with a line-numbered error on a value with no `=` or an empty key
- [x] 1.4 Emit `ENV KEY=VALUE` lines in `Format` (after `REMOTE`)
- [x] 1.5 Update the package doc comment in `outfit.go` to document `ENV`
- [x] 1.6 Add parser tests: `ENV` once, `ENV` repeated (not a duplicate error), malformed value (no `=` / empty key), value with whitespace rejected, and a `Format` round-trip that includes `ENV`

## 2. Whole-file .env parser

- [x] 2.1 Add `ParseEnvFile(path string) (map[string]string, error)` to `internal/opencode/opencode.go`, reusing the trim/surrounding-double-quote convention of `readEnvFileVar`; skip blank lines, `#` comments, lines without `=`, and empty values; last wins on a duplicate key; missing file returns an empty map and no error
- [x] 2.2 Remove the dead dangling comment above `EnvResolver` (the `envFilePath` reference at lines 17–18)
- [x] 2.3 Add tests for `ParseEnvFile`: multiple vars, quoted values, comments/blanks ignored, empty values skipped, missing file, duplicate-key last-wins

## 3. Load the local environment in the remote commands

- [x] 3.1 Add `applyOutfitEnv(sel outfit.Selection, dir string) error` in `cmd/outfit/remote.go`: load `filepath.Join(dir, ".env")` via `ParseEnvFile` and `os.Setenv` each var only when `os.Getenv(key) == ""` (process env wins), then `os.Setenv` each `sel.Env` entry unconditionally (`ENV` overrides both); document that it mutates the process env for the AWS SDK and that `ENV` is local-only
- [x] 3.2 Add the `internal/opencode` import to `remote.go`
- [x] 3.3 Call `applyOutfitEnv` in `resolveRemoteConfig` in both branches that read an Outfit, after `readOutfit` and before `remote.LoadConfigFile` (covers start/stop/status)
- [x] 3.4 Call `applyOutfitEnv` in `cmdRemoteDeploy` after `readOutfit` and before `remote.LoadAWSConfig`/`resolveRegion`
- [x] 3.5 Confirm `deployConfigFor`/`remote.DeployConfig` do not carry `ENV`/`.env` values (no code change expected; this is the local-only guarantee)

## 4. Tests for the remote-command behaviour

- [x] 4.1 Test precedence via `applyOutfitEnv`: `ENV` > process env > `.env` (each pairing), using a temp dir with a `.env` and a `Selection` with `Env`
- [x] 4.2 Test that a `.env` value beside an Outfit reaches a remote command's config/region resolution (e.g. an `OUTFIT_REMOTE_*`/`AWS_REGION` value flows through, mirroring existing `remote_*_test.go` patterns)
- [x] 4.3 Guard test: `outfit remote deploy` never puts `ENV`/`.env` values into the `remote.DeployConfig` sent to the deploy endpoint

## 5. Docs and defect

- [x] 5.1 Update `README.md`: add `ENV` to the Outfit keyword list; note the remote commands respect the adjacent `.env` (process env wins) and that `ENV` overrides both and is local-only
- [x] 5.2 Update `docs/outfit-file.md` with the `ENV` keyword and the precedence
- [x] 5.3 Update `AGENTS.md`'s env-resolution note to cover the remote path and `ENV`
- [x] 5.4 File a GitHub defect (`lucinate-ai/outfit`) that `opencode.EnvResolver` resolves `.env` before the process environment — the opposite of the remote commands' new rule — and that the provider-key path should be reconciled (and consider extending `ENV` support there)

## 6. Verification

- [x] 6.1 `gofmt`/`go vet ./...` and `go test ./... -cover` (keep coverage >= 80%)
- [x] 6.2 Manual smoke: in a temp dir, an `Outfit` (with `REMOTE <name>`) plus a `.env` setting bogus `OUTFIT_REMOTE_START_URL`/`OUTFIT_REMOTE_STOP_URL`; run `outfit remote status ./Outfit` and confirm it attempts those URLs; add an `ENV OUTFIT_REMOTE_STOP_URL=...` line and confirm it overrides the `.env`
