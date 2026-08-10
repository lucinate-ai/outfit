## ADDED Requirements

### Requirement: Status reports when the endpoint last did work

`outfit remote status` SHALL report how long it has been since the endpoint's
engine last did any work, alongside the instance state and health it reports
already. The figure SHALL come from the activity the on-instance daemon
tracks, not from a measurement the control plane makes itself — one answer,
derived on the box, however it is asked for.

The figure SHALL be labelled "last active", matching the wording and duration
formatting used everywhere else this fact appears, so the same fact reads the
same way in every command.

Collecting it SHALL NOT make `status` slower than its health check already
makes it: the daemon SHALL be asked in parallel with the health check rather
than after it. Nor SHALL it introduce a side effect — `status` SHALL remain a
read, and SHALL still perform no TCP probe.

#### Scenario: A running endpoint reports its last activity

- **WHEN** the user runs `outfit remote status` against a running endpoint
  whose engine has served work
- **THEN** the output reports how long ago that work happened, labelled "last
  active", beside the state and health lines

#### Scenario: Status stays a read

- **WHEN** the user runs `outfit remote status`
- **THEN** nothing is started, stopped or probed in order to obtain the
  last-active figure

### Requirement: Status degrades when activity cannot be read

`outfit remote status` SHALL omit the last-active figure rather than fail,
report zero, or imply inactivity, whenever the figure cannot be obtained. That
covers an endpoint whose engine has not yet done any work, a daemon that
cannot be reached or answers unrecognisably, and an instance that is not
running — reaching the daemon needs a running box, so a stopped or undeployed
environment has nothing to report about its engine.

A failure to read the activity SHALL NOT affect the rest of the report: the
state and health lines SHALL be exactly what they are today, and the command
SHALL still succeed.

#### Scenario: A stopped instance reports no activity figure

- **WHEN** the user runs `outfit remote status` and the instance is stopped or
  undeployed
- **THEN** the output reports the state as it does today and shows no
  last-active figure

#### Scenario: An unreachable daemon does not spoil the report

- **WHEN** the endpoint is running but its daemon cannot be reached
- **THEN** the state and health lines are reported as they are today, no
  last-active figure is shown, and the command succeeds

#### Scenario: An engine that has done nothing yet

- **WHEN** the endpoint is running and its daemon reports no last-active time
- **THEN** no last-active figure is shown, rather than one implying the engine
  has been quiet since it started
