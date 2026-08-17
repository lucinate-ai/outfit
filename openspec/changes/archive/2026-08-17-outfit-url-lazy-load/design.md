## Context

Every "path to another file" outfit resolves today follows the same two-line
shape:

```go
func resolvePresetPath(presetValue, outfitPath string) string {
	if filepath.IsAbs(presetValue) {
		return presetValue
	}
	return filepath.Join(filepath.Dir(outfitPath), presetValue)
}
```

`resolveRemotePath`'s path branch (`remoteConfigPath`) is the same shape, and
`readOutfit` itself does the local-disk equivalent for the Outfit path
argument (`os.Stat` for a directory, then `os.ReadFile`). None of the three
knows anything but `os.*` — there is no notion of "this reference is remote."

The three consuming commands already pull each of these files on its own
schedule, not eagerly at parse time: `outfit apply` reads the Outfit and,
separately, reads a path-form `REMOTE`'s `remote.json` whenever `REMOTE` is
set — to name the harness provider after the environment (`remoteEnvName`,
unconditional) and, only when the Outfit states no `BASEURL` of its own, to
fall back to the deployment's own address (`remoteBaseURL`) — but it never
touches `PRESET`. `outfit serve` and `outfit remote deploy` are the only
readers of `PRESET`. The `remote` subcommands are the only readers of a
path-form `REMOTE` beyond those two `apply`-time reads. This proposal's
"lazy" requirement is therefore not a new behavior to build — it is the
existing call graph, preserved as-is, by making the one primitive every one
of these call sites bottoms out on (`os.ReadFile`) dispatch to an HTTP fetch
when the reference is a URL, without moving *when* or *how often* any call
site fires.

## Goals / Non-Goals

**Goals:**

- Accept an `http://`/`https://` URL anywhere an Outfit path, `PRESET`, or
  path-form `REMOTE` is accepted today, alongside — never instead of — the
  existing local-path behavior.
- Let `outfit alias` name a URL, with the same registry, the same resolution
  precedence (a real local path still beats a same-named alias), and the same
  "parsed at registration time" validation.
- Keep every existing local-only flow byte-for-byte unchanged: this is
  additive, not a rewrite of path resolution.
- Preserve the existing pull-on-demand timing for `PRESET` and `REMOTE` — no
  call site starts fetching a file it did not already read, and no call site
  starts fetching it earlier than it already did.
- Put sane, fixed bounds on a remote fetch (timeout, response size cap) so a
  slow or oversized endpoint fails fast and cleanly instead of hanging or
  exhausting memory.

**Non-Goals:**

- No authentication for remote fetches (bearer tokens, custom headers, basic
  auth). A URL is fetched as a plain, unauthenticated `GET`; a private Outfit
  needs a signed/capability URL from its host, the same way a private
  Hugging Face repo already needs its own token flow today.
- No caching of fetched content across invocations. Each command run is one
  fetch of whatever it needs; nothing is written to a local cache directory.
  (`outfit remote bootstrap`'s CDK source cache is a different, existing
  mechanism and is out of scope.)
- No change to the bare-name form of `REMOTE` (the per-user environment
  registry). That path is, and remains, entirely local.
- No new `Outfit` keyword. `PROVIDER`/`MODEL`/…/`PRESET`/`REMOTE` already
  carry an opaque string value; a URL is just a new shape that value can take.

## Decisions

### D1: One small shared package, `internal/outfitsrc`

Three call sites (the Outfit path itself, `PRESET`, path-form `REMOTE`) need
the same three operations, so they get one package rather than three
near-identical copies:

```go
package outfitsrc

// IsURL reports whether ref names an http(s) resource rather than a local path.
func IsURL(ref string) bool

// Resolve joins ref against base, which may itself be a local path or a URL:
//   - ref is already an absolute URL, or an absolute local path → ref, unchanged
//   - base is a URL → url.Parse(base).ResolveReference(ref), so a relative
//     ref resolves the way a relative link resolves against a base document
//     (dropping base's own last path segment)
//   - otherwise → filepath.Join(filepath.Dir(base), ref), today's behavior
func Resolve(base, ref string) (string, error)

// Fetch reads ref's content: os.ReadFile for a local path, a bounded HTTP GET
// for a URL.
func Fetch(ref string) ([]byte, error)
```

This lives under `internal/` (not `cmd/outfit`) because it is domain logic
with its own unit tests, matching this repo's existing split — the CLI layer
orchestrates, the `internal/*` packages hold the logic being orchestrated.

