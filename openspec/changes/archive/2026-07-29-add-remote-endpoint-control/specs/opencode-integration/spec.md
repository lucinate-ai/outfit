## MODIFIED Requirements

### Requirement: Secrets and file permissions

The API key SHALL be written to the provider block's options as an
environment-variable reference in opencode's `{env:VAR}` form, naming the
provider's key variable — never the resolved secret, so no secret is written to
disk. The reference SHALL be written even when the variable is currently unset,
because opencode substitutes it when it reads the config, so the key may be set
after the Outfit is applied. The option SHALL be omitted entirely when the
provider declares no key variable, or when its key variable is declared
optional and the provider's endpoint is a local address — a local server needs
no key, and naming a variable nobody will set would only mislead.

The config SHALL still be written with owner-only (`0600`) permissions —
enforced even when the file already existed with looser ones — since it names
the user's providers and endpoints.

#### Scenario: The key is referenced, not embedded

- **WHEN** a provider with an API key variable is applied
- **THEN** the config's `apiKey` is that variable's `{env:VAR}` reference, and
  the resolved secret appears nowhere in the file

#### Scenario: A key set after applying still works

- **WHEN** an Outfit is applied with the key variable unset, and the variable is
  set before opencode runs
- **THEN** opencode resolves the reference to that value

#### Scenario: A local keyless server gets no key option

- **WHEN** a provider whose key is optional is applied against a local base URL
  with its key variable unset
- **THEN** the provider block carries no `apiKey` option

#### Scenario: Permissions enforced on existing file

- **WHEN** the config file exists with permissive mode and `outfit add` writes
  it
- **THEN** the file's mode is owner-only afterwards
