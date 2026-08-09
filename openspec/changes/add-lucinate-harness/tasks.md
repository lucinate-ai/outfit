## 1. Provider catalogue

- [x] 1.1 Add a `lucinate` marker to the OpenAI-compatible providers in
  `internal/catalog/providers.yaml` (openrouter, ollama, llamacpp, vllm, omlx,
  openai-compatible); leave amazon-bedrock and the Vertex providers unmarked.
- [x] 1.2 Document the `lucinate` marker in the `providers.yaml` header comment,
  next to the `pi` block docs.
- [x] 1.3 Parse the marker into the catalogue types in `internal/catalog` and
  expose whether a provider is lucinate-capable (mirroring the `pi` block).
- [x] 1.4 Add `catalog` unit tests: a marked provider is lucinate-capable, an
  unmarked one is not.

## 2. lucinate config IO (`internal/lucinate`)

- [x] 2.1 Create `internal/lucinate` with data-dir resolution matching lucinate
  (`$LUCINATE_DATA_DIR`, else `~/.lucinate`; not XDG) and the `connections.json`
  path helper.
- [x] 2.2 Model the store — `{defaultId, connections:[…]}` and the `Connection`
  fields — with a catch-all so unknown store-level and connection-level fields
  round-trip untouched.
- [x] 2.3 Implement the preserving merge: insert/update one managed `openai`
  connection keyed by a deterministic id (`outfit:<providerId>`), preserving its
  `createdAt` on update and all sibling connections and unknown fields; set
  `defaultId` to the managed connection. Write `0600` atomically.
- [x] 2.4 Implement `Remove(providerID)`: delete the managed connection, clear
  `defaultId` when it named it, return the removal count.
- [x] 2.5 Implement `State`: read the store back, reporting each managed
  connection as a provider (model from `defaultModel`, base URL from `url`, no
  context/output limits, no top-level default model). Recover the provider id
  from the connection id namespace.
- [x] 2.6 Unit tests for `internal/lucinate`: first-write creates the file
  `0600`; sibling connections and unknown fields survive; re-apply updates in
  place with no duplicate; `defaultId` is set on apply and cleared on removing
  the default; `$LUCINATE_DATA_DIR` override is honoured.

## 3. Harness adapter

- [x] 3.1 Add `lucinateHarness` to `internal/harness/adapters.go` implementing
  `Name` ("lucinate") / `Command` ("lucinate") / `ConfigPath` / `Apply` /
  `Remove` / `State`, wrapping `catalog` + `internal/lucinate`.
- [x] 3.2 In `Apply`: reject a provider that is not lucinate-capable with a clear
  "unsupported by the lucinate harness" error; require a resolvable base URL
  (error, write nothing, when absent); build the managed connection; write no
  secret; return `Summary` with `DefaultModel` set to the model and notes about
  the `LUCINATE_OPENAI_API_KEY` idiom (and the local-endpoint / missing-key
  cases, reusing `missingKeyWarning`).
- [x] 3.3 Register `"lucinate"` in the harness `registry` in
  `internal/harness/harness.go`; confirm `Names`, `Lookup`, `Resolve`, and
  `SavePreference` pick it up and the default stays `opencode`.
- [x] 3.4 Harness-level tests: lucinate resolves via flag/env/stored preference;
  a non-capable provider errors; Apply/State/Remove round-trip a selection.

## 4. Launch-time key injection

- [x] 4.1 On the launch path (`cmd/outfit/main.go`, around `harnessEnv`), when the
  resolved harness is lucinate, inject `LUCINATE_OPENAI_API_KEY` set to the active
  provider's resolved key into the launched agent's environment only; inject
  nothing when no key resolves.
- [x] 4.2 Tests: launching lucinate for a resolvable-key provider sets
  `LUCINATE_OPENAI_API_KEY` in the child env; an unresolvable key injects nothing
  and the launch still proceeds; outfit's own process environment is not mutated.

## 5. CLI surface

- [x] 5.1 Verify `add` / `apply` / `remove` / `unapply` / `show` / `export` route
  through the lucinate adapter with `-H lucinate` and print sensible output
  (config path, chosen model, key note).
- [x] 5.2 CLI tests covering a lucinate-routed `add`, `show`, `export`, and
  `remove` (mirroring the Pi coverage in `harness_test.go`).

## 6. Docs

- [x] 6.1 Update `README.md`: list lucinate among supported harnesses.
- [x] 6.2 Update `AGENTS.md`: the harness overview (three harnesses), the
  `internal/lucinate` package entry, the lucinate config notes (connections.json,
  data dir, the no-secret / `LUCINATE_OPENAI_API_KEY` idiom, limits not
  represented), and the `lucinate` marker under the catalogue notes.
- [x] 6.3 Do not touch `CHANGELOG.md` (handled by the release process).

## 7. Verification

- [x] 7.1 `gofmt` and `go test ./... -cover`; keep coverage >= 80%.
- [x] 7.2 Manually exercise: `outfit add -H lucinate -p openrouter -m <model>`
  writes a managed connection to `connections.json` with `defaultId` set and no
  secret on disk; `outfit harness -H lucinate` launches lucinate into that model
  with the key injected; `outfit export -H lucinate` reconstructs the selection.
- [x] 7.3 Run `openspec validate --change add-lucinate-harness` and resolve any
  findings.
