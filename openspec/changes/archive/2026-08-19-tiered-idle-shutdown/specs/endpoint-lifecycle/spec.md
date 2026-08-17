## MODIFIED Requirements

### Requirement: Stopping when unused

A running instance SHALL be **stopped**, not terminated, once unused, so that the boot disk and synced weights are preserved for fast re-wake. After a further configured period in the stopped state without a start request, the instance SHALL be **terminated** to free storage. Activity SHALL be judged from the inference server's own counters, read on the instance, and SHALL account for both requests in flight and work that started and finished between two readings. Because the metric names differ per inference engine, the check SHALL read the names belonging to the engine that is deployed.

Those counters SHALL be sampled continuously on the instance itself, at an interval short relative to the idle threshold, and the scheduled sweep SHALL judge idleness from the resulting activity history rather than from a single reading taken at the moment it runs. A quiet gap between requests that happens to coincide with a sweep SHALL NOT be read as idleness. The scheduled idle sweep SHALL consider every environment's instance in the account, judging and stopping each on its own activity, so one shared sweep covers all environments.

A failed reading SHALL be treated as no activity rather than as activity, so a wedged server is stopped rather than left running indefinitely.

#### Scenario: Idle for longer than the threshold

- **WHEN** no activity is observed for the configured idle period
- **THEN** the instance is stopped, not terminated

#### Scenario: A long generation is not mistaken for idleness

- **WHEN** a single request runs across two checks without any request being in flight at the moment either is taken
- **THEN** the moved token counters count as activity and the instance is kept

#### Scenario: A lull at sweep time is not idleness

- **WHEN** an endpoint is serving steady traffic but happens to have nothing in flight and no counter movement at the instant the scheduled sweep runs
- **THEN** the activity observed between sweeps keeps the instance alive

#### Scenario: The server has stopped responding

- **WHEN** the activity reading fails
- **THEN** the instance is still stopped once the idle period passes

#### Scenario: The sweep covers every environment

- **WHEN** several environments have running instances and the idle sweep runs
- **THEN** each is judged on its own activity, and only the idle ones are stopped

#### Scenario: Stopped instance is terminated after further idle

- **WHEN** an instance has been stopped for longer than the stop-retention period without a start request
- **THEN** the instance is terminated

#### Scenario: Start re-wakes a stopped instance

- **WHEN** a start request is made for an environment whose instance is stopped
- **THEN** the existing instance is started, not replaced, and the environment's URL remains unchanged

## ADDED Requirements

### Requirement: Explicit pause

A user-initiated pause SHALL stop the instance without terminating it, preserving boot disk and weights for fast re-wake. The instance SHALL remain stoppable and re-wakable via start, and retain-until overrides SHALL still apply.

#### Scenario: Pause stops without terminating

- **WHEN** user runs `outfit remote pause` for a running environment
- **THEN** the instance is stopped, not terminated, and the environment's URL is retained

#### Scenario: Pause is distinct from stop

- **WHEN** user runs `outfit remote stop`
- **THEN** the instance is terminated immediately
