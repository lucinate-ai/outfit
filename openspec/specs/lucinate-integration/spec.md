# Lucinate Integration Specification

## Purpose

How outfit reads and writes lucinate's connections store: the one managed
OpenAI-compatible connection it owns, the merge that leaves every sibling
connection and unknown field untouched, and the top-level default that makes
lucinate boot straight into the selected model.

The part worth knowing is the API key. A lucinate connection has somewhere to
put one, and outfit deliberately does not — no secret is written, to the store
or to lucinate's secrets store. lucinate falls back to `LUCINATE_OPENAI_API_KEY`
when its stored secret is empty, and `outfit harness` supplies it at launch,
which is the same runtime-injection idiom the other harnesses use. This also
covers which providers map (OpenAI-compatible ones only) and what happens to
the limits a connection cannot represent.

## Requirements

### Requirement: Config location and shape

The lucinate adapter SHALL read and write lucinate's connections store at
`connections.json` under lucinate's data directory — `$LUCINATE_DATA_DIR` when
set, otherwise `~/.lucinate` (resolved from the home directory, **not** XDG). The
store is plain JSON of the form
`{"defaultId": "<id>", "connections": [{id, name, type, url, defaultModel, …}]}`.
A missing file SHALL be treated as an empty store, and the directory SHALL be
created when needed. The file SHALL be written with owner-only (`0600`)
permissions.

#### Scenario: First write creates the store

- **WHEN** the user runs `outfit add -H lucinate` and no `connections.json`
  exists
- **THEN** the file is created with the managed connection and owner-only
  permissions

#### Scenario: The data directory is overridden

- **WHEN** `LUCINATE_DATA_DIR` is set and the lucinate harness writes its config
- **THEN** the store is written under that directory, not under `~/.lucinate`

### Requirement: Managed connection

Applying a selection SHALL write exactly one managed connection of type
`openai` (OpenAI-compatible). Its `url` SHALL be the resolved base URL, its
`defaultModel` SHALL be the selection's model key (the `ALIAS` when given,
otherwise the provider-native `MODEL`), and its `name` SHALL be the provider's
display name — or the selection's display name when one is set, so a remote
endpoint is told apart from a local engine of the same kind. The connection
SHALL carry a deterministic id derived from the provider so that re-applying the
same provider updates that one connection rather than creating a duplicate.

Because a lucinate OpenAI-compatible connection needs a concrete endpoint, a
selection whose base URL does not resolve SHALL fail with an error, and no
config SHALL be written.

#### Scenario: A selection becomes one openai connection

- **WHEN** an OpenRouter selection is applied to lucinate
- **THEN** the store gains one `openai` connection whose `url` is the resolved
  base URL and whose `defaultModel` is the selected model

#### Scenario: Re-applying updates the same connection

- **WHEN** the same provider is applied a second time with a different model
- **THEN** the managed connection's `defaultModel` is updated and no second
  connection for that provider appears

#### Scenario: A remote selection keeps its display name

- **WHEN** a selection carrying a display name (a remote endpoint) is applied
- **THEN** the managed connection's `name` is that display name

#### Scenario: A selection with no resolvable base URL fails

- **WHEN** a selection is applied whose base URL cannot be resolved
- **THEN** the command fails with an error and no connection is written

### Requirement: Preserving merge

Writes SHALL merge only the managed connection: other connections in the store,
the ordering of unrelated entries, and any unknown fields — on the store or on
the managed connection — SHALL round-trip untouched. When the managed connection
already exists, its creation timestamp SHALL be preserved and only the fields
outfit owns (`type`, `url`, `defaultModel`, `name`) SHALL be overwritten.

#### Scenario: Sibling connections survive

- **WHEN** the store already holds another connection and the user runs
  `outfit add -H lucinate`
- **THEN** that connection is intact afterwards

#### Scenario: Unknown fields round-trip

- **WHEN** the managed connection already carries fields outfit does not own
  (for example a last-used timestamp or a future field)
- **THEN** those fields are preserved after a re-apply

### Requirement: Default connection selection

