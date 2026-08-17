## 1. `internal/outfitsrc` (shared path/URL resolution)

- [x] 1.1 Create `internal/outfitsrc` with `IsURL(ref string) bool` (true for
  `http://`/`https://` prefixes).
- [x] 1.2 Implement `Resolve(base, ref string) (string, error)`: `ref` already
  absolute (URL or `filepath.IsAbs`) returns `ref` unchanged; `base` a URL
  resolves `ref` against it with `net/url`'s reference resolution; otherwise
  `filepath.Join(filepath.Dir(base), ref)`, matching today's behavior exactly.
- [x] 1.3 Implement `Fetch(ref string) ([]byte, error)`: `os.ReadFile` for a
  local path; for a URL, a package-level `http.Client` with a fixed timeout,
  a `GET`, a non-2xx status turned into an error naming the URL and status,
  and the body read through a capped reader so an oversized response errors
  instead of exhausting memory.
- [x] 1.4 Unit tests: `IsURL` on both schemes and on plain paths;
  `Resolve` across all four combinations (local base/relative ref, local
  base/absolute ref, URL base/relative ref, any base/absolute URL ref); `Fetch`
  against a local file, an `httptest.Server` success, a non-2xx status, and an
  oversized body.

## 2. Outfit path resolution over a URL

- [x] 2.1 In `cmd/outfit/main.go`'s `readOutfit`, branch on
  `outfitsrc.IsURL(path)` before the existing `os.Stat`/`os.ReadFile` pair: a
  URL ending in `/` gets `outfit.DefaultFile` appended (mirroring the local
  directory case), then `outfitsrc.Fetch` supplies the bytes to
  `outfit.Parse`, with the same error-wrapping the local path gets.
- [x] 2.2 Confirm every `readOutfit` caller (`apply`, `unapply`, `serve`,
  `alias`, `harness --outfit`, the `remote` subcommands) works unmodified,
  since they all go through this one chokepoint.
- [x] 2.3 Tests: `outfit apply <url>` against an `httptest.Server` serving a
  valid Outfit applies it; a directory-style URL (trailing `/`) fetches
  `<url>/Outfit`; a 404 or unreachable host surfaces a clear error naming the
  URL.

## 3. Alias registry support for a URL target

- [x] 3.1 In `cmdAlias` (`main.go`), skip `filepath.Abs` for a URL path and
  store it verbatim; keep `filepath.Abs` for a local path.
- [x] 3.2 In `resolveAlias`, skip the `os.Stat` dangling-target check when the
  registered value is a URL — let a real fetch failure surface later, at the
  point something actually reads it. Applied the same treatment to
  `outfitFromEnv` (the `OUTFIT_ALIAS` shortcut), which has the identical
  dangling-check pattern.
- [x] 3.3 In `writeAliases` (backing `outfit alias --list` and `outfit show`),
  skip the local liveness probe for a URL-valued entry; print it as-is with no
  "(missing)" annotation either way.
- [x] 3.4 Tests: `outfit alias -n <name> <url>` registers and round-trips
  through `outfit apply <name>`; `outfit alias --list` prints a URL entry
  without attempting a network call (verify via a URL that would fail if
  dialed); re-registering the same URL name without `--force` still fails as
  it does for a local path.

## 4. Lazy `PRESET` fetching

- [x] 4.1 Rewrite `resolvePresetPath` (`serve.go`) in terms of
  `outfitsrc.Resolve`.
- [x] 4.2 Swap the `os.ReadFile(presetPath)` calls in `buildServeArgv`
  (`serve.go`) and `deployConfigFor` (`remote.go`) for `outfitsrc.Fetch`.
- [x] 4.3 Tests: `outfit serve` against an Outfit with `PRESET
  https://.../preset.ini` fetches it and builds the expected argv; a relative
  `PRESET` under a URL-sourced Outfit resolves against that URL and fetches
  correctly; `outfit apply` on the same Outfit never dials the preset URL
  (assert via a URL that errors if reached).

## 5. Lazy path-form `REMOTE` fetching

- [x] 5.1 Export `remote.LoadConfigBytes(data []byte, source string, getenv
  func(string) string) (Config, error)` from `internal/remote`, refactoring
  `LoadConfigFile` to read the file and delegate to it.
- [x] 5.2 Change `resolveRemotePath` (`remote.go`) to take the full Outfit
  path (not a pre-computed directory) and resolve a path-form `REMOTE` via
  `outfitsrc.Resolve`; update its call sites — including `applySelection`/
  `removeSelection`/`fetchRemoteEnv`/`applyBeforeLaunch` in `main.go`, which
  also fed a pre-computed directory into remote resolution — to pass the raw
  Outfit path instead.
