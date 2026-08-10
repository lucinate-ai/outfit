## ADDED Requirements

### Requirement: Status reports the engine's endpoint

Status SHALL report where the supervised engine serves: the port it listens on,
the OpenAI-compatible path prefix under it when that is not the default, whether
the bind is loopback-only, and whether the engine requires an API key. This is
the one thing about a node a remote caller cannot work out for itself — the
engine's port is not the control API's, and nothing in the reply implies it —
so a router asking "where do I send inference?" gets an answer rather than a
guess.

The engine endpoint SHALL be omitted when no engine is running: an address for
a process that does not exist is worse than no address.

The reported key requirement SHALL say only whether a key is needed. The API
SHALL NOT return the engine's key under any endpoint: a caller authorised to
drive a node is not thereby authorised to be handed its engine's credential.

#### Scenario: A running engine reports where it serves

- **WHEN** a status request is made while an engine is running
- **THEN** the response reports the engine's port, whether the bind is
  loopback-only, and whether it requires a key

#### Scenario: The engine port is distinct from the API port

- **WHEN** a status request is made on a daemon whose control API and engine
  listen on different ports
- **THEN** the reported engine port is the engine's, not the API's

#### Scenario: A gated engine says so without saying what

- **WHEN** a status request is made while an engine started with an API key is
  running
- **THEN** the response says a key is required and carries no key value

#### Scenario: No engine, no endpoint

- **WHEN** a status request is made while the engine is idle, stopped, or
  crashed
- **THEN** the response carries no engine endpoint
