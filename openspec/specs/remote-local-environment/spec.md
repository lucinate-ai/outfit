# Remote Local Environment Specification

## Purpose

Define how the `remote` control commands load the local environment tied to a
resolved Outfit — the adjacent `.env` file and the Outfit's own `ENV`
instructions — so that AWS credentials, region resolution, and
`OUTFIT_REMOTE_*` overrides see those values, while keeping them local to the
device invoking `outfit`.

## Requirements

### Requirement: Remote commands load the Outfit's local environment

The `remote` control commands that resolve an Outfit — `deploy`, `start`,
`stop`, `status`, and `stats` — SHALL,
before resolving remote configuration or performing any AWS or control-plane
work, load environment variables into the process environment from two sources
tied to the resolved Outfit: the `.env` file beside that Outfit, and the Outfit's
own `ENV` instructions. The loaded values SHALL be visible to everything the
command does afterwards — the AWS credential chain, the region resolution, and
the `OUTFIT_REMOTE_*` overrides. When a command resolves no Outfit (no path
argument and no `./Outfit`), there is nothing adjacent to load and the command
SHALL proceed on the process environment alone.

#### Scenario: A .env beside the Outfit reaches the control calls

- **WHEN** `outfit remote status ./Outfit` runs and a `.env` beside that Outfit
  sets `OUTFIT_REMOTE_START_URL`, unset in the process environment
- **THEN** the command uses that value when it contacts the control endpoint

#### Scenario: Every control command loads the local environment

- **WHEN** any of `deploy`, `start`, `stop`, `status`, or `stats` resolves an
  Outfit
- **THEN** that Outfit's adjacent `.env` and its `ENV` instructions are loaded
  before the command performs any AWS or control-plane work

#### Scenario: No Outfit, nothing to load

- **WHEN** a `remote` command runs with no Outfit resolved and falls back to the
  per-user configuration
- **THEN** no adjacent `.env` is read and the command proceeds on the process
  environment alone

### Requirement: Precedence of local environment sources

When the same variable is set in more than one source, the value SHALL be chosen
with the precedence, highest to lowest: the Outfit's `ENV` instruction, then a
variable already present in the process environment, then the `.env` beside the
Outfit. A variable already set in the process environment SHALL therefore win
over the `.env`, which only fills gaps; an `ENV` instruction SHALL override both.

#### Scenario: The process environment wins over .env

- **WHEN** a variable is set both in the process environment and in the `.env`
  beside the Outfit
- **THEN** the process environment's value is used and the `.env` value is
  ignored

#### Scenario: .env fills a gap

- **WHEN** a variable is set in the `.env` beside the Outfit and is unset in the
  process environment
- **THEN** the `.env` value is used

#### Scenario: ENV overrides both

- **WHEN** a variable is set by an Outfit `ENV` instruction and also in the
  process environment and/or the `.env`
- **THEN** the `ENV` value is used, overriding both

### Requirement: ENV is local-only

Variables established from the Outfit's `ENV` instructions (and its adjacent
`.env`) SHALL affect only the local device invoking `outfit`. They SHALL NOT be
included in the deploy payload sent to the instance, nor otherwise transmitted to
the deployed instance.

#### Scenario: Deploy does not forward ENV to the instance

- **WHEN** `outfit remote deploy` runs for an Outfit that declares `ENV` variables
- **THEN** the configuration sent to the deploy endpoint contains none of those
  variables — they shape only the local command's own environment
