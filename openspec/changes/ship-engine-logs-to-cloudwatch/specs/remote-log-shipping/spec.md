## ADDED Requirements

### Requirement: Engine output is captured to a durable file

The inference engine's standard output and standard error SHALL be written to a
durable log file on the instance, at a stable path that does not depend on the
environment or instance. Writing to this file SHALL NOT prevent the engine from
starting or serving; if the file cannot be written the engine SHALL still run.
The file is the single on-instance source of engine logs that operators read and
that the log shipper tails, replacing reliance on the systemd journal.

#### Scenario: Engine writes to the log file

- **WHEN** an instance's inference engine starts and produces output
- **THEN** that output is appended to the engine's on-disk log file at the stable
  path
- **AND** the same output is readable on the instance without querying the
  systemd journal

### Requirement: Engine logs are shipped to CloudWatch Logs

Each running instance SHALL ship its engine log file to CloudWatch Logs so the
logs are readable from AWS without connecting to the instance. Shipping SHALL be
performed by an agent present on the instance's machine image, so that no
per-boot install is required. Logs SHALL be delivered to a **log group named for
the engine** and a **log stream named for the environment and instance**, so that
one engine's logs across many instances share a group while each instance's logs
are individually addressable and attributable to their environment.

#### Scenario: Running instance ships its engine logs

- **WHEN** an instance is serving an environment on a given engine
- **THEN** its engine log file is delivered to the engine's CloudWatch log group
- **AND** it appears in a stream identifying the environment and the instance id

#### Scenario: Logs outlive the instance

- **WHEN** an instance terminates after producing engine logs
- **THEN** the logs it shipped remain readable in CloudWatch Logs after the
  instance no longer exists, subject only to the group's retention

### Requirement: Boot logs are shipped to CloudWatch Logs

Each running instance SHALL ship its boot log — the output of the instance's
start-up (user-data) script, covering the steps that run before the engine
starts, such as the weights download and credential setup — to CloudWatch Logs,
so that a failure occurring before the engine is up is visible even though the
engine log would be empty. Boot logs SHALL be delivered to a boot log group,
addressable per environment and instance in the same way as engine logs, and
SHALL likewise survive termination of the instance. Start-up output SHALL avoid
gratuitous volume (for example progress output from bulk downloads) so the
shipped boot log remains legible.

#### Scenario: A pre-engine failure is visible after termination

- **WHEN** an instance's start-up fails before the engine starts — for example
  the weights download fails
- **THEN** the boot log capturing that failure is delivered to the boot log
  group under the instance's environment/instance stream
- **AND** it remains readable in CloudWatch Logs after the instance terminates

### Requirement: The on-instance log file is size-bounded

The engine log file SHALL be rotated so that its disk usage stays bounded
regardless of how long the instance runs or how much the engine logs, and SHALL
NOT be allowed to grow until it exhausts the root volume. Rotation SHALL be
compatible with the engine holding the file open for append (it SHALL NOT rely
on the engine reopening the file), and SHALL NOT interrupt log shipping. Rotation
SHALL be triggered by size, frequently enough that a rapidly-logging engine
(for example a crash loop) cannot exhaust the disk between rotations.

#### Scenario: A chatty engine does not fill the disk

- **WHEN** the engine writes a large volume of output over the instance's
  lifetime, including bursts such as repeated restarts
- **THEN** the engine log file is rotated once it reaches its size threshold
- **AND** total on-disk log usage stays within a bounded budget
- **AND** the shipping agent continues delivering logs across the rotation

### Requirement: The log groups are managed infrastructure

Every CloudWatch log group the instances ship to — the per-engine groups and the
boot group — SHALL be created as part of the shared, account-level
infrastructure, with an explicit retention period and an explicit removal policy,
rather than being created implicitly by the shipping agent at first write.
Retention SHALL bound how long logs are kept so storage does not grow without
limit.

#### Scenario: Log groups exist with managed retention

- **WHEN** the shared infrastructure is deployed
- **THEN** each log group the instances ship to exists with a defined retention
  period
- **AND** its lifecycle (creation and removal) is governed by the infrastructure
  definition, not by the agent

### Requirement: Instances are permitted to ship logs

An instance SHALL hold exactly the permissions its shipping agent needs to
create log streams and put log events into the log groups it ships to, and no
broader logging permission than that. The permission SHALL travel with the
instance's existing role so no additional credential handling is introduced.

#### Scenario: Instance can write its stream

- **WHEN** the shipping agent on a running instance delivers logs
- **THEN** it is authorised to create its stream and put events into the log
  groups it ships to
- **AND** it is not granted logging permissions beyond what shipping requires