- [x] 5.3 Swap `remoteConfig`'s `os.ReadFile` (`remote.go`) for
  `outfitsrc.Fetch`, and `resolveRemoteConfigForOutfit` to
  `outfitsrc.Fetch` + `remote.LoadConfigBytes` instead of
  `remote.LoadConfigFile`. `remoteConfig` tolerates a URL 404 the same way it
  tolerates a missing local file (`outfitsrc.ErrNotFound`, the URL analogue of
  `os.IsNotExist`, added to `internal/outfitsrc` for this).
- [x] 5.4 Confirm the bare-name (per-user registry) branch of `REMOTE` is
  untouched — it never becomes a URL and keeps reading local disk.
- [x] 5.5 Tests: a `remote` subcommand against an Outfit with `REMOTE
  https://.../remote.json` resolves control URLs from the fetched config; a
  relative `REMOTE` under a URL-sourced Outfit resolves against that URL;
  `outfit apply`'s base-URL fallback fetches a URL-form `REMOTE` exactly once
  and only when `BASEURL` is absent, matching today's local-path behavior.

## 6. No adjacent `.env` for a URL-sourced Outfit

- [x] 6.1 In `applyOutfitEnv` (`remote.go`), skip the `.env`-beside-the-Outfit
  read when the resolved Outfit path is a URL, proceeding straight to the
  Outfit's own `ENV` instructions over the process environment. Added an
  `envFileDir` helper in `main.go` so `opencode.EnvResolver`'s callers (the
  separate `.env`-for-API-keys path `apply`/`harness` use) get the same
  explicit "no local directory" treatment rather than relying on a mangled
  URL path silently failing to read.
- [x] 6.2 Tests: a URL-sourced Outfit with `ENV` instructions still applies
  them with the existing precedence; no `.env` fetch/read is attempted
  (nothing to attempt it against).

## 7. Docs

- [x] 7.1 Update `docs/outfit-file.md`: an Outfit path may be a URL (with the
  trailing-slash directory convenience); `PRESET`/`REMOTE` may be a URL, or
  relative to a URL-sourced Outfit, fetched only when the consuming command
  needs it.
- [x] 7.2 Update `docs/commands/alias.md`: registering a URL, and that listing
  never probes a URL-valued alias.
- [x] 7.3 Do not touch `CHANGELOG.md` (handled by the release process).

## 8. Example

- [x] 8.1 Add `examples/remote-outfit/`, following the existing
  `examples/<name>/{Outfit,README.md}` shape (see
  `examples/llamacpp/qwen3.6-27b/`): an `Outfit` with `PROVIDER llamacpp`,
  `ALIAS`, `CONTEXT`, and `PRESET ./preset.ini`, plus the matching
  `preset.ini`, written to be hosted as a pair behind any static file URL.
- [x] 8.2 Write `examples/remote-outfit/README.md` covering: publishing the
  two files behind a URL (a gist's raw URL, a GitHub raw URL, an internal
  static host, or `python3 -m http.server` for local testing);
  `outfit apply https://.../Outfit`; registering it —
  `outfit alias -n team-default https://.../Outfit` — and reusing the name —
  `outfit apply team-default`; and `outfit serve team-default`, calling out
  that `serve` is what fetches `preset.ini` (resolved relative to the Outfit's
  URL), and that `apply` never does, since it does not consume `PRESET` —
  the same lazy-fetch behavior a local `PRESET` already has.
- [x] 8.3 Link the new example from `README.md`'s example listing (if one
  exists) or from `docs/outfit-file.md`'s "Examples" section, alongside the
  existing provider examples.

## 9. Verification

- [x] 9.1 `gofmt -l .` (clean) and `go test ./... -race -cover` (all packages
  pass). `internal/remote` sits at 78.7% (was 78.6% before this change,
  pre-existing and unrelated to it); every other touched package is at or
  above 80%, `internal/outfitsrc` at 85.7%.
- [x] 9.2 Manually exercised: a local `python3 -m http.server` serving
  `examples/remote-outfit/` plus `httptest.Server`-backed integration tests
  (`cmd/outfit/url_source_test.go`) cover `outfit apply <url>`,
  `outfit alias -n <name> <url>` followed by `outfit apply <name>`,
  `outfit serve` against a `PRESET`-over-URL Outfit (and one relative to a
  URL-sourced Outfit), and `outfit remote status` against a `REMOTE`-over-URL
  Outfit (ditto); each was confirmed, via request counters, to fetch only what
  it needs and nothing eagerly.
- [x] 9.3 Ran the new `examples/remote-outfit/` example end to end against a
  local `python3 -m http.server`: `outfit apply` from the raw URL, `outfit
  alias -n` + `outfit apply <name>`, `outfit alias --list`, and `outfit serve
  <name> --dry-run` (which, and only which, fetched `preset.ini` and printed
  the expected `llama-server` command) — all matched the README exactly.
