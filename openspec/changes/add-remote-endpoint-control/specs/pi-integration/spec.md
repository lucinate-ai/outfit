## MODIFIED Requirements

### Requirement: API key idiom

The managed provider's `apiKey` SHALL be written as a `$ENV_VAR` interpolation
referencing the provider's key variable — never the resolved secret. The
reference SHALL be written even when that variable is currently unset, because
Pi resolves it when it runs, so the key may be exported after the Outfit is
applied.

A dummy literal placeholder SHALL be written instead when the provider has no
key variable at all, or when its key variable is declared optional and resolves
to nothing — because Pi hides a provider's models from `/model` until some auth
is configured, and a reference to a variable set nowhere would leave them
hidden. Local servers ignore the value.

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

#### Scenario: A required key survives being unset at apply time

- **WHEN** a provider whose key is not optional is applied to Pi with its key
  variable unset
- **THEN** the entry's `apiKey` is still the `$ENV_VAR` reference, so exporting
  the key later is enough
