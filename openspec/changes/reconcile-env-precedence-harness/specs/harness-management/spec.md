## MODIFIED Requirements

### Requirement: Keys reach the launched agent

When outfit launches a harness, the launched agent's environment SHALL carry the
worn Outfit's local environment: the whole `.env` file beside that Outfit, and
the Outfit's own `ENV` instructions, in addition to the API key variables outfit
can resolve for the catalogue's providers. The precedence, highest to lowest,
SHALL be the Outfit's `ENV` instructions, then a variable already present in
outfit's own environment, then the adjacent `.env`. An `ENV` instruction SHALL
therefore override an exported variable; the `.env` SHALL only fill a variable
that is otherwise unset. These values SHALL be placed only in the launched
agent's environment — outfit SHALL NOT mutate its own process environment on this
path. When outfit launches with no Outfit worn, only the process environment is
passed on. Neither harness stores a secret itself — each resolves a reference
when it runs — so a key kept where only outfit reads it still reaches the agent.
Failure to read the provider catalogue SHALL NOT prevent the launch.

#### Scenario: A key only outfit can see still reaches the agent

- **WHEN** outfit can resolve a provider's key variable but it is absent from
  the environment, and the harness is launched
- **THEN** the launched agent's environment carries that variable

#### Scenario: An explicit setting is not overridden by the .env

- **WHEN** a variable is set both in outfit's environment and in the `.env`
  beside the worn Outfit, and the harness is launched
- **THEN** the launched agent sees the environment's value, not the `.env` value

#### Scenario: The adjacent .env fills a gap for the agent

- **WHEN** a variable is set in the `.env` beside the worn Outfit and is unset in
  outfit's environment, and the harness is launched
- **THEN** the launched agent's environment carries the `.env` value

#### Scenario: An ENV instruction overrides both

- **WHEN** the worn Outfit sets a variable with an `ENV` instruction and the same
  variable is also present in outfit's environment and/or the adjacent `.env`,
  and the harness is launched
- **THEN** the launched agent sees the `ENV` value

#### Scenario: Launching without an Outfit passes only the environment

- **WHEN** the harness is launched with no Outfit worn
- **THEN** no adjacent `.env` or `ENV` values are added and the agent receives
  outfit's process environment

#### Scenario: An unreadable catalogue still launches the agent

- **WHEN** the provider catalogue cannot be loaded
- **THEN** the harness is launched anyway, with the environment otherwise
  unchanged
