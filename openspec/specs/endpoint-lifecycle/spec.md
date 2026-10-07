# Endpoint Lifecycle Specification

## Purpose

Define when the cloud endpoint's instance exists — how it is started on
demand, how it is judged to be still wanted, and the bounds that decide
when it is torn down.

## Requirements

### Requirement: Starting on demand

Each environment SHALL hold no running instance when idle. A start request names
an environment and SHALL launch that environment's instance, trying each
configured availability zone in turn until one has capacity, since GPU capacity
is not guaranteed in any single zone. A launch SHALL provision the instance's
root volume: a gp3 volume of the AMI's own root size, with provisioned
throughput at the volume's ceiling — the size is read from the AMI's own root
mapping, because a launch's block device mapping replaces the AMI's rather
than extending it — and IOPS provisioned at four times that throughput,
which is the minimum EC2 allows for it (gp3 caps throughput at 0.25 MiB/s
per provisioned IOP). The
instance SHALL be given the environment's own stable address (its Elastic IP)
so the environment's URL does not change between launches, and the request
SHALL NOT report success until the model is answering — the caller receives
one "ready", never a URL that is not yet serving. When no capacity can be
found anywhere, the response SHALL say so, SHALL name the instance type it was
trying to launch, and SHALL be retryable rather than
fatal. One shared set of lifecycle Lambdas SHALL serve every environment in
the account, selecting the instance by the environment identifier.

A launch SHALL use the instance type the environment's deploy config names,
when it names one, and the control plane's default type otherwise. The type is
a property of the environment's deployment, read from the same stored deploy
config the start already reads for what to serve, so changing it is a deploy,
not a start. Because EC2 cannot change the type of an existing instance, a
stored type takes effect on a fresh launch only: a re-wake of a stopped
instance SHALL keep the type it was originally launched with, and a changed
type applies once the instance has been terminated — by an explicit stop or
the idle sweep — and relaunched.

Before launching or re-waking the instance, a start SHALL check that the
environment's weights are present in shared storage, judged by the same
completeness record the seeding writes rather than by the absence of an error.
While the weights are absent, a start SHALL NOT launch or re-wake the
instance, and SHALL report a retryable state that names the seed producing
the weights, so a caller can wait for the seed or follow it separately rather
than receiving an instance that boots against an incomplete prefix. When no
seed is running for those weights, the start SHALL start one, so that the
weights are produced rather than the start failing; a seed whose compute has
ceased — failed, stopped or reaped — does not count as running, and its
re-run follows the same identity and convergence rules as any other seed
request. A start that would exceed the cap on seeds in flight SHALL NOT start
another seed, and SHALL report the retryable state until a later start can.

The control plane SHALL request the engine's start on every path — a fresh
launch and a re-wake alike — once the instance's daemon answers its control
API, which on a fresh boot is the signal that the boot has stored the deploy
config; the boot's own user data SHALL NOT start the engine. The start SHALL
carry the deploy config as its body, so it always names the exact config the
daemon runs.

#### Scenario: A zone without capacity is not the end of it

- **WHEN** the first availability zone cannot provide the instance type
- **THEN** the remaining zones are tried before reporting failure

#### Scenario: Ready means serving

- **WHEN** a start request returns success
- **THEN** the model is answering requests at the environment's reported address

#### Scenario: No capacity anywhere

- **WHEN** every configured zone is out of capacity
- **THEN** the response says so, names the instance type it was trying, and
  indicates the caller may retry shortly

#### Scenario: Starting the right environment

- **WHEN** several environments are deployed and a start names one of them
- **THEN** only that environment's instance is launched, at its own Elastic IP

#### Scenario: Nothing has been deployed

- **WHEN** a start is requested for an environment before it has been deployed
- **THEN** it fails saying what to deploy, rather than launching an instance
  with nothing to serve

#### Scenario: A launch provisions the root volume

- **WHEN** a start launches a fresh instance
- **THEN** its root volume is the AMI's gp3 root, at the AMI's own size, with
  provisioned throughput at the volume's ceiling and provisioned IOPS at four
  times that throughput

#### Scenario: A launch uses the environment's stored instance type

- **WHEN** an environment's deploy config names an instance type and a start
  launches a fresh instance for it
- **THEN** the instance is launched as that type

#### Scenario: A launch with no stored type uses the control plane default

- **WHEN** an environment's deploy config names no instance type and a start
  launches a fresh instance for it
