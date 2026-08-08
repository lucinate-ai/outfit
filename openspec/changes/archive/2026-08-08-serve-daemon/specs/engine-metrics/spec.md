## Purpose

Define in-process metrics collection: how outfit itself gathers engine
token/request stats and system GPU/CPU/RAM stats from the machine it runs on,
replacing the remote stats Lambda's shell-command collection as the one way an
engine's state is measured — locally, on fleet nodes, and (in a follow-up
change) on cloud instances.

## ADDED Requirements

### Requirement: Engine stats collection

The system SHALL collect token and request statistics from a running engine by
querying the engine's own metrics endpoint over HTTP on the engine's serving
address. The collected statistics SHALL include prompt and generated token
counts and request counts as exposed by the engine. When the engine requires
API-key authentication for its metrics endpoint, the collector SHALL
authenticate with the key the engine was started with.

#### Scenario: Running engine yields token stats

- **WHEN** metrics are collected while a supervised engine is running and
  serving requests
- **THEN** the result includes the engine's prompt token, generated token, and
  request counts

#### Scenario: Unreachable engine does not fail collection

- **WHEN** metrics are collected and the engine's metrics endpoint cannot be
  reached
- **THEN** the result omits engine stats, reports the rest, and the collection
  as a whole does not error

### Requirement: System stats collection

The system SHALL collect system statistics from the host: GPU utilization,
GPU memory used/total, CPU utilization, and RAM used/total. On hosts with
NVIDIA GPUs the GPU figures SHALL be sourced from `nvidia-smi`. The collected
values SHALL use the same units as the existing remote stats pipeline (bytes
for memory, percentages for utilization) so existing rendering applies
unchanged.

#### Scenario: NVIDIA host reports GPU stats

- **WHEN** metrics are collected on a host where `nvidia-smi` is available
- **THEN** the result includes GPU utilization and GPU memory used/total in
  bytes

#### Scenario: CPU and RAM are always attempted

- **WHEN** metrics are collected on any supported host
- **THEN** the result includes CPU utilization and RAM used/total when the
  platform provides them

### Requirement: Graceful platform degradation

When a system stat's source is unavailable on the host (for example
`nvidia-smi` on a machine without NVIDIA tooling, or Linux-only commands on
macOS), the collector SHALL omit that stat and return the remainder, rather
than failing the collection. The absence SHALL be distinguishable from a zero
value in the collected result.

#### Scenario: macOS host lacks GPU stats

- **WHEN** metrics are collected on a macOS host
- **THEN** the result includes engine stats and available CPU/RAM figures,
  omits GPU stats, and reports no error

#### Scenario: Missing command omits only its section

- **WHEN** one system stat source is missing but others are present
- **THEN** only the missing stat is absent from the result

### Requirement: Rendering-compatible stats shape

The collected metrics SHALL be expressible in the same stats shape the
`outfit remote metrics` formatters render (state, runner, model, GPU, CPU,
RAM, token stats), so the existing bar, table, and JSON formats display
in-process metrics without format-specific changes.

#### Scenario: Collected metrics render with existing formats

- **WHEN** in-process metrics are rendered
- **THEN** the bar, table, and JSON formats produce output with the same
  structure as remote metrics for the same data
