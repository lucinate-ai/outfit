# Cloud Local Environment Specification

## Purpose

Define how the `cloud` control commands load the local environment tied to a
resolved Spinloop — the adjacent `.env` file and the Spinloop's own `ENV`
instructions — so that AWS credentials, region resolution, and
`SPINLOOP_CLOUD_*` overrides see those values, while keeping them local to the
device invoking `spinloop`.

## Requirements

### Requirement: Cloud commands load the Spinloop's local environment

The `cloud` control commands that resolve a Spinloop — `deploy`, `start`,
`stop`, `status`, `stats`, and the other subcommands that take one — SHALL,
before performing any AWS or control-plane work, load environment variables
into the process environment from two sources tied to the resolved Spinloop:
the `.env` file beside that Spinloop, and the Spinloop's
own `ENV` instructions. The loaded values SHALL be visible to everything the
command does afterwards — the AWS credential chain, the region resolution, and
the `SPINLOOP_CLOUD_*` overrides. The Spinloop is read for these local values
only: which environment the command acts on is selected by the command's
`--env` flag, with the `default` environment as the fallback, and the Spinloop
itself names no environment. When a command resolves no Spinloop (no path
argument and no `./Spinloop`), there is nothing adjacent to load and the command
SHALL proceed on the process environment alone. When the resolved Spinloop was
fetched from a URL, there is likewise no local `.env` beside it to load — a
URL has no local directory — and the command SHALL proceed on the Spinloop's own
`ENV` instructions and the process environment alone, exactly as the
no-Spinloop-resolved case does.

#### Scenario: A .env beside the Spinloop reaches the control calls

- **WHEN** `spinloop cloud status ./Spinloop` runs and a `.env` beside that Spinloop
  sets `SPINLOOP_CLOUD_START_URL`, unset in the process environment
- **THEN** the command uses that value when it contacts the control endpoint

#### Scenario: Every control command loads the local environment

- **WHEN** any `cloud` subcommand resolves a Spinloop
- **THEN** that Spinloop's adjacent `.env` and its `ENV` instructions are loaded
  before the command performs any AWS or control-plane work

#### Scenario: No Spinloop, nothing to load

- **WHEN** a `cloud` command runs with no Spinloop resolved and acts on the
  `default` environment
- **THEN** no adjacent `.env` is read and the command proceeds on the process
  environment alone

#### Scenario: A URL-sourced Spinloop has no adjacent .env

- **WHEN** a `cloud` command resolves a Spinloop fetched from a URL
- **THEN** no `.env` read is attempted, and only that Spinloop's own `ENV`
  instructions and the process environment are applied

### Requirement: Precedence of local environment sources

When the same variable is set in more than one source, the value SHALL be chosen
with the precedence, highest to lowest: the Spinloop's `ENV` instruction, then a
variable already present in the process environment, then the `.env` beside the
Spinloop. A variable already set in the process environment SHALL therefore win
over the `.env`, which only fills gaps; an `ENV` instruction SHALL override both.

#### Scenario: The process environment wins over .env

- **WHEN** a variable is set both in the process environment and in the `.env`
  beside the Spinloop
- **THEN** the process environment's value is used and the `.env` value is
  ignored

#### Scenario: .env fills a gap

- **WHEN** a variable is set in the `.env` beside the Spinloop and is unset in the
  process environment
- **THEN** the `.env` value is used

#### Scenario: ENV overrides both

- **WHEN** a variable is set by a Spinloop `ENV` instruction and also in the
  process environment and/or the `.env`
- **THEN** the `ENV` value is used, overriding both

### Requirement: ENV is local-only

Variables established from the Spinloop's `ENV` instructions (and its adjacent
`.env`) SHALL affect only the local device invoking `spinloop`. They SHALL NOT be
included in the deploy payload sent to the instance, nor otherwise transmitted to
the deployed instance.

#### Scenario: Deploy does not forward ENV to the instance

- **WHEN** `spinloop cloud deploy` runs for a Spinloop that declares `ENV` variables
- **THEN** the configuration sent to the deploy endpoint contains none of those
  variables — they shape only the local command's own environment
