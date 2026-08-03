## Why

outfit configures a coding agent — a harness — to point at a model provider,
and today it knows only opencode and Pi. lucinate
([github.com/lucinate-ai/lucinate](https://github.com/lucinate-ai/lucinate)),
our sister project, is a terminal-native chat client that connects to any
OpenAI-compatible endpoint. Its users want the same one-command "point me at
this model" that outfit already gives the other harnesses, and outfit's own
harness abstraction was built so a third one is a matter of implementing the
interface and registering it.

## What Changes

- Add **lucinate** as a third harness. `outfit add`/`apply`/`remove`/`show`/
  `export`/`harness` all work with `-H lucinate`, `OUTFIT_HARNESS=lucinate`, or
  the stored default; the resolution precedence is unchanged.
- Teach outfit to read and write lucinate's config: an OpenAI-compatible
  *connection* in `~/.lucinate/connections.json` (data dir overridable with
  `LUCINATE_DATA_DIR`, **not** XDG). Applying a selection writes one managed
  connection — its `url` from the resolved base URL, its `defaultModel` from the
  selected model, its `name` from the provider — and points the store's
  top-level `defaultId` at it so lucinate launches straight into that model. The
  merge preserves every other connection and any unknown fields, mirroring the
  Pi adapter.
- Keep outfit's **no-secret-on-disk** invariant. lucinate reads an
  OpenAI-compatible key from its secrets store *or*, when none is stored, from
  the `LUCINATE_OPENAI_API_KEY` environment variable. outfit writes no secret;
  instead `outfit harness -H lucinate` injects the active provider's resolved key
  as `LUCINATE_OPENAI_API_KEY` at launch, the same runtime-injection approach the
  other harnesses use.
- Mark which catalogue providers are lucinate-capable — those that expose an
  OpenAI-compatible endpoint (openrouter and the local engines). Providers that
  authenticate through a native SDK rather than an OpenAI-style HTTP API
  (amazon-bedrock, the Vertex providers) are reported unsupported under the
  lucinate harness, just as providers without a `pi` block are under Pi.
- Document the new harness in `README.md` and `AGENTS.md`.

## Capabilities

### New Capabilities
- `lucinate-integration`: how outfit reads and writes lucinate's
  `~/.lucinate/connections.json` — the managed OpenAI-compatible connection, the
  preserving merge, the top-level default, the API-key idiom (env injection, no
  secret written), the providers that map, and how limits that lucinate cannot
  represent are handled.

### Modified Capabilities
- `harness-management`: the abstraction now covers three harnesses, not two;
  `outfit harness -H lucinate` additionally injects `LUCINATE_OPENAI_API_KEY` for
  the active provider so the launched agent can authenticate.
- `provider-catalog`: a provider entry MAY declare a `lucinate` marker
  identifying it as usable by the lucinate harness (OpenAI-compatible endpoint).

## Impact

- **New code**: `internal/lucinate` (connections.json + secrets IO, preserving
  merge, state read-back) and a `lucinateHarness` adapter in
  `internal/harness/adapters.go`, registered in the harness registry.
- **Changed code**: `cmd/outfit/main.go` launch path (inject
  `LUCINATE_OPENAI_API_KEY`); `internal/catalog` (recognise the `lucinate`
  marker and gate provider eligibility); `internal/catalog/providers.yaml` (add
  the marker to OpenAI-compatible providers).
- **Docs**: `README.md` (supported harnesses) and `AGENTS.md` (architecture,
  invariants, the lucinate config notes).
- **No breaking changes**: existing opencode/Pi behaviour, Outfit files, and
  outfit's own config are untouched; the harness stays a runtime choice that
  never appears in an Outfit.
