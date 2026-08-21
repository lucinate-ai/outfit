## ADDED Requirements

### Requirement: Explicit restart

A user-initiated restart SHALL stop the environment's instance in the same
manner as an explicit pause — without terminating it, so the boot disk and its
synced weights are preserved and the re-wake is fast — and SHALL immediately
start it again. A restart SHALL NOT report success until the model is
answering again. A restart of an environment whose instance is already
stopped, or which has no instance, SHALL behave as a start of that
environment: the existing stopped instance is re-woken, not replaced. The
environment's stable address SHALL NOT change as a result of a restart.

A restart SHALL be able to request a forced stop. A forced restart SHALL stop
the instance without first asking the on-instance daemon to shut the engine
down, so an engine or daemon that does not answer a graceful stop cannot
prevent the instance from being brought down and back up. A restart without
force SHALL stop the engine first, exactly as a pause does (see the
"Engine is stopped before the EC2 instance" requirement).

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

## MODIFIED Requirements

### Requirement: Engine is stopped before the EC2 instance

When the stop Lambda stops a running instance (idle sweep, manual pause, or manual
terminate), it SHALL first send a stop request to the on-instance daemon's control
API (`POST /v1/stop`) to shut down the engine before calling EC2 `StopInstances`.
The daemon's existing signal handler terminates the engine process group, ensuring
the instance exits the `stopping` state promptly regardless of which engine it runs.
The API call SHALL be best-effort: if the daemon is unreachable the Lambda SHALL
proceed with the EC2 stop as normal, rather than failing the operation.

A manual stop request SHALL be able to mark itself as forced. For a forced manual
stop, the Lambda SHALL skip the daemon stop request entirely and proceed directly
to its EC2 call; everything else about that mode — recording the stop time, the
choice between stopping and terminating, and the reply — SHALL be unchanged. The
idle sweep SHALL never be forced: it SHALL always make the best-effort engine stop
first.

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
