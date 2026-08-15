## MODIFIED Requirements

### Requirement: Stats subcommand

The system SHALL provide a `metrics` subcommand (`outfit remote metrics`) that reports the current state of a remote inference instance. It SHALL accept the same Outfit resolution as `start`, `stop`, and `deploy` — an optional positional Outfit path, defaulting to `./Outfit` when present — and SHALL require the Outfit to name a `REMOTE` environment. When the instance is running, the report SHALL include the outfit version from the daemon, carried by the stats Lambda reply.

#### Scenario: Stats with a running instance

- **WHEN** the user runs `outfit remote metrics` with a running instance
- **THEN** the command reports the instance state, runner, model, outfit version, GPU info, CPU/RAM usage, token counts, and request counts

#### Scenario: Stats with a stopped instance

- **WHEN** the user runs `outfit remote metrics` and the instance is stopped
- **THEN** the command reports `state: stopped` and no metrics

#### Scenario: Stats resolves the Outfit

- **WHEN** the user runs `outfit remote metrics` in a directory with an `Outfit` that has a `REMOTE` instruction
- **THEN** the command uses that Outfit's remote environment without an explicit path argument

#### Scenario: Stats with explicit Outfit path

- **WHEN** the user runs `outfit remote metrics ./some/Outfit`
- **THEN** the command uses that Outfit's `REMOTE` environment

#### Scenario: Version is shown in stats output

- **WHEN** the user runs `outfit remote metrics` with a running instance
- **THEN** the output includes the outfit version
