## Context

outfit's harness abstraction (`internal/harness`) already anticipates a third
harness: `AGENTS.md` says "Start here when adding a third harness: implement the
interface and register it." The interface is `Name / Command / ConfigPath /
Apply / Remove / State`, and adapters wrap `catalog` + a per-harness config-IO
package + `contextsize` (`internal/harness/adapters.go`). opencode reads a JSONC
config with `{env:VAR}` key references; Pi reads `~/.pi/agent/models.json` with
`$VAR` references and a keyless placeholder.

lucinate (github.com/lucinate-ai/lucinate) is a terminal-native chat client. Its
relevant on-disk state (read directly from the repo):

- **Data dir**: `$LUCINATE_DATA_DIR`, else `~/.lucinate` — resolved from the home
  directory, not XDG (`internal/config/datadir.go`).
- **`connections.json`**: `{"defaultId": "<id>", "connections": [Connection…]}`,
  written `0600` atomically. A `Connection` is
  `{id, name, type, url, createdAt, lastUsed?, defaultModel?}`. `type` is one of
  `openclaw` / `openai` / `hermes`. `defaultModel` is OpenAI-compat only. At
  startup lucinate auto-selects the connection named by `defaultId`
  (`internal/config/startup.go`).
- **Secrets**: `~/.lucinate/secrets/secrets.json` (`0600`),
  `{"apiKeys": {"<connID>": "<key>"}}`. For an `openai` connection lucinate reads
  the key from that store **or**, when the stored key is empty, from the
  `LUCINATE_OPENAI_API_KEY` environment variable (`app/factory.go`).

So the natural mapping is: an outfit provider selection → one OpenAI-compatible
lucinate connection. This is the same shape as the Pi adapter (one managed entry
merged into a document full of user entries), so the Pi adapter is the template.

## Goals / Non-Goals

**Goals:**
- Add lucinate as a third harness behind the existing interface, with no change
  to opencode/Pi behaviour and no new keyword in the Outfit format.
- Support `add` / `apply` / `remove` / `show` / `export` / `harness` for
  `-H lucinate`.
- Preserve outfit's invariants: preserving merge that never drops user
  connections or unknown fields; `0600` output; **no resolved secret written to
  disk**.
- Keep providers-as-data: eligibility is a marker in `providers.yaml`, not Go.

**Non-Goals:**
- Configuring lucinate's OpenClaw or Hermes connection types. outfit selects a
  *model provider*; those are gateway backends, out of scope.
- Round-tripping context/output limits through lucinate — its connection has no
  fields for them.
- Writing to lucinate's secrets store, or backing it with an OS keychain.

## Decisions

### D1: Map a selection to one managed `openai` connection

Apply writes a single `openai`-type connection: `url` = resolved base URL,
`defaultModel` = model key (`ALIAS` when given, else `MODEL` — the shared
`modelKey` helper), `name` = provider display name (or `Selection.DisplayName`
for a remote endpoint, matching the opencode adapter). Context/output limits are
dropped — lucinate has no fields for them.

*Alternative — the env-only ephemeral connection* (`LUCINATE_OPENAI_BASE_URL` +
`LUCINATE_OPENAI_DEFAULT_MODEL` create a connection at startup with no file
write): rejected because it leaves `add` / `show` / `export` / `remove` with
nothing to read or write, breaking the `Apply` / `State` / `Remove` contract.
The env vars are used only for key injection at launch, not as the config
mechanism.

### D2: Deterministic connection id keyed by provider

The managed connection's `id` is derived deterministically from the provider id
(e.g. `outfit:<providerId>`) rather than a random hex string. This lets a
re-apply update the same connection instead of duplicating it, lets `Remove`
target it, and lets `State` recover the provider id when reconstructing an
Outfit. It parallels how opencode/Pi key their managed entry by provider id.

*Alternative — match by natural key (`type`, `url`)*: rejected because two
`openai-compatible` selections can share a URL, and the id is the stable handle
outfit already needs for round-tripping.

### D3: Set `defaultId` so lucinate launches into the model

Apply points the store's top-level `defaultId` at the managed connection. This
is lucinate's equivalent of a default model: `defaultModel` names the model and
`defaultId` makes lucinate boot into it — the closest analog to opencode's
top-level `model`. `Remove` clears `defaultId` when it pointed at the removed
connection, so lucinate falls back to its own picker. All other connections and
the rest of the document are preserved by the merge (D4).

### D4: Preserving merge, like Pi

Read the whole store into a structure that round-trips unknown fields (a struct
with an `extra`/`json.RawMessage` catch-all, or a generic map as Pi does), merge
only the managed connection (preserving its `createdAt` when it already exists),
and write back `0600` atomically. Sibling connections, entry ordering, and
unknown fields survive. This is the core invariant carried over from the Pi
adapter.

### D5: No secret on disk — inject `LUCINATE_OPENAI_API_KEY` at launch

outfit writes no key into `connections.json` and nothing into
`secrets/secrets.json`. Instead it relies on lucinate's env fallback: at launch,
`outfit harness -H lucinate` sets `LUCINATE_OPENAI_API_KEY` to the active
provider's resolved key, alongside the provider key variables `harnessEnv`
already forwards. Apply prints a note that the key is read from that variable at
run time (or that a local endpoint needs none). This keeps outfit's
"never write the resolved secret to disk" invariant intact — the analog of
opencode's `{env:VAR}` and Pi's `$VAR`, whose runtime-resolution outfit already
mirrors by injecting keys into the launched child.

The injection is a small addition on the launch path (`cmd/outfit/main.go`,
around `harnessEnv`): when the resolved harness is lucinate, also export
`LUCINATE_OPENAI_API_KEY` for the worn/active provider's key. A bare launch with
no provider resolvable simply injects nothing and lucinate uses its own auth.

*Alternative — write the literal key into `secrets.json`*: rejected. It is
lucinate's native mechanism but breaks outfit's central no-secret-on-disk
invariant; the env fallback exists precisely for this.

### D6: Eligibility via a `lucinate` marker in `providers.yaml`

Add a `lucinate` marker to the OpenAI-compatible providers (openrouter and the
local engines — ollama, llamacpp, vllm, omlx, openai-compatible). Providers that
authenticate through a native SDK (amazon-bedrock, google-vertex,
google-vertex-anthropic) omit it and are rejected under the lucinate harness with
a clear error, exactly as providers without a `pi` block are under Pi. The base
URL and key variable come from the provider's existing fields and the standard
base-URL precedence, so the marker only needs to signal capability (no new
builder as complex as `BuildPiProvider` is required, though a small
`BuildLucinateConnection` keeps the mapping in `catalog`).

## Risks / Trade-offs

- **Single `LUCINATE_OPENAI_API_KEY` for possibly many connections** → outfit
  sets `defaultId` to the managed connection and injects that provider's key, so
  the connection lucinate boots into is authenticated. Other managed connections
  would need their own stored key; documented as a limitation. Mitigation: the
  common flow is one active model at a time, which is exactly what outfit points
  at.
- **Limits don't round-trip** → a `CONTEXT`/`OUTPUT` applied under lucinate is
  silently absent from `export`. Mitigation: the spec states this explicitly and
  `State` reports no limits, so behaviour is predictable, not lossy-by-surprise.
- **lucinate's on-disk shape could change** → the adapter is pinned to today's
  `connections.json`/secrets shape. Mitigation: the preserving merge tolerates
  unknown fields, and the shape is covered by the adapter's tests so a drift
  surfaces there.
- **Keychain-backed secrets (future in lucinate)** → if lucinate moves secrets to
  the OS keychain, the env fallback still works, so outfit is unaffected.

## Migration Plan

Additive only. New `internal/lucinate` package and adapter, one registry entry,
a launch-env addition, and a `providers.yaml` marker. No data migration, no
change to existing configs, Outfit files, or opencode/Pi paths. Rollback is
removing the registry entry (the harness simply stops being selectable).

## Open Questions

- **Exact marker shape**: a bare `lucinate: true`, or a small `lucinate:` block
  (mirroring `pi:`) able to carry a base-URL override? Default to the smallest
  thing that works (presence = capable), reusing the standard base-URL
  resolution; promote to a block only if a provider needs a lucinate-specific
  endpoint.
- **Connection `name` collisions**: if a user already has a hand-made connection
  with the same display name, outfit still keys by id (D2), so they coexist;
  confirm the id namespace (`outfit:`) can't collide with lucinate's hex ids
  (it can't — hex ids contain no colon).
