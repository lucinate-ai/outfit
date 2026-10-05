## Purpose

Work the items of a running orchestrator from the shell — add an item, read
the backlog, stop a running item, remove an item — as a client of the
orchestrator's work list API: the commands name the API's address, present its
token, and the run's view of the items is the source of truth.

## Requirements

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

### Requirement: Reading an item's log through the work list API

`spinloop work logs <id>` SHALL call the API's `GET /v1/items/{id}/log`
path for the item's kept agent output and print it. An item the API
answers as carrying no output yet SHALL be printed as empty, not treated
as a fault. An id the run does not carry SHALL be refused, naming it, the
way the API states it.

Given `-f`/`--follow`, the command SHALL instead poll the API for new
output and print it as it arrives, the way `tail -f` does: it SHALL keep
polling while the item's state is `backlog` or `running` — an item named
before, or just as, it starts is still followed — and SHALL stop once the
API reports the item `done` or `failed`, printing whatever output arrived
up to that point first. The operator's interrupt SHALL also end a follow,
cleanly. The command SHALL NOT itself read the items file, the state, or
the logs directly: the API's answers are the whole source of what it
prints and when it stops.

#### Scenario: An item's kept output is printed

- **WHEN** the operator runs `work logs` naming an item with kept output
- **THEN** the command prints it

#### Scenario: An item with no output yet is empty, not a fault

- **WHEN** the operator runs `work logs` naming an item the API answers
with no output yet
- **THEN** the command prints nothing, and does not fail

#### Scenario: An id the run does not carry is refused

- **WHEN** the operator runs `work logs` naming an id the API does not
carry
- **THEN** the command fails, naming the id, the way the API states it

#### Scenario: A follow streams new output as it arrives

- **WHEN** the operator runs `work logs -f` on a running item, and its
agent produces more output
- **THEN** the command prints the new output as later polls see it

#### Scenario: A follow waits through the backlog

- **WHEN** the operator runs `work logs -f` on an item the run has not yet
started
- **THEN** the command keeps polling rather than ending, and starts
printing output once the item runs

#### Scenario: A follow ends once the item ends

- **WHEN** a followed item's state becomes `done` or `failed`
- **THEN** the command prints whatever output arrived up to that point and
ends

#### Scenario: A follow ends on the operator's interrupt

- **WHEN** the operator interrupts a running follow
- **THEN** the command ends cleanly, without reporting a failure

### Requirement: Adding an item through the work list API

`spinloop work add` SHALL take the item's fields from flags — its id, its
instructions, its working directory, its tags, repeatable, and its numeric
priority — and send them to the API's add path as the item to add. The API
applies the items file's validation on the fields and its own refusals, and the
command SHALL report the API's answer: where the API refuses, the command SHALL
fail naming the refusal the way the API states it — a field the validation
rejects, an id the file already carries, or an id the state has recorded done or
failed — and where the API accepts, the command SHALL say the item is added. The
command SHALL NOT itself check the file or the state: the API is the one that
holds them.

#### Scenario: An item's fields come from flags

- **WHEN** the operator runs add naming an id, instructions, a working
  directory, two tags, and a priority
- **THEN** the command sends the item to the API with those fields

#### Scenario: A field the validation refuses

- **WHEN** the operator runs add with no working directory, or a tag that is
  not a `key=value` pair
- **THEN** the API refuses, and the command fails naming the item and the fault

#### Scenario: An id the file already has

- **WHEN** the operator runs add with an id the items file carries
- **THEN** the API refuses, and the command fails naming the id

#### Scenario: An id the state has recorded ended

- **WHEN** the operator runs add with an id the state beside the file has
  recorded done or failed
- **THEN** the API refuses, naming the record, and the command fails saying so

#### Scenario: An accepted item is reported

- **WHEN** the operator runs add and the API accepts the item
- **THEN** the command says the item is added

### Requirement: Listing the work from the work list API

`spinloop work list` SHALL read the API's list and show every item the run holds
with its record, in the file's order: each item's id, its state — backlog,
running, done or failed — the node a running item runs on, and when it started
and ended. An item with no record SHALL show as backlog. The command's output
SHALL be plain lines a program can consume, one per item, and decoration SHALL
be drawn only where there is a terminal to draw it on: there, the columns
SHALL line up as a table, each column as wide as its widest value including
the heading, and the table SHALL open with a heading row naming the columns.
`ls` SHALL be an alias of list.

#### Scenario: Every item shows its state

- **WHEN** the API's list carries items the run records running, done, failed
  and not at all
- **THEN** the list shows them, in the file's order, running with its node and
  start, done and failed with their ends, and the unrecorded one backlog

#### Scenario: A piped run gets plain lines

- **WHEN** the operator pipes the list into another program
- **THEN** the pipe carries one plain line per item, in order, with no
  decoration

#### Scenario: A terminal gets a heading and aligned columns

- **WHEN** the operator runs list on a terminal, and an item's id is longer
  than a value already shown in that column
- **THEN** the table opens with a heading row naming the columns, and every
  row's columns still line up under it

#### Scenario: The alias works

- **WHEN** the operator runs `spinloop work ls`
- **THEN** it lists the work, the way `spinloop work list` does

### Requirement: Aborting an item through the work list API

`spinloop work abort` SHALL take an item's id and call the API's abort path for
it, which stops the item's agent the way the run stops one on a clean interrupt
and puts the item back in the backlog. Only a running item SHALL be abortable:
the API refuses where the item is not running, naming the item and its state,
and where the file does not carry the id, naming it, and the command SHALL
report the API's answer. The command SHALL NOT itself mark the item for abort or
wait for a run to take a change in: the API's call is the whole ask, and it
answers once the item is stopped.

#### Scenario: A running item is stopped

- **WHEN** the orchestrator is running an item, and the operator aborts it
- **THEN** the API stops the item's agent and puts it back in the backlog, and
  the command says the item is stopped

#### Scenario: An item that is not running is refused

- **WHEN** the operator aborts an item the run records backlog, done or failed
- **THEN** the API refuses, naming the item and its state, and the command
  fails saying so

#### Scenario: An id the file does not carry

- **WHEN** the operator aborts an id the items file does not carry
- **THEN** the API refuses, naming the id, and the command fails naming it

### Requirement: Removing an item through the work list API

`spinloop work remove` SHALL take an item's id and call the API's remove path
for it, which takes the item out of the work list: the items file, its record,
and its kept output. A running item SHALL be refused, naming the item and the
abort that goes first, and an id the file does not carry SHALL be refused,
naming it, and the command SHALL report the API's answer. The command SHALL NOT
itself edit the file, the state, or the logs, or wait for a run to take a change
in: the API's call is the whole ask, and it answers once the item is out.

#### Scenario: An item is removed

- **WHEN** the operator removes an item that is backlog, done or failed
- **THEN** the API takes it out of the work list, and the command says the item
  is removed

#### Scenario: A running item is refused

- **WHEN** the operator removes an item the run records running
- **THEN** the API refuses, naming the item and that it aborts first, and the
  command fails saying so

#### Scenario: An id the file does not carry

- **WHEN** the operator removes an id the items file does not carry
- **THEN** the API refuses, naming the id, and the command fails naming it

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
