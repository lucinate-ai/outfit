# Inference Runners Specification

## Purpose

Define which inference engine serves a deployment, how it is chosen and
configured, and how the engine's command is built.
## Requirements
### Requirement: Choosing an inference engine

A deployment SHALL serve through one of a fixed set of inference engines. There
SHALL be no default: an engine is a deliberate choice, and a deployment whose
engine is unset or unrecognised SHALL fail loudly rather than assume one, at
the point the configuration is accepted and again when an instance is started.

Each engine SHALL have its own machine image, identified by the engine it was
built for, so that starting an instance uses an image carrying that engine.
Both images SHALL remain available, so moving a deployment from one engine to
the other is a matter of what is deployed, not of rebuilding anything.

#### Scenario: No engine chosen

- **WHEN** a configuration naming no engine is deployed
- **THEN** it is rejected saying which engines are accepted

#### Scenario: The image matches the engine

- **WHEN** an instance is started for a deployment
- **THEN** it is launched from the most recent image built for that
  deployment's engine

#### Scenario: Changing engine

- **WHEN** a deployment naming the other engine is applied
- **THEN** the next start serves through that engine, with no image rebuilt

### Requirement: What to serve is stored, not built in

What a deployment serves — its engine, model, quantisation, context window,
the parallelism (concurrent request slots) it should run with, the name it
serves under, and the engine's own arguments — SHALL be held as a single
stored configuration, read when an instance starts. Changing any of it SHALL
therefore take effect on the next start without redeploying the
infrastructure.

That configuration SHALL be owned by whoever deploys it, and SHALL NOT be
overwritten by deploying the infrastructure itself, so a routine deployment
cannot silently replace what is being served. An instance started before
anything has been configured SHALL fail saying so.

#### Scenario: Changing model does not rebuild anything

- **WHEN** a configuration naming a different model is deployed
- **THEN** the next start serves that model, with no image or infrastructure
  change

#### Scenario: Deploying the infrastructure preserves what is served

- **WHEN** the infrastructure is deployed again after a configuration has been
  set
- **THEN** the stored configuration is left exactly as it was

#### Scenario: Started before being configured

- **WHEN** an instance is started while no configuration has been stored
- **THEN** it fails saying that something must be deployed first

### Requirement: Building the engine's command

The engine's command line SHALL be derived from the stored configuration. The
settings that belong to the deployment rather than the model — the address and
port to listen on, where the weights are on disk, where each companion weight
is on disk, the API key, the context window, the parallelism (concurrent
request slots), the served name, and metrics — SHALL be set by the deployment
itself, and the configuration's remaining arguments SHALL be passed through
unchanged. Parallelism SHALL be translated into the runner's own flag the same
way a local `outfit serve` would (see the `local-serving` capability's
Parallelism requirement), including scaling the context window for a
`llamacpp` runner when both a context window and a parallelism value are
stored.

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

#### Scenario: A llamacpp deployment scales context by parallelism

- **WHEN** a stored configuration has a `llamacpp` runner, a context window,
  and a parallelism of `n`
- **THEN** the started engine's command carries a context-size flag scaled by
  `n` and a parallel-slots flag set to `n`, matching what a local `outfit
  serve` of the same Outfit would produce

#### Scenario: A vllm deployment's context is unaffected by parallelism

- **WHEN** a stored configuration has a `vllm` runner, a context window, and a
  parallelism value
- **THEN** the started engine's command carries the context window unscaled
  and a concurrency flag set from the parallelism value

