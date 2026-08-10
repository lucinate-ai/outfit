# engine-metrics Specification

## Purpose

Collecting what a serving host knows about itself, in process: the engine's own
token and request counters scraped from its Prometheus endpoint, plus the
host's GPU, CPU and memory figures.

It is the Go home of collection that first shipped as a TypeScript Lambda
shelling `nvidia-smi`, `vmstat` and `free` onto an instance over SSM — which
worked only for the cloud, and only from outside. Every stat is optional by
design: a host with no source for one omits it rather than erroring, which is
how a machine without `nvidia-smi` reports engine stats and no GPU figures. The
shape is kept value-for-value compatible with what the existing
`outfit remote metrics` formatters render.
## Requirements
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
RAM, token stats, and the engine's last-active time with the idle duration
derived from it), so the existing bar, table, and JSON formats display
in-process metrics without format-specific changes.

The last-active time SHALL be carried as an RFC 3339 timestamp and the idle
duration as whole seconds. The two SHALL travel together: either both are
present or neither is. They SHALL be absent — rather than zero or empty — when
no engine has run, so a consumer can tell "nothing has happened yet" from
"activity happened at the epoch".

Unlike the system and token figures, which describe a running engine, the
last-active pair SHALL be carried whatever the engine's state, including a
stopped or crashed one — the value of keeping the record across a stop is that
it still answers when work last happened.

#### Scenario: Collected metrics render with existing formats

- **WHEN** in-process metrics are rendered
- **THEN** the bar, table, and JSON formats produce output with the same
  structure as remote metrics for the same data

#### Scenario: Activity travels with the stats

- **WHEN** metrics are collected for an engine that has done work
- **THEN** the result carries the last-active timestamp and the seconds since
  it, alongside the token and system figures

#### Scenario: No activity yet is absent, not zero

- **WHEN** metrics are collected and no engine has ever run
- **THEN** the result carries neither a last-active time nor an idle duration

#### Scenario: A stopped engine still reports when it last worked

- **WHEN** metrics are collected for an engine that has been stopped after
  doing work
- **THEN** the result carries the last-active time and idle duration even
  though the running-engine figures are absent

