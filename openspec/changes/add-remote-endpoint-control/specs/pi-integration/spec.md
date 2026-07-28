## MODIFIED Requirements

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