- **THEN** the instance is launched as the control plane's default type

#### Scenario: A re-wake keeps the instance's original type

- **WHEN** an environment's instance is stopped, its deploy config's instance
  type is changed, and a start re-wakes the stopped instance
- **THEN** the instance comes back as the type it was originally launched
  with, because a stopped instance is not resized

#### Scenario: A changed type applies after the instance is terminated

- **WHEN** an environment's deploy config instance type is changed, its
  instance is terminated, and a later start launches it
- **THEN** the fresh instance is launched as the new type

#### Scenario: The control plane starts the engine on a fresh boot

- **WHEN** a fresh instance's daemon first answers its control API
- **THEN** the start request itself issues the engine's start, with the
  deploy config as its body, and reports ready only once the model answers —
  the boot started no engine

#### Scenario: A start while the weights are seeding launches nothing

- **WHEN** a start is requested for an environment whose weights are still
  being seeded
- **THEN** no instance of the environment is launched or re-woken, and the
  response is retryable and names the running seed

#### Scenario: A start with absent weights and no running seed starts one

- **WHEN** a start is requested for an environment whose weights are absent
  and no seed is running for them
- **THEN** a seed for those weights is started, no instance is launched, and
  the response is the same retryable state naming the seed

#### Scenario: Two starts for the same weights share one seed

- **WHEN** starts for two environments naming the same weights arrive while no
  seed is running
- **THEN** one seed is started, and both responses name it

#### Scenario: A ceased seed does not block a start

- **WHEN** the only seed for the weights has ceased — failed, stopped or
  reaped — and a start is requested
- **THEN** the weights are treated as absent: a new seed is started and the
  response is the retryable state, never a launch against the partial prefix
  the failed seed left behind

#### Scenario: A start that would exceed the seed cap waits

- **WHEN** the cap on seeds in flight is reached and a start needs to start a
  seed for absent weights
- **THEN** no further seed is started, and the response is retryable until a
  later start can start the seed

#### Scenario: A start proceeds once the weights are present

- **WHEN** a start has been reported in the seeding state and the seed has
  since finished, leaving the weights present
- **THEN** the next start launches the environment's instance and proceeds to
  ready as usual

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
   tag is set from the CLI via `spinloop cloud keep DURATION` or
   `spinloop cloud start --keep DURATION`, which compute an absolute deadline
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

- **WHEN** the user runs `spinloop cloud keep 4h` on a running instance
- **THEN** the `Retain-Until` tag is set to 4 hours from now, and the idle
  sweep defers automatic termination until that time

### Requirement: Explicit pause

A user-initiated pause SHALL stop the instance without terminating it, preserving boot disk and weights for fast re-wake. The instance SHALL remain stoppable and re-wakable via start, and retain-until overrides SHALL still apply.

#### Scenario: Pause stops without terminating

- **WHEN** user runs `spinloop cloud pause` for a running environment
- **THEN** the instance is stopped, not terminated, and the environment's URL is retained

#### Scenario: Pause is distinct from stop

- **WHEN** user runs `spinloop cloud stop`
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

### Requirement: Concurrent starts of one environment are serialised

At most one start SHALL be working on an environment at a time, whoever calls it: the CLI, a gateway, a scheduled start, or any other client of the start endpoint. A start SHALL take the environment's lock before it checks the weights, looks up the instance, launches or re-wakes it, and SHALL release the lock on every way the start can end: ready, a retryable reply, a failure reply, or an error. A start that finds the lock held SHALL NOT check the weights, look up, launch or re-wake anything. It SHALL reply with a retryable state that says another start is in progress and when to retry. A start made after the holder has finished SHALL find the instance the holder started and report it as it would for any existing instance, so two starts never produce two launches.

The lock SHALL be per environment: a start for one environment SHALL NOT wait for, or be refused because of, a start for another. A read of an environment's state SHALL NOT take the lock.

#### Scenario: Two starts arrive together

- **WHEN** two starts for the same environment reach the control plane at the same moment and the environment has no instance
- **THEN** exactly one instance is launched, and the start that did not take the lock replies with a retryable "another start is in progress" state

#### Scenario: The refused start is retried

- **WHEN** a start refused because the lock was held is retried after the first start has finished
- **THEN** it finds the first start's instance and reports it, and launches nothing

#### Scenario: Different environments do not wait for each other

