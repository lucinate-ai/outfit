## MODIFIED Requirements

### Requirement: Embedded provider catalogue

The system SHALL ship with a built-in catalogue of providers and model
families, defined in a YAML file (`providers.yaml`) embedded into the binary at
build time. Each provider entry declares a description and MAY declare a
display name, an npm package, an API key environment variable (with optional
required flag, optional flag, and expected key prefix), static options (such as
`baseURL`), options resolved from environment variables, a `pi` block marking
the provider as usable by the Pi harness, and named model families. Each family
declares a description, a default model, and a set of models.

A provider whose API key is declared optional is one that also works
unauthenticated — the same engine run as a local server and as an
authenticated remote endpoint — so an unset key variable SHALL mean "no key",
not "a key that is missing".

#### Scenario: Catalogue loads without external files

- **WHEN** any command that needs the catalogue runs with no `--providers` flag
  and no `OUTFIT_PROVIDERS` environment variable
- **THEN** the embedded catalogue is used, with no file read from disk

#### Scenario: Family default model is always a member of the family

- **WHEN** the catalogue defines a model family
- **THEN** that family's `defaultModel` is one of the family's `models` keys

#### Scenario: An optional key is injected only when set

- **WHEN** a provider whose API key is optional is applied, with its key
  variable set
- **THEN** the key is injected into the harness config

#### Scenario: An optional key that is unset is not an error

- **WHEN** the same provider is applied with the key variable unset
- **THEN** the configuration is written with no key, and the command succeeds
