## MODIFIED Requirements

### Requirement: Engine token references

A node MAY name the environment variable holding the key its engine is to be
gated with, separately from the daemon's own bearer token — the two are
different credentials and a node may need either, both, or neither. The value
SHALL NOT be written in `fleet.yaml`, and SHALL be resolved exactly as the
daemon token reference is: the process environment first, then a `.env` beside
the fleet file.

The reference is the *client's* lookup, not a description of the node. The
client resolves it and supplies the key when it starts an engine there, so the
node holds no key of its own and the two ends cannot disagree about what it is.
It is also the seam for resolving keys from somewhere better than an environment
variable — a keychain, a secret manager — without anything else changing.

A reference that resolves to nothing SHALL be reported against that node, naming
the variable, in the same way a missing daemon token is.

#### Scenario: The engine key is a reference

- **WHEN** a node names an engine token variable that is set
- **THEN** the client resolves that value and gates that node's engine with it

#### Scenario: The fleet file holds no engine key

- **WHEN** a `fleet.yaml` is parsed
- **THEN** it carries only a reference to the engine key, never a literal value

#### Scenario: An unset engine token variable names itself

- **WHEN** a node names an engine token variable that is set nowhere
- **THEN** the failure names that variable and that node

#### Scenario: A node naming no key is not gated

- **WHEN** a node's entry names no engine token variable and the client starts
  an engine there
- **THEN** the engine is started ungated
