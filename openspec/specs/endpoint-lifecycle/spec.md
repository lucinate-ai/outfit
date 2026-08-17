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

### Requirement: Explicit pause

A user-initiated pause SHALL stop the instance without terminating it, preserving boot disk and weights for fast re-wake. The instance SHALL remain stoppable and re-wakable via start, and retain-until overrides SHALL still apply.

#### Scenario: Pause stops without terminating

- **WHEN** user runs `outfit remote pause` for a running environment
- **THEN** the instance is stopped, not terminated, and the environment's URL is retained

#### Scenario: Pause is distinct from stop

- **WHEN** user runs `outfit remote stop`
- **THEN** the instance is terminated immediately

### Requirement: Engine is stopped before the EC2 instance

When the stop Lambda stops a running instance (idle sweep, manual pause, or manual terminate), it SHALL first send a stop request to the on-instance daemon's control API (`POST /v1/stop`) to shut down the engine before calling EC2 `StopInstances`. The daemon's existing signal handler terminates the engine process group, ensuring the instance exits the `stopping` state promptly regardless of which engine it runs. The API call SHALL be best-effort: if the daemon is unreachable the Lambda SHALL proceed with the EC2 stop as normal, rather than failing the operation.

A manual stop request SHALL be able to mark itself as forced. For a forced manual stop, the Lambda SHALL skip the daemon stop request entirely and proceed directly to its EC2 call; everything else about that mode — recording the stop time, the choice between stopping and terminating, and the reply — SHALL be unchanged. The idle sweep SHALL never be forced: it SHALL always make the best-effort engine stop first.

#### Scenario: Normal graceful stop

- **WHEN** the stop Lambda needs to stop a running instance
- **THEN** it first sends `POST /v1/stop` to the daemon, and only then calls EC2 `StopInstances`

#### Scenario: Daemon is unreachable

- **WHEN** the stop request to the daemon fails or times out
- **THEN** the Lambda still calls EC2 `StopInstances` and does not treat it as an error

#### Scenario: A forced manual stop skips the engine

- **WHEN** a manual stop is marked forced
- **THEN** no stop request is sent to the daemon, and the EC2 stop or terminate proceeds without it

#### Scenario: The sweep is never forced

- **WHEN** the scheduled idle sweep stops an idle instance
- **THEN** it makes the best-effort engine stop first, as it always has

#### Scenario: Engine-neutral stop

- **WHEN** an unforced stop stops an instance running any supported engine (llama.cpp, vLLM, or a future runner)
- **THEN** the stop mechanism works via the daemon API without engine-specific logic in the Lambda

### Requirement: Explicit restart

A user-initiated restart SHALL stop the environment's instance in the same manner as an explicit pause — without terminating it, so the boot disk and its synced weights are preserved and the re-wake is fast — and SHALL immediately start it again. A restart SHALL NOT report success until the model is answering again. A restart of an environment whose instance is already stopped, or which has no instance, SHALL behave as a start of that environment: the existing stopped instance is re-woken, not replaced. The environment's stable address SHALL NOT change as a result of a restart.

A restart SHALL be able to request a forced stop. A forced restart SHALL stop the instance without first asking the on-instance daemon to shut the engine down, so an engine or daemon that does not answer a graceful stop cannot prevent the instance from being brought down and back up. A restart without force SHALL stop the engine first, exactly as a pause does (see the "Engine is stopped before the EC2 instance" requirement).

#### Scenario: Restart a running endpoint

- **WHEN** the user restarts an environment whose instance is running
- **THEN** the instance is stopped, not terminated, is started again, and the restart reports success only once the model is answering again, at the environment's unchanged address

#### Scenario: Restart is not a fresh launch

- **WHEN** the user restarts an environment whose instance previously booted and synced its weights
- **THEN** that same instance is re-woken rather than a new one being launched, and its boot disk and weights are reused

#### Scenario: Restarting a stopped endpoint starts it

- **WHEN** the user restarts an environment whose instance is already stopped
- **THEN** the instance is re-woken rather than replaced, and the restart reports success only once the model is answering again

#### Scenario: A forced restart does not ask the engine first

- **WHEN** the user restarts with force against an engine or daemon that does not answer a graceful stop
- **THEN** the instance is still stopped and re-woken, and no engine stop request was sent first

