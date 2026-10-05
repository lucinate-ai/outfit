## MODIFIED Requirements

### Requirement: The work commands as work list clients

`spinloop work` SHALL be a top-level command group with the subcommands
add, list, abort, retry, remove, logs and board, each a client of the
orchestrator's work list API. Every subcommand SHALL take a `--url` flag
naming the API's base address, and SHALL present the API's token as a
bearer on every request it makes — resolved from `--api-token`, else
`--api-token-file`, else the `SPINLOOP_API_TOKEN` environment variable,
two of the flags given at once being a refusal naming both. A subcommand
that names no `--url` SHALL fail before it calls the API, naming the flag.
The commands SHALL be clients of the API alone: they SHALL NOT read or
write the items file, the state, or the logs directly, and SHALL NOT take
any lock beside them.

#### Scenario: The API's address is named

- **WHEN** the operator runs a work command naming the API's address with
  `--url`
- **THEN** it calls that API, presenting the token as a bearer

#### Scenario: No address is named

- **WHEN** the operator runs a work command with no `--url`
- **THEN** it fails, naming the `--url` flag, and calls no API

#### Scenario: Two token flags at once

- **WHEN** the operator gives both `--api-token` and `--api-token-file`
- **THEN** the command fails, naming both flags, and calls no API

#### Scenario: The token comes from the environment

- **WHEN** the operator names the API's address, sets no token flag, and the
  `SPINLOOP_API_TOKEN` environment is set
- **THEN** the command presents that value as the bearer

## ADDED Requirements

### Requirement: Retrying a failed item through the work list API

`spinloop work retry <id>` SHALL call the API's `POST /v1/items/{id}/retry`
path to put a failed item back in the backlog, and report the API's answer:
where the API accepts, the command SHALL say the item is back in the
backlog; where it refuses — an item that is not failed, naming its state, or
an id the run does not carry — the command SHALL fail naming the refusal the
way the API states it. The command SHALL take exactly one id, and SHALL NOT
itself check the item's state: the API is the one that holds it.

#### Scenario: A failed item is retried

- **WHEN** the operator runs `work retry` naming a failed item and the API
  accepts
- **THEN** the command says the item is back in the backlog

#### Scenario: An item that has not failed is refused

- **WHEN** the operator runs `work retry` naming an item that is running,
  done, or backlog
- **THEN** the API refuses, and the command fails naming the item and its
  state

#### Scenario: An id the run does not carry is refused

- **WHEN** the operator runs `work retry` naming an id the API does not carry
- **THEN** the command fails, naming the id, the way the API states it
