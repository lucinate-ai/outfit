# Remote Stats Specification

## Purpose

Define the `outfit remote stats` command: reading token usage, resource consumption, and GPU information from a running remote inference instance.

## Requirements

### Requirement: Stats subcommand

The system SHALL provide a `stats` subcommand (`outfit remote stats`) that reports the current state of a remote inference instance. It SHALL accept the same Outfit resolution as `start`, `stop`, and `deploy` — an optional positional Outfit path, defaulting to `./Outfit` when present — and SHALL require the Outfit to name a `REMOTE` environment.

#### Scenario: Stats with a running instance

- **WHEN** the user runs `outfit remote stats` with a running instance
- **THEN** the command reports the instance state, runner, model, GPU info, CPU/RAM usage, token counts, and request counts

#### Scenario: Stats with a stopped instance

- **WHEN** the user runs `outfit remote stats` and the instance is stopped
- **THEN** the command reports `state: stopped` and no metrics

#### Scenario: Stats resolves the Outfit

- **WHEN** the user runs `outfit remote stats` in a directory with an `Outfit` that has a `REMOTE` instruction
- **THEN** the command uses that Outfit's remote environment without an explicit path argument

#### Scenario: Stats with explicit Outfit path

- **WHEN** the user runs `outfit remote stats ./some/Outfit`
- **THEN** the command uses that Outfit's `REMOTE` environment

### Requirement: Token and request metrics

The stats report SHALL include cumulative token and request metrics read from the inference server's `/metrics` endpoint. For both llama.cpp and vLLM runners, it SHALL report: prompt tokens processed, output (predicted) tokens generated, requests completed, and requests currently in-flight. The metric names SHALL be runner-aware, reusing the same runner-specific parsing used by the idle detection system.

#### Scenario: Token counts for vLLM

- **WHEN** the instance runs vLLM and stats is queried
- **THEN** prompt tokens, output tokens, request count, and in-flight requests are reported

#### Scenario: Token counts for llama.cpp

- **WHEN** the instance runs llama.cpp and stats is queried
- **THEN** prompt tokens, output tokens, request count, and in-flight requests are reported

### Requirement: GPU information

The stats report SHALL include GPU hardware and utilization information from `nvidia-smi`. For each GPU in the instance, it SHALL show the GPU index, model name, total memory, current utilization percentage, and current memory usage. When the instance has multiple GPUs, it SHALL also show an aggregate row with average utilization and summed memory.

#### Scenario: Single GPU

- **WHEN** the instance has one GPU
- **THEN** the report shows that GPU's model, utilization, and memory usage

#### Scenario: Multiple GPUs

- **WHEN** the instance has multiple GPUs
- **THEN** the report shows each GPU's stats individually plus an aggregate row with average utilization and summed memory

### Requirement: CPU and RAM information

The stats report SHALL include current CPU utilization (percentage) and RAM usage (used out of total) read from the instance's system metrics. The CPU utilization SHALL be a snapshot, not an average over time.

#### Scenario: Resource usage is reported

- **WHEN** the instance is running and stats is queried
- **THEN** CPU utilization percentage and RAM usage (used/total) are displayed

### Requirement: Instance metadata

The stats report SHALL include the environment name, runner type, served model identifier, and instance uptime (time since launch).

#### Scenario: Metadata is shown

- **WHEN** the instance is running and stats is queried
- **THEN** the environment name, runner, model, and uptime are displayed

### Requirement: Optional cost estimation

When the user passes `--cost`, the stats report SHALL include an estimated on-demand cost for the current running session. The cost SHALL be computed from the instance type's on-demand price (fetched from the AWS Price List API for the deployed region) multiplied by the elapsed time since launch. Without `--cost`, no price lookup is performed and no cost is shown.

#### Scenario: Cost is shown with flag

- **WHEN** the user runs `outfit remote stats --cost` with a running instance
- **THEN** the report includes the estimated cost for the current session

#### Scenario: Cost is not shown by default

- **WHEN** the user runs `outfit remote stats` without `--cost`
- **THEN** the report does not include a cost line

### Requirement: Tabular display

The stats output SHALL be a tab-separated key-value table, one line per metric, with the key column left-aligned and values right of it. Progress and error messages SHALL go to standard error.

#### Scenario: Clean output

- **WHEN** the command succeeds
- **THEN** standard output contains only the stats table with no progress or debug lines