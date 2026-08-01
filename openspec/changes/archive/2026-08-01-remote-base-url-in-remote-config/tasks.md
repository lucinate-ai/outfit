## 1. The remote deployment stops generating an Outfit

- [x] 1.1 Commit `remote/Outfit` with no `BASEURL`, its comments explaining that
      it is hand-maintained and where the address comes from; drop `Outfit` from
      `remote/.gitignore`
- [x] 1.2 Delete `remote/Outfit.example`
- [x] 1.3 Cut the Outfit templating out of `remote/scripts/write-config.mjs` so
      it writes only `remote.json`, and have it fill `base_url` from the stack's
      separate `BaseUrl` output when the config output predates the field
- [x] 1.4 Add `base_url` to the `OutfitRemoteConfig` output in
      `remote/lib/llm-stack.ts`

## 2. The CLI reads the base URL from the remote config

- [x] 2.1 Add the optional `BaseURL` field to `remote.Config`, documenting that
      no control call uses it
- [x] 2.2 Add `remoteBaseURL` in `cmd/outfit/remote.go` — a lenient read that
      yields "" for a config that is absent or has no base URL — and make
      `remoteConfigPath` take a directory so both callers share it
- [x] 2.3 Fill the base URL in `applySelection` when the Outfit states none and
      names a `REMOTE`, reporting where it came from

## 3. Tests

- [x] 3.1 Applying an Outfit with `REMOTE` and no `BASEURL` takes the base URL
      from the remote config and says so
- [x] 3.2 An Outfit's own `BASEURL` wins over the remote config's
- [x] 3.3 Applying succeeds when the remote config does not exist yet
- [x] 3.4 `gofmt`, `go vet`, `go test ./...`, and `remote/`'s `tsc --noEmit` and
      vitest suite all pass, and `scripts/check-no-cloud-identifiers.sh` still
      passes with `remote/Outfit` tracked

## 4. Docs

- [x] 4.1 `remote/README.md`: `pnpm run deploy` generates only `remote.json`;
      the Outfit is committed and hand-maintained
- [x] 4.2 `docs/commands/remote.md` and `README.md`: `base_url` in the remote
      config, what it is for, and that the control calls do not use it
- [x] 4.3 `docs/commands/apply.md` and `docs/outfit-file.md`: the fallback and
      its precedence
- [x] 4.4 `AGENTS.md`: the remote config's fields, `remote/Outfit` as the
      committed exception to the gitignored deployment state, and an
      architecture note on where the base URL comes from
