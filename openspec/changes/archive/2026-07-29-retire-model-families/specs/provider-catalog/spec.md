## MODIFIED Requirements

### Requirement: Catalogue listing

`outfit list` SHALL print every provider in the catalogue in stable
(alphabetical) order, showing for each: its id and description, its API key
environment variable (marked `(required)` when the key is mandatory), and the
harnesses that support it (`opencode`, plus `pi` when the provider has a `pi`
block).

#### Scenario: Listing the built-in catalogue

- **WHEN** the user runs `outfit list`
- **THEN** every embedded provider is printed with its key requirements and
  supported harnesses

## REMOVED Requirements

### Requirement: Embedded provider catalogue

**Reason**: This requirement declared named model families and a per-family
default model, and carried the invariant that a family's `defaultModel` is one
of its `models`. Families are removed from the catalogue, so that invariant no
longer holds and the scenario asserting it must go. The family-free
provider-plumbing rules are restated by the "Embedded provider definitions"
requirement added below.

**Migration**: The catalogue no longer declares families or models; the user
names the model directly in the selection (`MODEL`/`ALIAS`), which already passes
through the block builders.

## ADDED Requirements

### Requirement: Embedded provider definitions

The system SHALL ship with a built-in catalogue of providers, defined in a YAML
file (`providers.yaml`) embedded into the binary at build time. Each provider
entry declares a description and MAY declare a display name, an npm package, an
API key environment variable (with optional required flag, optional flag, and
expected key prefix), static options (such as `baseURL`), options resolved from
environment variables, and a `pi` block marking the provider as usable by the Pi
harness. The catalogue SHALL NOT enumerate models: the model a provider serves is
named by the user's selection, not stored in the catalogue.

A provider whose API key is declared optional is one that also works
unauthenticated — the same engine run as a local server and as an
authenticated remote endpoint — so an unset key variable SHALL mean "no key",
not "a key that is missing".

#### Scenario: Catalogue loads without external files

- **WHEN** any command that needs the catalogue runs with no `--providers` flag
  and no `OUTFIT_PROVIDERS` environment variable
- **THEN** the embedded catalogue is used, with no file read from disk

#### Scenario: An optional key is injected only when set

- **WHEN** a provider whose API key is optional is applied, with its key
  variable set
- **THEN** the key is injected into the harness config

#### Scenario: An optional key that is unset is not an error

- **WHEN** the same provider is applied with the key variable unset
- **THEN** the configuration is written with no key, and the command succeeds
