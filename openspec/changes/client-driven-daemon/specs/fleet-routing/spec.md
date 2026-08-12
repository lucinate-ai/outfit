## REMOVED Requirements

### Requirement: The chosen node's engine key

**Reason**: It described a client discovering the key a node was *already* gated
with, and failing when it could not. The client now supplies the key when it
starts the engine, so the direction is reversed rather than adjusted — including
the scenarios, which asserted a lookup that no longer happens.

**Migration**: None for a fleet file: `engineTokenEnv` is still the variable
naming the key, and still resolved from the environment then the adjacent
`.env`. What changes is what outfit does with it — it sets the key rather than
matching one — so a node whose engine was gated out of band by its own preset
must now be gated by the client that starts it.

## ADDED Requirements

### Requirement: The engine key the client sets

Routing SHALL resolve the engine key from the variable the node's fleet entry
names, supply it to the node when it wakes one, and place it in the launched
agent's environment as `OPENAI_API_KEY` — and, for a harness that reads the key
under its own name, under that name too, as the remote path already does. A key
already set in outfit's environment SHALL win.

The client is therefore the one party that holds the key: it decides what the
engine it starts is gated with, and it knows what to give the agent because it
set it. A node whose fleet entry names no key SHALL wake an ungated engine,
which is correct for a node reached over loopback.

Routing SHALL NOT ask the daemon for a key, and the daemon SHALL NOT return one:
saying a key is required is a fact a router needs, and handing the key out is
not.

Where routing selects a node that is *already running* — one it did not wake —
the engine was gated by whoever started it. When such a node reports that it
requires a key and the fleet entry names none, routing SHALL fail before the
agent launches, naming the node and the variable to set: an agent that cannot
authenticate is worse than a message that says so.

#### Scenario: A woken node is gated with the client's key

- **WHEN** routing wakes a node and its fleet entry names a variable holding a
  key
- **THEN** the node is started gated with that key, and the launched agent's
  environment carries the same value as `OPENAI_API_KEY`

#### Scenario: No key wakes an ungated engine

- **WHEN** routing wakes a node whose fleet entry names no engine key
- **THEN** the engine starts ungated and the agent launches with no key injected
  for it

#### Scenario: An already-running gated node with no key fails early

- **WHEN** routing selects a node that is already running a gated engine and the
  node's fleet entry names no key
- **THEN** the command fails naming the node and what to set, and no agent is
  launched

#### Scenario: An unset variable fails before anything starts

- **WHEN** a node's fleet entry names an engine key variable that is set nowhere
- **THEN** the command fails naming the node and the variable, and no engine is
  started

#### Scenario: An exported key wins

- **WHEN** `OPENAI_API_KEY` is already set in the user's environment and a
  fleet-routed launch runs
- **THEN** the existing value reaches the agent unchanged
