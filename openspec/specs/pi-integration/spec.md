# Pi Integration Specification

## Purpose

Define how `outfit` reads and writes the Pi coding agent's model catalogue at
`~/.pi/agent/models.json`: the merge that preserves everything else in the
file, Pi's API-key idiom, which providers are Pi-capable, and the differences
from opencode (no default-model setting).

## Requirements

### Requirement: Config location and shape

The Pi adapter SHALL write `~/.pi/agent/models.json` — resolved from the home
directory, not XDG — as plain JSON of the form
`{"providers": {"<id>": {baseUrl, api, apiKey, models: [...]}}}`. A missing
file SHALL be treated as an empty document, and the directory SHALL be created
when needed. The file SHALL be written with owner-only (`0600`) permissions.

#### Scenario: First write creates the file

- **WHEN** the user runs `outfit add -H pi` and no `models.json` exists
- **THEN** the file is created with the managed provider entry and owner-only
  permissions

### Requirement: Preserving merge

Writes SHALL merge only the managed provider entry: unknown top-level keys,
sibling providers, and unknown fields on the managed provider (headers,
compat, model overrides, …) SHALL all round-trip untouched. The provider's
scalar fields (`baseUrl`, `api`, `apiKey`) are overwritten when set; the
`models` arrays are unioned by `id` with incoming entries winning, and written
sorted by id so the file is deterministic.

#### Scenario: Sibling provider and unknown fields survive

- **WHEN** `models.json` holds another provider and extra fields on the managed
  one, and the user re-runs `outfit add -H pi`
- **THEN** the sibling provider and the extra fields are intact afterwards

#### Scenario: Models unioned by id

- **WHEN** the managed provider already lists a model the selection also names
- **THEN** the incoming entry replaces it and no duplicate id appears

### Requirement: API key idiom

The managed provider's `apiKey` SHALL be written as a `$ENV_VAR` interpolation
referencing the provider's key variable — never the resolved secret. The
reference SHALL be written even when that variable is currently unset, because
Pi resolves it when it runs, so the key may be exported after the Outfit is
applied.

A dummy literal placeholder SHALL be written instead when the provider has no
key variable at all, or when its key variable is declared optional, resolves to
nothing, AND the provider's endpoint is a local address — because Pi hides a
provider's models from `/model` until some auth is configured, and for a local
server, which ignores the value, a reference to a variable set nowhere would
leave them hidden. The placeholder SHALL NOT be written for a non-local
endpoint, since Pi resolves the reference when it runs and a placeholder could
not then be repaired by setting the variable.

#### Scenario: Keyed provider references the variable

- **WHEN** an OpenRouter selection is applied to Pi
- **THEN** the entry's `apiKey` is the literal string `$DEEPSEEK_API_KEY`-style
  reference, not the key's value

#### Scenario: Keyless provider gets a placeholder

- **WHEN** an Ollama or llama.cpp selection is applied to Pi
- **THEN** the entry's `apiKey` is a dummy literal so the models are selectable
  in `/model`

#### Scenario: An optional key that is set is referenced

- **WHEN** a provider whose key is optional is applied to Pi with its key
  variable set
- **THEN** the entry's `apiKey` is the `$ENV_VAR` reference, so the remote
  endpoint is authenticated

#### Scenario: An optional key at a remote endpoint keeps its reference

- **WHEN** a provider whose key is optional is applied to Pi against a non-local
  base URL, with its key variable unset
- **THEN** the entry's `apiKey` is the `$ENV_VAR` reference, so setting the
  variable before running Pi is enough

#### Scenario: A required key survives being unset at apply time

- **WHEN** a provider whose key is not optional is applied to Pi with its key
  variable unset
- **THEN** the entry's `apiKey` is still the `$ENV_VAR` reference, so exporting
  the key later is enough

### Requirement: Pi capability gating

A provider SHALL be usable with the Pi harness only when the catalogue gives it
a `pi` block (declaring the protocol `api` and optionally a Pi-specific base
URL). Selecting an unsupported provider under Pi SHALL fail saying the provider
is not supported by the pi harness. The Pi base URL SHALL resolve as: explicit
override, then the provider's own `optionsFromEnv` endpoint variable, then the
`pi` block's `baseUrl`, then the provider's `options.baseURL`.

#### Scenario: Bedrock is opencode-only

- **WHEN** the user runs `outfit add -p amazon-bedrock -f claude -H pi`
- **THEN** the command fails explaining the provider has no Pi support

### Requirement: No default model on Pi

Pi has no default-model setting, so applying a selection SHALL NOT record one;
instead the command SHALL tell the user which model to select with `/model`
inside Pi. `outfit export` for Pi SHALL rely on the provider selection alone.

#### Scenario: Add tells the user what to pick

- **WHEN** a selection with a chosen model is applied to Pi
- **THEN** the output notes that Pi has no default-model setting and names the
  model to pick with `/model`

### Requirement: Per-model limits

When the selection carries a context window and output limit, they SHALL be
written on every model the selection adds, as `contextWindow` and `maxTokens`.

#### Scenario: Limits land on the Pi models

- **WHEN** `outfit add -H pi -p llamacpp -m my-model -c 128k` is applied
- **THEN** the written model has `contextWindow` 128000 and `maxTokens` 32000