Applying a selection SHALL set the store's top-level `defaultId` to the managed
connection, so that lucinate auto-selects that connection — and therefore its
model — at startup. This is lucinate's equivalent of a default model: the
connection's `defaultModel` names the model, and `defaultId` makes it the one
launched into.

#### Scenario: Applying makes the connection the default

- **WHEN** a selection is applied to lucinate
- **THEN** the store's `defaultId` names the managed connection

### Requirement: API key idiom

The adapter SHALL NOT write the resolved API key to disk — neither into
`connections.json` nor into lucinate's secrets store. lucinate reads an
OpenAI-compatible key from its secrets store or, when none is stored, from the
`LUCINATE_OPENAI_API_KEY` environment variable; outfit relies on the latter, and
`outfit harness -H lucinate` injects the active provider's resolved key as
`LUCINATE_OPENAI_API_KEY` at launch. Applying a keyed provider SHALL report to
the user that the key is read from `LUCINATE_OPENAI_API_KEY` when lucinate runs
and is never written to the config.

When the provider's endpoint is a local address that needs no key, the adapter
SHALL note that no key is required rather than warn about a missing one. When a
key is required but the provider's key variable resolves to nothing and the
endpoint is not local, the adapter SHALL warn that requests will likely be
rejected until the variable is set.

#### Scenario: No secret is written for a keyed provider

- **WHEN** a provider with an API key is applied to lucinate
- **THEN** neither `connections.json` nor the secrets store contains the key
  value, and the user is told it is read from `LUCINATE_OPENAI_API_KEY` at launch

#### Scenario: A local endpoint needs no key

- **WHEN** a keyless local provider (Ollama or llama.cpp) is applied to lucinate
- **THEN** the connection is written and the user is not warned about a missing
  key

### Requirement: Provider eligibility

Only providers marked lucinate-capable — those exposing an OpenAI-compatible
endpoint — SHALL be applicable under the lucinate harness. Applying a provider
that authenticates through a native SDK rather than an OpenAI-style HTTP API
(such as amazon-bedrock or the Vertex providers) SHALL fail with an error naming
the provider as unsupported by the lucinate harness, and no config SHALL be
written.

#### Scenario: An OpenAI-compatible provider is accepted

- **WHEN** an OpenRouter or Ollama selection is applied to lucinate
- **THEN** the connection is written successfully

#### Scenario: A non-OpenAI provider is rejected

- **WHEN** an amazon-bedrock selection is applied to lucinate
- **THEN** the command fails reporting the provider unsupported by lucinate, and
  no config is written

### Requirement: Removing a connection

Removing a provider SHALL delete the managed connection outfit created for it.
When the removed connection was the store's `defaultId`, that field SHALL be
cleared so lucinate falls back to its own startup selection. Other connections
SHALL be untouched. The operation SHALL report how many entries it removed.

#### Scenario: Remove deletes the managed connection

- **WHEN** the user runs `outfit remove -H lucinate` for a provider previously
  applied
- **THEN** the managed connection is gone and other connections remain

#### Scenario: Removing the default clears the pointer

- **WHEN** the removed connection was named by `defaultId`
- **THEN** `defaultId` is cleared afterwards

### Requirement: Reading state back

The adapter SHALL read the store back for `outfit show` and `outfit export`,
reporting each managed connection as a configured provider: its model key (from
`defaultModel`) and its base URL (from `url`). Because a lucinate connection has
no fields for context or output limits, the adapter SHALL report none, and those
limits SHALL NOT round-trip through `export`. lucinate has no single top-level
default *model* setting distinct from the connection, so the read-back SHALL
report no top-level default model.

#### Scenario: Export reconstructs provider and model

- **WHEN** `outfit export -H lucinate` runs against a store with a managed
  connection
- **THEN** the reconstructed Outfit names that provider, its model, and its base
  URL

#### Scenario: Limits do not round-trip

- **WHEN** a selection with a context window is applied to lucinate and then
  exported
- **THEN** the exported Outfit carries no context or output limit, because the
  connection cannot hold them
