## ADDED Requirements

### Requirement: Engine is stopped before the EC2 instance

When the stop Lambda stops a running instance (idle sweep, manual pause, or manual terminate), it SHALL first send a stop request to the on-instance daemon's control API (`POST /v1/stop`) to shut down the engine before calling EC2 `StopInstances`. The daemon's existing signal handler terminates the engine process group, ensuring the instance exits the `stopping` state promptly regardless of which engine it runs. The API call SHALL be best-effort: if the daemon is unreachable the Lambda SHALL proceed with the EC2 stop as normal, rather than failing the operation.

#### Scenario: Normal graceful stop

- **WHEN** the stop Lambda needs to stop a running instance
- **THEN** it first sends `POST /v1/stop` to the daemon, and only then calls EC2 `StopInstances`

#### Scenario: Daemon is unreachable

- **WHEN** the stop request to the daemon fails or times out
- **THEN** the Lambda still calls EC2 `StopInstances` and does not treat it as an error

#### Scenario: Engine-neutral stop

- **WHEN** the instance runs any supported engine (llama.cpp, vLLM, or a future runner)
- **THEN** the stop mechanism works via the daemon API without engine-specific logic in the Lambda