It does not live inside `internal/outfit`: that package is deliberately a
leaf grammar package (`Parse`/`Format`, no I/O — see its doc comment), and
folding fetch/HTTP concerns into it would give the pure parser a network
dependency it does not need for its own job. `internal/outfitsrc` depends on
nothing outfit-specific; `internal/outfit` stays exactly as it is.

*Alternative — inline the branch at each of the three call sites*: rejected.
The three sites already share the "absolute wins, else join against the
base's directory" shape almost verbatim; adding a URL branch to each
separately would triple the logic (and its tests) for no benefit over one
shared, three-function package.

### D2: `readOutfit` dispatches on `IsURL` before anything filesystem-shaped

```go
func readOutfit(usage, path string) (outfit.Selection, string, error) {
	if path == "" {
		path = outfit.DefaultFile
	}
	if aliased, ok, err := resolveAlias(path); err != nil {
		return outfit.Selection{}, path, err
	} else if ok {
		path = aliased
	}
	if outfitsrc.IsURL(path) {
		if strings.HasSuffix(path, "/") {
			path += outfit.DefaultFile
		}
		data, err := outfitsrc.Fetch(path)
		// ... same Parse/wrap-error tail as today
	}
	// unchanged: os.Stat for a directory, then os.ReadFile
}
```

The `os.Stat`-for-a-directory branch is skipped entirely for a URL — there is
no local directory to stat, and a URL's own trailing-slash convention plays
the same role. Every caller downstream still receives a single `path` string
that is either a local path or a URL; nothing further up the call chain
(`cmdApply`, `cmdAlias`, `cmdHarness`, the `remote` command group) needs to
know which, because every place that later needs to act on `path` already
goes through `outfitsrc` too (D3, D4).

### D3: `PRESET`/`REMOTE` resolution goes through `outfitsrc.Resolve` + `outfitsrc.Fetch`

`resolvePresetPath` becomes a thin wrapper — `outfitsrc.Resolve(outfitPath,
sel.Preset)` — and its two callers (`buildServeArgv` in `serve.go`,
`deployConfigFor` in `remote.go`) swap their `os.ReadFile(presetPath)` for
`outfitsrc.Fetch(presetPath)`. Neither caller's surrounding logic changes: the
bytes still feed `preset.Parse` exactly as before. Nothing new fetches a
`PRESET` — the same two call sites that read it today are the only two that
read it after this change.

`remoteConfigPath` (the path-form branch of `resolveRemotePath`) becomes
`outfitsrc.Resolve(outfitDir..., ...)` — except `resolveRemotePath` currently
takes `outfitDir` (already `filepath.Dir(outfitPath)`), which throws away the
information `Resolve` needs when the base is a URL (there is no
"`filepath.Dir` of a URL" the caller can hand it that also lets `Resolve`
apply URL-relative-reference semantics). So `resolveRemotePath` changes to
take the full `outfitPath` and calls `outfitsrc.Resolve(outfitPath,
remoteValue)` itself, letting `Resolve` (D1) decide path-join vs.
URL-reference resolution. Its three call sites in `remote.go` pass the Outfit
path they already have in scope instead of pre-computing its directory.

`remoteConfig` (the `os.ReadFile` in `remote.go` behind the manual
`remoteBaseURL`/`remoteEnvName` lookups) swaps to `outfitsrc.Fetch`.
`resolveRemoteConfigForOutfit` goes through `remote.LoadConfigFile`, which
does its own internal `os.ReadFile` — see D4.

### D4: Export a bytes-based entry point from `internal/remote`

`remote.LoadConfigFile(path, getenv)` reads the file itself
(`internal/remote/remote.go:99-114`) before handing off to the unexported
`finishConfig` (env overrides + validation). A URL-sourced `remote.json`
needs that same env-override/validation pass without going through
`os.ReadFile`. Rather than duplicate `finishConfig`'s logic at the call site,
`internal/remote` gains:

```go
func LoadConfigBytes(data []byte, source string, getenv func(string) string) (Config, error) {
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing %s: %w", source, err)
	}
	return finishConfig(cfg, getenv, source)
}
```

`LoadConfigFile` becomes a two-line wrapper (`os.ReadFile` +
`LoadConfigBytes`), and `resolveRemoteConfigForOutfit` in `cmd/outfit/remote.go`
becomes `outfitsrc.Fetch` + `remote.LoadConfigBytes`. This is the one place
the change reaches into `internal/remote` rather than stopping at the
`cmd/outfit` call sites, because the env-override/validation logic
(`finishConfig`) is not something `cmd/outfit` should reimplement.

