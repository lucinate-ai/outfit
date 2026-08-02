## MODIFIED Requirements

### Requirement: Starting on demand

Each environment SHALL hold no running instance when idle. A start request names
an environment and SHALL launch that environment's instance, trying each
configured availability zone in turn until one has capacity, since GPU capacity
is not guaranteed in any single zone. The instance SHALL be given the
environment's own stable address (its Elastic IP) so the environment's URL does
not change between launches, and the request SHALL NOT report success until the
model is answering — the caller receives one "ready", never a URL that is not yet
serving. When no capacity can be found anywhere, the response SHALL say so and
SHALL be retryable rather than fatal. One shared set of lifecycle Lambdas SHALL
serve every environment in the account, selecting the instance by the
environment identifier.

#### Scenario: A zone without capacity is not the end of it

- **WHEN** the first availability zone cannot provide the instance type
- **THEN** the remaining zones are tried before reporting failure

#### Scenario: Ready means serving

- **WHEN** a start request returns success
- **THEN** the model is answering requests at the environment's reported address

#### Scenario: No capacity anywhere

- **WHEN** every configured zone is out of capacity
- **THEN** the response says so and indicates the caller may retry shortly

#### Scenario: Starting the right environment

- **WHEN** several environments are deployed and a start names one of them
- **THEN** only that environment's instance is launched, at its own Elastic IP

#### Scenario: Nothing has been deployed

- **WHEN** a start is requested for an environment before it has been deployed
- **THEN** it fails saying what to deploy, rather than launching an instance
  with nothing to serve

### Requirement: Stopping when unused

A running instance SHALL be **terminated**, not stopped, once unused, so that
no storage is billed while an environment is idle. Activity SHALL be judged from
the inference server's own counters, read on the instance, and SHALL account
for both requests in flight and work that started and finished between two
checks. Because the metric names differ per inference engine, the check SHALL
read the names belonging to the engine that is deployed. The scheduled idle
sweep SHALL consider every environment's instance in the account, judging and
terminating each on its own activity, so one shared sweep covers all
environments.

A failed reading SHALL be treated as no activity rather than as activity, so a
wedged server is terminated rather than left running indefinitely.

#### Scenario: Idle for longer than the threshold

- **WHEN** no activity is observed for the configured idle period
- **THEN** the instance is terminated

#### Scenario: A long generation is not mistaken for idleness

- **WHEN** a single request runs across two checks without any request being in
  flight at the moment either is taken
- **THEN** the moved token counters count as activity and the instance is kept

#### Scenario: The server has stopped responding

- **WHEN** the activity reading fails
- **THEN** the instance is still terminated once the idle period passes

#### Scenario: The sweep covers every environment

- **WHEN** several environments have running instances and the idle sweep runs
- **THEN** each is judged on its own activity, and only the idle ones are
  terminated
