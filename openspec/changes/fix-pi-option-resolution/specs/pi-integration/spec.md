## MODIFIED Requirements

### Requirement: Pi capability gating

A provider SHALL be usable with the Pi harness only when the catalogue gives it
a `pi` block (declaring the protocol `api` and optionally a Pi-specific base
URL). Selecting an unsupported provider under Pi SHALL fail saying the provider
is not supported by the pi harness.

The Pi base URL SHALL resolve as: explicit override, then the provider's own
`optionsFromEnv` endpoint variable, then the `pi` block's `baseUrl`, then the
provider's `options.baseURL`. A per-provider endpoint variable SHALL therefore
apply to both harnesses, not only opencode: it states where the user's server
is, which is not a property of the config format being written.

Because the resolved endpoint decides whether a keyless local server is being
addressed, dropping that variable would also mis-classify a remote endpoint as
local and write the keyless placeholder in place of the key reference.

#### Scenario: Bedrock is opencode-only

- **WHEN** the user selects a provider with no `pi` block under Pi
- **THEN** the command fails explaining the provider has no Pi support

#### Scenario: Per-provider endpoint variable reaches Pi

- **WHEN** a provider's endpoint variable names a remote host and the selection
  is applied under Pi
- **THEN** the written entry carries that host, and a provider with a key
  variable carries the key reference rather than the keyless placeholder

#### Scenario: An explicit override still wins

- **WHEN** both the generic base-URL override and the provider's own endpoint
  variable are set
- **THEN** the override is written

## ADDED Requirements

### Requirement: Required options under Pi

A Pi provider entry carries only a base URL, a protocol, a key, and models, so a
provider declaring required options has nowhere to put them. Building a Pi
provider whose required options do not resolve SHALL fail, naming the variable
to set, rather than writing an entry silently missing them.

The embedded catalogue SHALL NOT pair required options with a `pi` block, since
such a provider cannot be served by Pi at all; the runtime check exists for
catalogues supplied at run time, which no integrity test can inspect.

#### Scenario: A required option is unset

- **WHEN** a Pi-capable provider declares a required option and nothing supplies
  it
- **THEN** the command fails naming the environment variable that would satisfy it
