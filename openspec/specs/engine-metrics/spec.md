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
`spinloop cloud metrics` formatters render.

## Requirements

### Requirement: Engine stats collection

The system SHALL collect token and request statistics from a running engine by
querying the engine's own metrics endpoint over HTTP on the engine's serving
address. The collected statistics SHALL include the prompt and generated token
counts as the engine exposes them, and the engine's cumulative request count
where the engine exposes one. A request count the engine's metrics do not
expose SHALL be absent from the collected statistics rather than reported as
zero: every stat is optional by design, and a figure no source produced is not
a zero. When the engine requires API-key authentication for its metrics
endpoint, the collector SHALL authenticate with the key the engine was
started with.

The serving address SHALL be the one the engine was actually told to bind:
when the engine's command states a host or port, those SHALL determine where
the collector looks, in preference to any address configured elsewhere or
compiled in as that engine's default. An engine started on a non-default port
is the ordinary case, not an exception — a deployment that describes what to
serve without stating a base URL still binds wherever its arguments say.

A bind naming every interface SHALL be read as loopback for collection
purposes. Collection is always to an engine on the same host, and a wildcard
is not an address to dial.

Where the engine's command states no address at all, the collector SHALL fall
back to a configured base URL, and failing that to the engine's default.

#### Scenario: Running engine yields token stats

- **WHEN** metrics are collected while a supervised engine is running and
  serving requests
- **THEN** the result includes the engine's prompt token and generated token
  counts, and its request count where the engine's metrics expose one

#### Scenario: An engine without a request counter omits the figure

- **WHEN** metrics are collected from an engine whose metrics expose no
  cumulative request count
- **THEN** the result omits the request count rather than reporting zero, and
  the figures the engine does expose are present

#### Scenario: The engine's own arguments locate it

- **WHEN** an engine is started on a port other than its default, and no base
  URL is configured
- **THEN** the collector queries the port the engine was given, not the
  engine's default

#### Scenario: A wildcard bind is collected over loopback

- **WHEN** an engine is started bound to every interface
- **THEN** the collector queries it on loopback

#### Scenario: Unreachable engine does not fail collection

- **WHEN** metrics are collected and the engine's metrics endpoint cannot be
  reached
- **THEN** the result omits engine stats, reports the rest, and the collection
  as a whole does not error

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

### Requirement: Rendering-compatible stats shape

The collected metrics SHALL be expressible in the same stats shape the
`spinloop cloud metrics` formatters render (state, runner, model, GPU, CPU,
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
