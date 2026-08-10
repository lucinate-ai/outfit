# remote-logs Specification

## Purpose
Define how an operator reads the logs a remote environment's instances have
shipped: which environment and which of the two sources (the inference engine's
output and the boot output) are read, how much is fetched and in what order,
following output as it arrives, and the failures that are named so they can be
fixed. Reading is deliberately independent of the instance and of the control
plane, so the logs are still there once the instance that wrote them is gone —
which is when they are wanted most.
## Requirements
### Requirement: An environment's shipped logs are readable from the CLI

`outfit remote logs` SHALL print the logs an environment's instances have
shipped, without the operator needing to know the log group or stream naming,
open the AWS console, or connect to an instance. It SHALL select which
environment to read using the same rules as the other remote subcommands — an
explicit Outfit path or alias, else `./Outfit`'s `REMOTE`, else the `default`
environment — so `outfit remote logs` and `outfit remote status` in the same
directory always speak about the same environment.

#### Scenario: Reading the current environment's logs

- **WHEN** the operator runs `outfit remote logs` where `outfit remote status`
  would report on an environment
- **THEN** the log events that environment's instances shipped are printed
- **AND** the operator is not required to name a log group, stream, or instance

#### Scenario: Reading a named environment's logs

- **WHEN** the operator runs `outfit remote logs <path-or-alias>` naming an
  Outfit whose `REMOTE` selects an environment
- **THEN** that environment's logs are printed rather than the default
  environment's

### Requirement: Logs are readable after the instance is gone

Reading logs SHALL NOT depend on an instance being running, nor on the
environment's control endpoints answering. Logs SHALL be read from the durable
store the instances ship to, so the output of a boot that failed, or of an
instance that has since terminated, is still available.

#### Scenario: A terminated instance's logs are still readable

- **WHEN** an instance has produced logs and has since terminated
- **THEN** `outfit remote logs` still prints that instance's shipped events

#### Scenario: A stopped environment can be diagnosed

- **WHEN** an environment is stopped, so its status reports no running instance
- **THEN** `outfit remote logs` still prints the logs from its previous runs

### Requirement: Both engine and boot logs are reachable

The command SHALL be able to read either log source an instance ships — the
inference engine's output and the boot (user-data) output — and both together.
The engine log SHALL be the default source, since it is what an operator wants
once the model is serving. Selecting the boot source SHALL be possible without
knowing which engine the environment runs, so a failure that happened before
the engine started is reachable even though the engine log is empty.

#### Scenario: Engine output by default

- **WHEN** the operator runs `outfit remote logs` with no source selected
- **THEN** the environment's engine log events are printed

#### Scenario: Boot output on request

- **WHEN** the operator asks for the boot source
- **THEN** the environment's start-up output is printed, including steps that
  run before the engine starts

#### Scenario: Both sources interleaved

- **WHEN** the operator asks for all sources
- **THEN** events from both the engine and boot logs are printed together in
  time order
- **AND** each line identifies which source it came from

#### Scenario: The engine need not be named

- **WHEN** an environment's logs are read and the operator has not stated which
  inference engine it runs
- **THEN** the engine's logs are found regardless of which supported engine
  produced them

### Requirement: The volume fetched is bounded and controllable

The command SHALL bound what it fetches by default rather than pulling an
environment's entire retained history, and SHALL let the operator widen or
narrow that: how far back to look, how many events at most to return, and
whether to restrict output to a single instance. When a bound causes older
events to be omitted, the command SHALL say so rather than presenting a
truncated view as complete.

#### Scenario: A default window applies

- **WHEN** the operator runs `outfit remote logs` with no window stated
- **THEN** only events from a bounded recent window are fetched

#### Scenario: The window is widened

- **WHEN** the operator states how far back to look
- **THEN** events from that whole period are fetched, subject to the retention
  of the durable store

#### Scenario: Output is capped

- **WHEN** more events match than the stated maximum
- **THEN** the most recent events up to that maximum are printed
- **AND** the operator is told that earlier matching events were omitted

#### Scenario: One instance is singled out

- **WHEN** the operator names an instance
- **THEN** only that instance's events are printed, and events from the
  environment's other instances are excluded

### Requirement: Output is ordered, timestamped and attributable

Events SHALL be printed oldest first, each carrying its timestamp, so the
output reads like a log rather than an unordered dump. When the printed events
come from more than one instance or more than one source, each line SHALL
identify which instance and source it came from; when there is only one of
each, that labelling SHALL be omitted so the common case stays uncluttered. A
machine-readable output format SHALL also be available, carrying the same
fields for scripting.

#### Scenario: Chronological, timestamped output

- **WHEN** events are printed
- **THEN** they appear oldest first, each preceded by its timestamp

#### Scenario: Mixed origins are labelled

- **WHEN** the printed events come from more than one instance, or from both
  sources
- **THEN** each line identifies its source and instance

#### Scenario: A single origin is not labelled

- **WHEN** every printed event comes from the same source and the same instance
- **THEN** the lines carry no source or instance prefix

#### Scenario: Machine-readable output

- **WHEN** the operator asks for the machine-readable format
- **THEN** the events are emitted as structured records carrying at least the
  timestamp, source, instance and message

### Requirement: New output can be followed

The command SHALL be able to keep running and print events as they arrive,
rather than exiting after one fetch, so an operator can watch a start or a
crash unfold. Following SHALL print each event once — an event already printed
SHALL NOT be repeated on a later poll — and SHALL stop cleanly on interrupt.

#### Scenario: Live output is appended

- **WHEN** the operator follows an environment's logs and the instance writes
  more output
- **THEN** the new events are printed as they arrive, after the events already
  shown

#### Scenario: No duplicates while following

- **WHEN** following continues across several polls
- **THEN** no event that has already been printed is printed again

#### Scenario: Interrupting stops cleanly

- **WHEN** the operator interrupts a follow
- **THEN** the command exits without reporting an error

### Requirement: Missing logs and missing access are explained

When no output can be produced, the command SHALL distinguish the causes an
operator can act on and say what to do, rather than printing nothing or a raw
service error. It SHALL cover at least: an environment whose stored
configuration does not name the environment, so its streams cannot be
identified; a shared layer deployed before log shipping existed, so the log
group is absent; credentials that lack permission to read the logs; and an
environment that simply has not logged anything in the window asked for.

#### Scenario: The environment is not named in its config

- **WHEN** the resolved configuration carries no environment name
- **THEN** the command fails with a message saying the environment cannot be
  identified and how to re-register it

#### Scenario: The log group does not exist

- **WHEN** the log group the environment would ship to is absent
- **THEN** the command reports that the shared layer predates log shipping and
  needs re-deploying, rather than reporting an empty result

#### Scenario: Credentials cannot read logs

- **WHEN** the caller's credentials are not permitted to read the log events
- **THEN** the command reports that the credentials lack log-reading permission
  and names the permission needed

#### Scenario: Nothing was logged in the window

- **WHEN** the log group exists and is readable but holds no events for the
  environment in the window asked for
- **THEN** the command reports that there are no events for that environment in
  that window, and exits without an error

