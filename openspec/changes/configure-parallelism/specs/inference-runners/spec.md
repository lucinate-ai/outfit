## MODIFIED Requirements

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
port to listen on, where the weights are on disk, the API key, the context
window, the parallelism (concurrent request slots), the served name, and
metrics — SHALL be set by the deployment itself, and the configuration's
remaining arguments SHALL be passed through unchanged. Parallelism SHALL be
translated into the runner's own flag the same way a local `outfit serve`
would (see the `local-serving` capability's Parallelism requirement),
including scaling the context window for a `llamacpp` runner when both a
context window and a parallelism value are stored.

The API key SHALL be given to the engine by reference to a file readable only
by the owner, never as a command-line argument, so it does not appear in the
machine's process list.

#### Scenario: The deployment's own settings are not taken from the request

- **WHEN** a configuration's arguments include a listen address or a context
  window
- **THEN** the deployment's values are used for them

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
