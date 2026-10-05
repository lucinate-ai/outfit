# Cloud Keep Specification

## Purpose

Lets the user set a retention deadline on a cloud inference instance so the idle sweep keeps it alive for a known period — useful for overnight debugging, long-running tasks, or any scenario where the user needs a guaranteed minimum runtime.
## Requirements
### Requirement: The keep subcommand sets the retention tag

The system SHALL provide a `keep` subcommand under `spinloop cloud` that accepts a duration argument (e.g. `4h`, `60m`) and sets the `Retain-Until` EC2 tag on the environment's instance to the current time plus the duration. The duration SHALL be parsed using Go duration syntax (supporting `h`, `m`, `s` units). When successful, the command SHALL report the deadline it set.

#### Scenario: Setting a 4-hour retention

- **WHEN** the user runs `spinloop cloud keep 4h` against a running environment
- **THEN** the instance's `Retain-Until` tag is set to 4 hours from now and the command reports the deadline

#### Scenario: Invalid duration format

- **WHEN** the user runs `spinloop cloud keep 4hours` with an unsupported unit
- **THEN** the command fails with a parsing error

#### Scenario: No instance to tag

- **WHEN** the user runs `spinloop cloud keep 4h` and the environment has no running instance
- **THEN** the command fails saying no instance is found

### Requirement: Start accepts a keep flag

`spinloop cloud start` SHALL accept a `--keep DURATION` flag that sets the `Retain-Until` tag on the instance at wake time. The instance SHALL be tagged before the wake completes, so the idle sweep does not terminate it during the retention period. When the flag is given, the command SHALL report the deadline alongside the usual start output.

#### Scenario: Starting with a retention period

- **WHEN** the user runs `spinloop cloud start --keep 2h`
- **THEN** the instance is started and the `Retain-Until` tag is set to 2 hours from now

#### Scenario: Start with keep reuses existing instance

- **WHEN** the user runs `spinloop cloud start --keep 1h` and the instance is already running
- **THEN** the instance is left as-is and the `Retain-Until` tag is set (or updated) to 1 hour from now

### Requirement: Status reports the active retention

When the instance's `Retain-Until` tag is set, `spinloop cloud status` SHALL report the deadline as a "retain until" line alongside the state and health it reports already. When the tag is absent, the line SHALL be omitted.

#### Scenario: A retained instance reports its deadline

- **WHEN** the user runs `spinloop cloud status` and the instance has a `Retain-Until` tag
- **THEN** the output includes a "retain until" line showing the absolute deadline

#### Scenario: A non-retained instance omits the line

- **WHEN** the user runs `spinloop cloud status` and the instance has no `Retain-Until` tag
- **THEN** the output contains no "retain until" line

### Requirement: The keep command selects its environment

The `keep` subcommand SHALL select which environment's instance to tag using the same rule as the other `cloud` subcommands: the `--env <name>` flag naming a registered environment, or the per-user default environment.

#### Scenario: Keep names the environment with the flag

- **WHEN** the user runs `spinloop cloud keep 2h --env dev-2`
- **THEN** the `dev-2` environment's instance has its tag set

#### Scenario: Keep falls back to the default environment

- **WHEN** the user runs `spinloop cloud keep 2h` with no `--env` flag
- **THEN** the `default` environment's instance is tagged
