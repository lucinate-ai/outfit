## Purpose

Define the operator's command-line surface for model weight seeds: starting one,
asking after one, listing those in flight, and stopping one — without the
operator needing to know how seeds are identified, where their records are kept,
or which compute is running them.

## ADDED Requirements

### Requirement: A seed is startable from the CLI

`outfit remote seed start` SHALL request a seed for the weights an Outfit names,
resolving what to seed by the same rules the other remote subcommands use — an
explicit Outfit path or alias, else `./Outfit`, else the default environment — so
that seeding and deploying in the same directory always speak about the same
model.

It SHALL report the seed's identity, and SHALL say whether it started a new seed
or joined one already in flight, so a repeated invocation is unambiguous rather
than looking like a fresh start.

It SHALL be able to seed weights that are already stored when explicitly asked
to, which is the supported way to replace weights in place; nothing outside the
CLI SHALL be needed for that.

#### Scenario: Starting a seed for the current Outfit

- **WHEN** the operator runs `outfit remote seed start` where `outfit remote
  deploy` would deploy a model
- **THEN** a seed for that model's weights is requested, and its identity is
  printed

#### Scenario: A repeated start says it joined

- **WHEN** the operator starts a seed that is already in flight
- **THEN** the output says it joined the running seed rather than started one

#### Scenario: Forcing a re-seed

- **WHEN** the operator asks to seed weights that are already stored, and asks
  explicitly for them to be seeded again
- **THEN** a seed is started

### Requirement: A seed's state is readable from the CLI

`outfit remote seed status` SHALL report a seed's phase, how far it has got, and
its outcome once it has one, without the operator naming a log group or stream,
opening the provider's console, or connecting to any instance.

It SHALL report on a seed whose compute is gone, because that is when a failed
seed most needs explaining. A seed that died without reporting an outcome SHALL be
reported as failed, not as in progress.

#### Scenario: Reading a running seed's progress

- **WHEN** the operator asks after a seed that is transferring files
- **THEN** its phase and progress are printed

#### Scenario: Reading a finished seed

- **WHEN** the operator asks after a seed that has completed
- **THEN** its outcome is printed, and no instance needs to exist

#### Scenario: Reading a seed that died

- **WHEN** the operator asks after a seed whose compute vanished mid-transfer
- **THEN** it is reported as failed, with what it was last doing

### Requirement: Seeds in flight are listable

`outfit remote seed ls` SHALL list the seeds currently in flight across the
account, each with its identity, what it is seeding, how long it has been
running and its phase, so an operator can see what is costing money without
querying each seed by name.

An empty list SHALL be stated plainly rather than printed as nothing, so that "no
seeds are running" is distinguishable from a command that failed quietly.

#### Scenario: Listing running seeds

- **WHEN** seeds are in flight
- **THEN** each is listed with its identity, what it is seeding, its age and its
  phase

#### Scenario: Listing when none are running

- **WHEN** no seeds are in flight
- **THEN** the output says so

### Requirement: A seed is stoppable from the CLI

`outfit remote seed stop` SHALL stop a named seed, ceasing its compute, and the
stopped outcome SHALL be recorded so that a later status reports it as stopped
rather than as failed or as still running.

Stopping a seed that is not running SHALL be reported as such and SHALL NOT be an
error, so that stopping twice is safe.

#### Scenario: Stopping a running seed

- **WHEN** the operator stops a seed that is in flight
- **THEN** its compute ceases, and a later status reports it as stopped

#### Scenario: Stopping a seed that is not running

- **WHEN** the operator stops a seed that is already finished or was never started
- **THEN** the output says it is not running, and the command succeeds

### Requirement: A deploy's automatic seed is followable

Where deploying starts a seed because the weights were absent, the deploy output
SHALL name the command that follows that seed, so the operator's next step is
stated rather than inferred.

Following that seed SHALL query the seed surface directly. Deploying SHALL NOT be
the route through which a seed's progress is read, so that reading progress does
not re-run a deployment's work.

#### Scenario: A deploy that seeds says how to follow it

- **WHEN** `outfit remote deploy` starts a seed because the weights were absent
- **THEN** its output names the seed and the command that reports on it

#### Scenario: Progress is not read through deploy

- **WHEN** the operator follows a seed started by a deploy
- **THEN** the seed is queried directly, and no deployment work is repeated

### Requirement: Seed command failures are named

Where a seed command cannot proceed, the reason SHALL be stated with the action
that fixes it: a remote configuration without a seed endpoint SHALL say which
value to add and how to obtain it, an unknown seed identity SHALL say that no
such seed is known, and a refusal because too many seeds are in flight SHALL say
so rather than appear as a generic failure.

A remote configuration written before the seed endpoint existed SHALL keep
working for every other remote subcommand, so adding seeds does not invalidate
existing configurations.

#### Scenario: No seed endpoint configured

- **WHEN** a seed command runs against a remote configuration that names no seed
  endpoint
- **THEN** the output says which value to add and where the deployment prints it

#### Scenario: An older configuration still works

- **WHEN** a remote configuration predating the seed endpoint is used for another
  remote subcommand
- **THEN** that subcommand works as before

#### Scenario: An unknown seed

- **WHEN** the operator asks after a seed identity that is not known
- **THEN** the output says no such seed is known

#### Scenario: Refused because too many seeds are running

- **WHEN** starting a seed is refused because the cap on seeds in flight is reached
- **THEN** the output says the cap was reached
