# engine-credentials Specification

## Purpose
Where the API key that gates an inference engine comes from, how it reaches the
engine without being exposed to everyone on the machine, and what a node will
and will not tell a caller about it.
## Requirements
### Requirement: The caller supplies the engine's key

The client that asks a node to start an engine SHALL supply the API key that
engine is gated with, in the start request. The node SHALL NOT source a key of
its own: not from a file beside it, not from its environment, not from a preset.

This puts one party in charge of the secret. A client already holds the node's
bearer token, so it can already run engine commands on that machine and read
its logs; supplying a key is less than what it can already do. And because the
client sets the key, it knows the key — so nothing has to reconcile what the
node was gated with against what the client believes.

A start request that carries no key SHALL start an ungated engine, which is
correct for a node reached only over loopback.

#### Scenario: A start request gates the engine

- **WHEN** a start request carries an engine API key
- **THEN** the engine is started requiring that key, and reports itself as
  requiring one

#### Scenario: No key starts an ungated engine

- **WHEN** a start request carries no engine API key
- **THEN** the engine starts accepting unauthenticated requests, and reports
  that it requires no key

#### Scenario: The node supplies no key of its own

- **WHEN** a start request carries no key and the node's own environment holds
  one
- **THEN** the engine is still ungated: the node's environment is not a source

### Requirement: The key is never passed on a command line

A supplied key SHALL reach the engine through a file the daemon writes, passed
as the engine's key-file argument — never as a literal argument. A process's
command line is readable by every local user, so a key passed that way is
disclosed to anyone with a shell on that machine, including whoever else the
node is shared with.

The file SHALL be readable only by the user the daemon runs as, and SHALL be
replaced rather than accumulated when a new engine is started with a new key.

#### Scenario: The key does not appear in the process list

- **WHEN** an engine is started with a supplied key
- **THEN** the engine's command line carries a path, not the key, and the key
  cannot be read from the process list

#### Scenario: The key file is private

- **WHEN** the daemon writes a supplied key
- **THEN** the file it writes is readable only by the user the daemon runs as

#### Scenario: A new key replaces the old one

- **WHEN** an engine is started with a key, stopped, and started again with a
  different key
- **THEN** the second engine is gated with the second key and the first is no
  longer on disk

### Requirement: A node reports that a key is needed, never the key

A node SHALL report whether its engine requires a key and SHALL NOT return the
key under any endpoint. A caller authorised to drive a node is not thereby
authorised to be handed its engine's credential, and a client that set the key
already has it.

#### Scenario: The requirement is reported without the value

- **WHEN** a caller reads the status of a node whose engine is gated
- **THEN** the reply says a key is required and carries no key value

#### Scenario: No endpoint discloses the key

- **WHEN** any control endpoint is called on a node whose engine is gated
- **THEN** no reply carries the engine's key

### Requirement: A stored key gates a later start

A key supplied with a start SHALL be persisted with the deploy config it
accompanied, so a start request that carries neither — a restart of what the
node was already running — gates the engine the same way rather than silently
opening it.

Persisting a secret on the node is deliberate and bounded: it is stored with the
same protection as the deploy config, which already carries whatever the serve
arguments contain, and it is replaced whenever a new key arrives.

#### Scenario: A bare start reuses the stored key

- **WHEN** a start request carrying a key is followed later by a start request
  carrying neither a config nor a key
- **THEN** the engine starts gated with the stored key

#### Scenario: A new key supersedes the stored one

- **WHEN** a start request carries a key different from the stored one
- **THEN** the engine is gated with the new key and the new key is what a later
  bare start uses