- **WHEN** a start for environment `a` holds its lock and a start for environment `b` arrives
- **THEN** the start for `b` proceeds without waiting

#### Scenario: Every ending releases the lock

- **WHEN** a start ends because the weights are absent, no capacity was found, the instance went into a terminal state, the boot failed, the deadline passed, or an unexpected error was raised
- **THEN** the lock is released, and the next start for that environment is not refused because of it

#### Scenario: Status does not take the lock

- **WHEN** a start holds the environment's lock and the state of the environment is read
- **THEN** the read is answered normally

### Requirement: Status shows a start in progress

The state of an environment SHALL show that a start is in progress while an unexpired start lock exists, whichever client began the start. While the lock is held and the instance is absent, stopped or still pending, the reported state SHALL be `starting`. In every reply while the lock is held, including when the instance is already running and its model is still loading, the reply SHALL carry `start_in_progress: true`; it SHALL carry `start_in_progress: false` otherwise. A lock that has expired SHALL NOT count. A failure to read the lock SHALL NOT fail the read of the environment's state: the reply is given as if no start were in progress.

A start that is waiting for GPU capacity, or for any other retry, holds no lock between attempts and is not shown as in progress.

#### Scenario: A second client sees a start another client began

- **WHEN** one client has started an environment that has no instance yet and a second client reads the environment's state
- **THEN** the state is `starting` and `start_in_progress` is true

#### Scenario: A loading model is still reported as a start

- **WHEN** the instance is running and its model is still loading under a start that holds the lock
- **THEN** the state is the instance's own (`running`), healthy is false, and `start_in_progress` is true

#### Scenario: No start, no flag

- **WHEN** no start holds the lock
- **THEN** the state is the instance's own and `start_in_progress` is false

#### Scenario: An abandoned lock is not a start in progress

- **WHEN** the lock's expiry has passed
- **THEN** the state read does not report a start in progress

#### Scenario: A start waiting for capacity is not in progress

- **WHEN** a start found no capacity and is waiting to retry
- **THEN** the environment is not reported as starting between attempts

#### Scenario: An unreadable lock does not fail the read

- **WHEN** the lock cannot be read
- **THEN** the state is still returned, without a start in progress

### Requirement: An abandoned lock expires

A lock SHALL record when it expires, no earlier than the moment the start holding it would itself run out of time. A start that finds an expired lock SHALL take it over and proceed as if it had found none. A lock that has not expired SHALL NOT be taken over, so a start that is still legitimately running keeps its lock for as long as it could run.

#### Scenario: A killed start does not block the environment for ever

- **WHEN** a start took the lock and its Lambda was killed before releasing it, and its time limit has since passed
- **THEN** the next start for that environment takes over the lock and proceeds

#### Scenario: A running start keeps its lock

- **WHEN** a start has held the lock for longer than a typical wake but not past its time limit
- **THEN** another start does not take the lock over

### Requirement: A stop during a start ends the start

Once a start has launched the instance, issued the command to start it, or seen it running, and then sees the instance stopped, stopping, shutting down or terminated, the start SHALL end at once with a retryable reply that names the state it saw, and SHALL release the lock. It SHALL NOT keep polling an instance that is not coming up until its time limit. The reply SHALL be retryable so that a client still waiting on its start can ask again, which re-wakes the instance or launches a fresh one; a stop does not wait for, or take, the start's lock.

#### Scenario: The instance is stopped after the start command

- **WHEN** a start has issued the command to start a stopped instance and the instance is then stopped by a pause, a scheduled stop or the idle sweep
- **THEN** the start ends promptly with a retryable reply naming the state, and the lock is released

#### Scenario: The instance is terminated while the engine loads

- **WHEN** an instance is terminated after it has reached running, while the start waits for the model to answer
- **THEN** the start ends promptly with a retryable reply naming the state, and the lock is released

#### Scenario: A stop is never refused because a start is running

- **WHEN** a start holds the environment's lock and a pause, stop or scheduled stop for the environment arrives
- **THEN** the stop is carried out and is not refused or delayed by the lock

### Requirement: A start does not proceed without its lock

When the control plane cannot tell whether the lock is held, because the lock could not be read or written, the start SHALL NOT proceed to launch or re-wake. It SHALL reply with a retryable state and SHALL log the cause.

#### Scenario: The lock cannot be written

- **WHEN** a start cannot create the lock for a reason other than the lock already existing
- **THEN** nothing is launched, and the reply is retryable
