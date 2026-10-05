# Spec Delta

## MODIFIED Requirements

### Requirement: System stats collection

The system SHALL collect system statistics from the host: GPU utilization,
GPU memory used/total, CPU utilization, and RAM used/total. On hosts with
NVIDIA GPUs the GPU figures SHALL be sourced from `nvidia-smi`. On macOS
hosts the GPU SHALL be sourced from the host's I/O Kit accelerator service,
which yields GPU utilization and the GPU's name; where that service reports
no memory total or no temperature — the ordinary case on a host with
unified memory — those figures SHALL be absent rather than reported as
zero. The collected values SHALL use the same units as the existing remote
stats pipeline (bytes for memory, percentages for utilization) so existing
rendering applies unchanged.

#### Scenario: NVIDIA host reports GPU stats

- **WHEN** metrics are collected on a host where `nvidia-smi` is available
- **THEN** the result includes GPU utilization and GPU memory used/total in
  bytes

#### Scenario: macOS host reports GPU utilisation

- **WHEN** metrics are collected on a macOS host whose accelerator service
  reports device utilization
- **THEN** the result includes a GPU with that utilization percentage and
  the accelerator's model name, and omits GPU memory and temperature rather
  than reporting them as zero

#### Scenario: CPU and RAM are always attempted

- **WHEN** metrics are collected on any supported host
- **THEN** the result includes CPU utilization and RAM used/total when the
  platform provides them

### Requirement: Graceful platform degradation

When a system stat's source is unavailable on the host (for example
`nvidia-smi` on a machine without NVIDIA tooling, or no accelerator service
at all on a virtualized guest), the collector SHALL omit that stat and
return the remainder, rather than failing the collection. The absence SHALL
be distinguishable from a zero value in the collected result.

A source that is *present and failing* SHALL be distinguished from one that
is absent. Where the collector has an address to query and the query fails,
it SHALL report that failure among the collected errors, naming what it
tried, so a misdirected or broken collector is visible rather than
presenting as an engine that has simply served nothing. An absent source
SHALL remain silent: reporting the routine absence of a source as an error
would bury the failures worth seeing.

#### Scenario: macOS host lacks GPU stats

- **WHEN** metrics are collected on a macOS host that names no I/O Kit
  accelerator service, such as a virtualized guest
- **THEN** the result includes engine stats and available CPU/RAM figures,
  omits GPU stats, and reports no error

#### Scenario: Missing command omits only its section

- **WHEN** one system stat source is missing but others are present
- **THEN** only the missing stat is absent from the result

#### Scenario: A failing scrape is reported, not hidden

- **WHEN** the collector has an engine address to query and the query fails
- **THEN** the result omits the engine's counters and reports an error naming
  the address it tried

#### Scenario: An engine with no metrics endpoint stays silent

- **WHEN** the engine exposes no metrics endpoint, so there is no address to
  query
- **THEN** the result omits the engine's counters and reports no error

## ADDED Requirements

### Requirement: Absent GPU figures render as absent

A GPU reading that carries no memory total or no temperature SHALL render
without those figures, rather than as zeros: a resource-series view SHALL
draw no memory series for a GPU whose current reading and retained history
both report no memory total, and a table GPU line SHALL omit its memory and
temperature segments when the reading carries none. The GPU's utilization
SHALL still render. This keeps the shape's own rule — an absent figure is
not a zero — true of the rendered output as it is of the collected one.

#### Scenario: A utilisation-only GPU draws one series

- **WHEN** a bar or gauge view renders a GPU whose readings never carry a
  memory total
- **THEN** the output includes the GPU utilization series and no GPU memory
  series

#### Scenario: A GPU with memory still draws both series

- **WHEN** a bar or gauge view renders a GPU whose current reading or any
  retained sample carries a memory total
- **THEN** the output includes both the utilization and the memory series,
  as before

#### Scenario: Table omits the segments a reading lacks

- **WHEN** the table format renders a GPU line whose reading carries no
  memory total and no temperature
- **THEN** the line shows the GPU's name and utilization with no `mem=` and
  no `temp=` segment, and a GPU reading that does carry them shows them as
  before
