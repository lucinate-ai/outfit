## ADDED Requirements

### Requirement: Per-node engine endpoint

A node MAY declare where its engine serves, overriding what its daemon reports:
a host, a port, and a path prefix, each optional and each falling back to what
routing would otherwise derive (the node's own `host`, and the port and path the
daemon reports). This is what covers the setups a daemon cannot describe — an
engine behind a reverse proxy, a container publishing the engine on a different
port than it binds inside, a node reached through a tunnel.

A node that declares no engine block SHALL behave exactly as it does now.

#### Scenario: An override replaces what the daemon reports

- **WHEN** a node's entry declares an engine host and port and routing chooses
  that node
- **THEN** the declared host and port are used, whatever the daemon reports

#### Scenario: A partial override falls back per field

- **WHEN** a node's entry declares only an engine port
- **THEN** that port is used with the node's own host

#### Scenario: No engine block changes nothing

- **WHEN** a fleet file's nodes declare no engine block
- **THEN** they parse and behave as before

### Requirement: Engine token references

A node MAY name the environment variable holding the key its engine requires,
separately from the daemon's own bearer token — the two are different
credentials and a node may need either, both, or neither. The value SHALL NOT be
written in `fleet.yaml`, and SHALL be resolved exactly as the daemon token
reference is: the process environment first, then a `.env` beside the fleet
file.

A reference that resolves to nothing SHALL be reported against that node, naming
the variable, in the same way a missing daemon token is.

#### Scenario: The engine key is a reference

- **WHEN** a node names an engine token variable that is set
- **THEN** routing resolves that value for that node's engine

#### Scenario: The fleet file holds no engine key

- **WHEN** a `fleet.yaml` is parsed
- **THEN** it carries only a reference to the engine key, never a literal value

#### Scenario: An unset engine token variable names itself

- **WHEN** a node names an engine token variable that is set nowhere
- **THEN** the failure names that variable and that node
