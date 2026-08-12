## MODIFIED Requirements

### Requirement: Building the engine's command

The engine's command line SHALL be derived from the stored configuration. The
settings that belong to the deployment rather than the model — the address and
port to listen on, where the weights are on disk, where each companion weight
is on disk, the API key, the context window, the served name, and metrics —
SHALL be set by the deployment itself, and the configuration's remaining
arguments SHALL be passed through unchanged.

Because a companion's location is a deployment-owned setting, an argument in
the stored configuration that names one SHALL be overridden by the
deployment's own location for it, however that argument is spelled. A path that
is meaningful only on the machine a configuration was authored on SHALL NOT
reach the engine.

An engine SHALL be given a companion only when the deployment names one for
that role; a deployment naming no companions SHALL produce the command it
produced before companions existed.

The API key SHALL be given to the engine by reference to a file readable only
by the owner, never as a command-line argument, so it does not appear in the
machine's process list.

#### Scenario: The deployment's own settings are not taken from the request

- **WHEN** a configuration's arguments include a listen address or a context
  window
- **THEN** the deployment's values are used for them

#### Scenario: A companion's location is set by the deployment

- **WHEN** a deployment names a companion weight
- **THEN** the engine's command names that companion at the deployment's own
  location for it

#### Scenario: An authored companion path does not reach the engine

- **WHEN** a stored configuration's arguments name a companion at a path from
  the machine it was authored on
- **THEN** that argument does not reach the engine, and the deployment's
  location is used instead

#### Scenario: No companions leaves the command unchanged

- **WHEN** a deployment names no companion weights
- **THEN** the engine's command carries no companion arguments

#### Scenario: The key is not visible in the process list

- **WHEN** the engine is started with an API key
- **THEN** the key is passed by reference to an owner-only file
