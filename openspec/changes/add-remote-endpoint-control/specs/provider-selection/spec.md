## MODIFIED Requirements

### Requirement: API key resolution

When a provider declares an API key environment variable, the system SHALL
resolve its value from a `.env` file **beside the Outfit being applied** first,
then from the process environment. When no Outfit is involved — a selection made
entirely from flags — the working directory SHALL be used in its place, so a
project's own `.env` is still found. A missing key SHALL be an error when the
provider marks it required. A resolved key that does not start with the
provider's declared prefix SHALL be rejected.
Secrets SHALL never be written into an Outfit file.

The file sits beside the Outfit for the same reason `PRESET` and `REMOTE` do:
an Outfit and the key it needs belong to the same project and travel together,
and a location relative to the tool cannot be resolved by an installed binary.

#### Scenario: The key travels with the Outfit

- **WHEN** an Outfit is applied and a `.env` beside it sets the provider's key
  variable, which is unset in the environment
- **THEN** that value is used

#### Scenario: A command with no Outfit reads the working directory

- **WHEN** a selection made entirely from flags is applied and a `.env` in the
  working directory sets the provider's key variable
- **THEN** that value is used

#### Scenario: Another project's .env is not consulted

- **WHEN** an Outfit is applied and the key variable is set only in a `.env`
  belonging to a different directory
- **THEN** the key does not resolve from that file

#### Scenario: Required key missing

- **WHEN** the user adds a provider whose key is required and the variable is
  set in neither `.env` nor the environment
- **THEN** the command fails naming the variable to set

#### Scenario: Malformed key

- **WHEN** the resolved key does not start with the provider's declared prefix
- **THEN** the command fails saying the key does not look right
