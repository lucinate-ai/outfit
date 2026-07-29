## Why

With the static model lists removed from the catalogue (see the
`retire-model-families` change), `outfit list` no longer suggests any models — the
catalogue is provider plumbing only. But most of these providers already expose their
*own* authoritative, always-current model list over HTTP. Querying that on demand gives
users a browsable, self-updating set of models to pick from with **zero curation** — the
right fix for "models change all the time", and a far better source than a general weights
hub like Hugging Face, which does not know what a given provider actually serves.

## What Changes

- Add an optional live model-discovery layer that, for a selected provider, fetches the
  models the provider currently serves from the provider's own endpoint:
  - OpenRouter: `GET {baseURL}/models` (`https://openrouter.ai/api/v1/models`)
  - Generic OpenAI-compatible / vLLM / llama.cpp: `GET {baseURL}/models`
  - Ollama: `GET {baseURL}/api/tags`
  - (Amazon Bedrock `ListFoundationModels` is out of scope for this change.)
- Surface discovered models through `outfit list --models <provider>` (and enrich the
  per-provider block of `outfit list` when a discovery source is available).
- Source shell model completion (`outfit __complete … --model <TAB>`) from discovery when
  the provider supports it, bounded and cached so completion stays instant and quiet.
- Degrade gracefully: discovery is best-effort. A network failure, timeout, missing key,
  or unparseable response yields "no models" — never an error, and `outfit list` still
  prints the provider's plumbing. Results are cached for a short TTL so repeated calls in
  one session do not re-hit the network.
- Never write secrets: discovery reuses the same resolved API key the provider already
  uses, read from the environment/`.env`; it is only sent as a request header, never
  persisted.

## Capabilities

### New Capabilities

- `model-discovery`: fetching a provider's currently-served models from the provider's own
  HTTP endpoint (per-provider protocol, auth, caching, and graceful-failure rules) and
  surfacing them in `outfit list` and shell completion.

### Modified Capabilities

_None._ Discovery is additive: it introduces a new capability rather than changing the
`provider-catalog`, `outfit-files`, or `shell-completion` requirements. It builds on the
`retire-model-families` change (which removes the static lists it replaces) and should
land after it.

## Impact

- Code: a new `internal/discovery` package (HTTP client, per-provider protocol adapters,
  in-memory TTL cache), reusing the client pattern in `internal/remote/remote.go`. Wiring
  in `cmd/outfit/main.go` (`cmdList` + a `--models` flag) and `cmd/outfit/complete.go`
  (model candidates).
- Catalogue: providers gain a small hint of which discovery protocol applies. The existing
  `pi.api` already distinguishes `openai-completions` vs others; discovery can key off the
  provider id / base URL, or an explicit `discovery:` field may be added to
  `providers.yaml` if the mapping is not derivable. Decide in design.
- Dependencies: standard library `net/http` only; no new module dependency.
- Tests: unit tests for each protocol adapter against recorded fixtures, cache behaviour,
  and offline/error fallback; a `--models` command test using a stub server.
- Docs: `docs/commands/list.md` (the `--models` flag), and a note in `docs/outfit-file.md`
  pointing users at `outfit list --models` to discover model ids.
