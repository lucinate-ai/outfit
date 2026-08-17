## MODIFIED Requirements

### Requirement: Remote command group

The system SHALL provide a `remote` command group with the subcommands
`bootstrap`, `start`, `stop`, `restart`, `status`, `deploy`, `ls`, `metrics`,
and `keep`. `start`, `stop`, `restart`, `status`, `metrics` and `deploy` each
take an optional Outfit path:
`start` SHALL boot the endpoint and block until it is serving, then perform a
quick TCP probe of the inference endpoint — if the probe fails, a warning is
printed to stderr explaining the network mismatch (see the Remote Start Probe
specification) — and finally print the base URL and API key as shell exports;
`start` SHALL also accept a `--keep DURATION` flag that sets the instance
retention deadline to `now + DURATION`, preventing the idle sweep from
terminating it before that time (see the Remote Keep specification);
`stop` SHALL stop it immediately rather than waiting for its idle timer;
`restart` SHALL stop the endpoint in the manner of a pause — without
terminating it, so its boot disk, its weights and its stable address are
preserved — and SHALL immediately start it again, blocking until it is serving
and reporting progress as `start` does (see the Reporting a start in progress
specification); `restart` SHALL accept a `--force` flag with a `-F` short form
that, when set, performs the stop without first asking the engine to shut down
(see the Endpoint Lifecycle specification for forced stops);
`status` SHALL report instance state and endpoint health without side effects
and SHALL NOT perform any TCP probe, and SHALL include the `Retain-Until`
deadline when the instance has an active retention tag;
`keep` SHALL set the `Retain-Until` tag on the environment's instance for the
given duration, without starting or stopping the instance (see the Remote Keep
specification); `metrics` SHALL report instance state, token usage, resource
consumption, and GPU information for a running instance; `deploy` SHALL set
what the endpoint serves. `ls` SHALL list the registered remote environments
(see the Remote Environments specification). `bootstrap` SHALL stand up the
account-level AWS control plane (once per account) by obtaining and driving the
CDK project, and takes its own flags rather than an Outfit path (see the
Endpoint Provisioning specification). An unrecognised subcommand SHALL fail
naming the accepted ones.

#### Scenario: Starting the endpoint

- **WHEN** the user runs `outfit remote start` and the endpoint reports ready
- **THEN** the base URL and API key are printed as `export` lines

#### Scenario: Starting warns when the network is not admitted

- **WHEN** the user runs `outfit remote start` and the endpoint reports ready
  but the TCP probe to the inference port fails
- **THEN** a warning is printed to stderr with a remediation command, and the
  command still exits 0

#### Scenario: Starting with a keep flag

- **WHEN** the user runs `outfit remote start --keep 4h` and the endpoint reports ready
- **THEN** the base URL and API key are printed as `export` lines, and the
  instance retention deadline is set to 4 hours from now

#### Scenario: Waiting through a cold start

- **WHEN** the endpoint reports that it is still starting
- **THEN** the command waits and retries until it is ready or the timeout
  passes, rather than failing on the first attempt

#### Scenario: Restarting the endpoint

- **WHEN** the user runs `outfit remote restart` for a running environment and
  the endpoint reports ready again
- **THEN** the instance was stopped and re-woken without being terminated, the
  command blocked until the model was serving again, and the environment's
  address is the one its configuration records

#### Scenario: Forcing a restart skips the engine stop

- **WHEN** the user runs `outfit remote restart --force` (or `-F`)
- **THEN** the instance is stopped without the engine being asked to shut down
  first, and the command then blocks until the model is serving again

#### Scenario: Restarting a stopped endpoint starts it

- **WHEN** the user runs `outfit remote restart` for an environment whose instance is already stopped
- **THEN** the instance is re-woken rather than replaced, and the command blocks
  until the model is serving again, as with a plain start

#### Scenario: A failed re-wake says how to recover

- **WHEN** the stop half of a restart has taken effect but the wake fails
- **THEN** the command fails saying the instance is stopped and that
  `outfit remote start` will bring it back

#### Scenario: Listing environments

- **WHEN** the user runs `outfit remote ls`
- **THEN** the registered environments are listed rather than any endpoint being
  contacted

#### Scenario: Setting a keep deadline

- **WHEN** the user runs `outfit remote keep 2h`
- **THEN** the instance retention tag is set and the deadline is reported

#### Scenario: Metrics reports instance figures

- **WHEN** the user runs `outfit remote metrics` with a running instance
- **THEN** token counts, resource usage, and GPU information are displayed

#### Scenario: Bootstrap is a recognised subcommand

- **WHEN** the user runs `outfit remote bootstrap`
- **THEN** the command is dispatched to the provisioning flow rather than
  reported as unknown

#### Scenario: Unknown subcommand

- **WHEN** the user runs `outfit remote frobnicate`
- **THEN** the command fails listing the accepted subcommands, which include
  `bootstrap`, `metrics`, and `keep`
