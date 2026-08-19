## MODIFIED Requirements

### Requirement: Bounds on a running instance

The following SHALL take precedence over one another in this order, so that the
stronger guarantee always wins:

1. A **retention override** — an instance marked to be retained until a stated
   time SHALL NOT be terminated automatically before it, for any reason. The
   tag is set from the CLI via `outfit remote keep DURATION` or
   `outfit remote start --keep DURATION`, which compute an absolute deadline
   from the provided duration and apply it as the `Retain-Until` EC2 tag on the
   instance. The idle sweep reads this tag and defers automatic termination
   until the deadline passes.
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

#### Scenario: Setting retention from the CLI

- **WHEN** the user runs `outfit remote keep 4h` on a running instance
- **THEN** the `Retain-Until` tag is set to 4 hours from now, and the idle
  sweep defers automatic termination until that time
