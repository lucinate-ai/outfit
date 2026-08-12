## ADDED Requirements

### Requirement: Explaining a route

`outfit fleet route` SHALL report the node a harness launch would choose for a
given Outfit, the endpoint that node resolves to, and why it was chosen — and
SHALL change nothing: it SHALL never push a config, start an engine, or write a
harness config. It is how a routing decision is checked before an agent depends
on it, and how an unexpected choice is diagnosed after one.

When no node would be chosen, it SHALL report each node's state and the reason
it was passed over, and SHALL name what would happen on a real launch: which
node would be woken, or that none could serve it.

The Outfit and the fleet file SHALL resolve as they do for a launch: the Outfit
path defaults to `./Outfit`, and `--fleet` overrides the Outfit's `FLEET`. It
SHALL accept `--prefer` and `--node` as a launch does, and SHALL name the
activity preference in force — comparing the two preferences on a live fleet is
the cheapest way to decide which one a fleet should be run with.

#### Scenario: The chosen node is explained

- **WHEN** `outfit fleet route` runs against a fleet with a node serving the
  Outfit's model
- **THEN** it prints that node, its resolved engine endpoint, and why it was
  chosen

#### Scenario: Routing changes nothing

- **WHEN** `outfit fleet route` runs against a fleet where no node is serving
  the Outfit's model
- **THEN** no engine is started, no config is pushed, and no harness config is
  written

#### Scenario: The two preferences can be compared

- **WHEN** `outfit fleet route --prefer active` runs against a fleet whose file
  declares `prefer: idle`
- **THEN** it reports the node `active` would choose and names that preference,
  without changing the fleet file

#### Scenario: A launch that would wake a node says so

- **WHEN** `outfit fleet route` runs and no node is serving the model but one
  could
- **THEN** it names the node a launch would wake, and does not wake it
