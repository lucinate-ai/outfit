## ADDED Requirements

### Requirement: Starting on demand

The endpoint SHALL hold no running instance when idle. A start request SHALL
launch one, and SHALL try each configured availability zone in turn until one
has capacity, since GPU capacity is not guaranteed in any single zone. The
instance SHALL be given a stable address so the endpoint's URL does not change
between launches, and the request SHALL NOT report success until the model is
answering — the caller receives one "ready", never a URL that is not yet
serving. When no capacity can be found anywhere, the response SHALL say so and
SHALL be retryable rather than fatal.

#### Scenario: A zone without capacity is not the end of it

- **WHEN** the first availability zone cannot provide the instance type
- **THEN** the remaining zones are tried before reporting failure

#### Scenario: Ready means serving

- **WHEN** a start request returns success
- **THEN** the model is answering requests at the reported address

#### Scenario: No capacity anywhere

- **WHEN** every configured zone is out of capacity
- **THEN** the response says so and indicates the caller may retry shortly

#### Scenario: Nothing has been deployed

- **WHEN** a start is requested before any configuration has been deployed
- **THEN** it fails saying what to deploy, rather than launching an instance
  with nothing to serve

### Requirement: Stopping when unused

A running instance SHALL be **terminated**, not stopped, once unused, so that
no storage is billed while the endpoint is idle. Activity SHALL be judged from
the inference server's own counters, read on the instance, and SHALL account
for both requests in flight and work that started and finished between two
checks. Because the metric names differ per inference engine, the check SHALL
read the names belonging to the engine that is deployed.

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