### D5: Alias registry stores a URL verbatim; no schema change

`config.File.Aliases` is already `map[string]string` — a URL is just a string
that happens to contain `://`. The only code that assumed "path" specifically
is `cmdAlias`'s `filepath.Abs(path)` call before storing it (main.go:593-596):
that becomes `if outfitsrc.IsURL(path) { abs = path } else { abs, err =
filepath.Abs(path) }`. `resolveAlias`'s dangling-target check
(`os.Stat(path)`, main.go:466-468) skips for a URL — probing it would mean a
network call on every single alias-resolving command (`apply`, `serve`,
`harness`, every `remote` subcommand), not just `outfit alias --list`, which
is a cost this change should not impose on the common case. A stale URL alias
simply surfaces its failure the normal way: whatever fetch it eventually
feeds (`readOutfit`'s own `outfitsrc.Fetch`) reports the real HTTP or network
error at the point it actually happens.

The same carve-out applies to `writeAliases` (`outfit alias --list` /
`outfit show`): the existing `os.Stat`-per-entry liveness probe stays
local-path-only. A URL-valued entry is printed as-is, with no "(missing)"
annotation either way — listing performs no network I/O, in keeping with this
change's overall "don't fetch until something is actually pulled" principle.

*Alternative — a HEAD request at list time*: rejected. It would make a
previously-instant, offline command (`outfit alias --list`) perform one
network round-trip per URL alias, and a slow/unreachable host would make
listing hang or fail for reasons unrelated to what the user asked.

### D6: No adjacent `.env` for a URL-sourced Outfit

`applyOutfitEnv` (`remote.go`) currently does `filepath.Join(dir, ".env")`
where `dir` is `filepath.Dir(outfitPath)`. For a URL-sourced Outfit, "the
directory the Outfit lives in" is not a concept `.env`-loading can use — there
is no local file to sit "beside." `applyOutfitEnv` gains an early check:
`outfitsrc.IsURL`-derived, it skips the `.env` read entirely (not an error —
mirrors the existing "no Outfit resolved → nothing to load" case) and
proceeds straight to applying the Outfit's own `ENV` instructions over the
process environment, per the existing precedence rule.

## Risks / Trade-offs

- **A malicious or misbehaving URL** (huge body, slow drip, redirect loop) →
  `outfitsrc.Fetch` sets a client timeout and caps the response body it reads
  (erroring past the cap rather than buffering an unbounded response), and Go's
  default `http.Client` redirect cap (10 hops) bounds a redirect loop. No
  content fetched this way is ever executed — it only ever feeds a text parser
  (`outfit.Parse`, `preset.Parse`, `json.Unmarshal`) — so the blast radius of a
  bad response is a parse error, not code execution.
- **Plain HTTP is allowed, not just HTTPS** → an Outfit fetched over `http://`
  is visible and tamperable in transit. Mitigation: this mirrors `BASEURL`,
  which already accepts a plain `http://` for a local llama.cpp server; the
  same judgment call (permit it, let the user choose HTTPS for anything that
  matters) applies here, and is worth a callout in the docs.
- **`resolveRemotePath`'s signature changes** (`outfitDir` → `outfitPath`, per
  D3) → every call site in `remote.go` already has the full Outfit path in
  scope (they compute `filepath.Dir(outfitPath)` from it today), so this is a
  mechanical, same-file change, not a new value to thread through.

## Migration Plan

Additive only. New `internal/outfitsrc` package; every changed call site
keeps its existing local-path behavior unchanged and gains a URL branch.
`config.json`'s `aliases` map needs no migration — it is already
`map[string]string`, agnostic to what kind of string it holds. No data
migration, no change to existing Outfit files, presets, `remote.json` files,
or aliases. Rollback is reverting the code; nothing persisted needs undoing.

## Open Questions

- **Response size cap value**: proposing 1 MiB, generous for a text Outfit, an
  INI preset, or a `remote.json` (all are small, hand-editable files today) —
  confirm this is not too tight for a preset someone has grown unusually large
  before implementing.
- **Fetch timeout value**: proposing 15s (longer than `internal/discovery`'s
  3s, since this is not backing interactive tab-completion and a slower
  third-party host serving a static file is plausible) — open to tightening
  once real-world fetch latency is observed.
