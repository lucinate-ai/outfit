## Why

The two harness builders in `internal/catalog` had drifted apart on option
handling. `BuildProviderBlock` applies `optionsFromEnv` and enforces
`optionsRequired`; `BuildPiProvider` did neither, reading `p.Options["baseURL"]`
straight from the catalogue.

The visible symptom was that per-provider endpoint variables did nothing under
Pi. `OLLAMA_BASE_URL`, `LLAMACPP_BASE_URL`, `OMLX_BASE_URL`, `VLLM_BASE_URL` and
`OPENAI_BASE_URL` were silently dropped, so `models.json` kept pointing at
`localhost` while the same Outfit under opencode pointed at the right server.
Only the generic `OUTFIT_BASE_URL` worked, because it arrives by a different
parameter.

The dropped URL was not the damaging part. `IsLocalEndpoint` then still saw
`localhost`, so an `apiKeyOptional` provider took the keyless branch and Pi was
handed the literal placeholder `local` to authenticate a *remote* server with —
a rejected request whose cause is nowhere near where it appears. Nothing errored
at any point.

The `optionsRequired` half is the same split, one commit younger, and currently
latent: the providers that use it (`google-vertex`, `google-vertex-anthropic`)
have no `pi` block, so `BuildPiProvider` rejects them earlier. It would become
live the moment a `pi` block met a required option — including via a runtime
catalogue supplied with `--providers`, which no integrity test covers.

## What Changes

- Extract `Provider.ResolveOptions` (static options with `optionsFromEnv`
  applied over them) and `Provider.RequireOptions` (the `optionsRequired` check),
  and call both from *both* builders, so the two paths cannot drift again.
- `BuildPiProvider` resolves its base URL as: explicit override, then the
  provider's own `optionsFromEnv` variable, then the `pi` block's `baseUrl`, then
  `options.baseURL`. The per-provider variable sits above both catalogue values
  because it is the user describing their own machine, and below the explicit
  override because that is the user being more specific.
- `BuildPiProvider` fails when a required option is unset, rather than writing an
  entry silently missing it. Pi's schema has no general options map, so it cannot
  carry one; failing is the only honest outcome.
- Add an integrity test asserting no embedded provider pairs `optionsRequired`
  with a `pi` block, which is the invariant that keeps the case above unreachable
  for the shipped catalogue.

Non-goal: no change to how opencode resolves options — that path was already
correct, and its behaviour is unchanged.

## Impact

- Affected specs: `pi-integration`
- Affected code: `internal/catalog/catalog.go`
- Behaviour change: a Pi user who set a per-provider endpoint variable was
  previously served a `localhost` entry; they will now get the endpoint they
  asked for. Anyone who worked around the bug by *also* setting
  `OUTFIT_BASE_URL` is unaffected, since the explicit override still wins.
