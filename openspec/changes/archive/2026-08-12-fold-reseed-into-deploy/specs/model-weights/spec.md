## ADDED Requirements

### Requirement: Requesting a re-fetch of stored weights

A deployment request MAY ask for its weights to be fetched even when they are
already stored. When it does, the fetch SHALL be started regardless of what is
in storage, and the reply SHALL report it exactly as it reports a fetch started
because the weights were absent — the caller learns a fetch is running the same
way either way.

A request that does not ask for this SHALL behave as before: the fetch happens
only when the weights are judged absent.

A requested re-fetch SHALL go through the same mechanism as any other fetch, so
it inherits the same guarantees — the same completion markers, the same
protection against a retried launch starting a second fetch, and the same
disposal of whatever ran it.

A re-fetch SHALL overwrite the stored weights in place rather than being kept
somewhere new, so a deployment's weights stay at their derived location and no
stale copy is orphaned.

#### Scenario: A re-fetch is requested for weights already stored

- **WHEN** a deployment asks for a re-fetch and its weights are already stored
- **THEN** a fetch is started, and the reply says so

#### Scenario: Without the request, stored weights are left alone

- **WHEN** a deployment does not ask for a re-fetch and its weights are stored
- **THEN** nothing is fetched and the deployment is stored

#### Scenario: A re-fetch is requested for weights that are absent

- **WHEN** a deployment asks for a re-fetch and its weights are absent
- **THEN** exactly one fetch is started, not two

#### Scenario: A re-fetch that cannot be started refuses the deployment

- **WHEN** a re-fetch is requested and the fetch cannot be started
- **THEN** the deployment is refused and the stored configuration is left
  unchanged
