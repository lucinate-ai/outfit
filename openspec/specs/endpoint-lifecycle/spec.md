# Endpoint Lifecycle Specification

## Purpose

Define when the remote endpoint's instance exists — how it is started on
demand, how it is judged to be still wanted, and the bounds that decide
when it is torn down.

## Requirements

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
no storage is billed while an environment is idle. Activity SHALL be judged
from the inference server's own counters, read on the instance, and SHALL
account for both requests in flight and work that started and finished between
two readings. Because the metric names differ per inference engine, the check
SHALL read the names belonging to the engine that is deployed.

Those counters SHALL be sampled continuously on the instance itself, at an
interval short relative to the idle threshold, and the scheduled sweep SHALL
judge idleness from the resulting activity history rather than from a single
reading taken at the moment it runs. A quiet gap between requests that happens
to coincide with a sweep SHALL NOT be read as idleness. The scheduled idle
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

#### Scenario: A lull at sweep time is not idleness

- **WHEN** an endpoint is serving steady traffic but happens to have nothing in
  flight and no counter movement at the instant the scheduled sweep runs
- **THEN** the activity observed between sweeps keeps the instance alive

#### Scenario: The server has stopped responding

- **WHEN** the activity reading fails
- **THEN** the instance is still terminated once the idle period passes

#### Scenario: The sweep covers every environment

- **WHEN** several environments have running instances and the idle sweep runs
- **THEN** each is judged on its own activity, and only the idle ones are
  terminated
### Requirement: Bounds on a running instance

The following SHALL take precedence over one another in this order, so that the
stronger guarantee always wins:

1. A **retention override** — an instance marked to be retained until a stated
   time SHALL NOT be terminated automatically before it, for any reason.
2. A **maximum runtime** — an instance SHALL be terminated once it has run
   longer than the configured maximum, **even while requests are in flight**,
   as a backstop against a session nobody is watching.
3. A **grace period** — an instance SHALL NOT be terminated for idleness within
   the configured period after launch, which covers loading the model.

A manual stop SHALL take effect immediately regardless of all three.

#### Scenario: Retention beats the maximum runtime

- **WHEN** an instance is marked retained until a future time and has also
  exceeded the maximum runtime
- **THEN** it is kept, and the reason given is the retention

#### Scenario: The maximum runtime beats activity

- **WHEN** an instance has run longer than the maximum and requests are still
  in flight
- **THEN** it is terminated

#### Scenario: Loading the model is not idleness

- **WHEN** an instance is still within the grace period and reports no activity
- **THEN** it is kept

#### Scenario: A manual stop is not delayed

- **WHEN** a stop is requested for a retained instance
- **THEN** it is terminated
