# remote-engine-host Specification

## Purpose
TBD - created by archiving change remote-on-daemon. Update Purpose after archive.
## Requirements
### Requirement: Outfit is baked into the runtime AMI

The runtime AMIs for both runners SHALL include a pinned outfit release,
installed at image-bake time with its version declared alongside the other
pinned runner versions. Changing the pinned version SHALL produce a new image
version, so an instance's outfit is decided by its AMI, not fetched at boot.

#### Scenario: Instance boots with outfit present

- **WHEN** an instance launches from a runtime AMI
- **THEN** the outfit binary is already installed and runnable without network
  access

### Requirement: The instance engine runs under the daemon

At boot the instance SHALL run `outfit daemon` as its engine host, bound to
loopback, instead of a per-runner engine unit. The daemon's config directory
SHALL be pinned to a fixed system path via `OUTFIT_CONFIG_DIR` on its service
unit, so it does not depend on `$HOME` (which a systemd service does not get) —
what the boot writes and what the daemon reads are the same location. The boot
sequence SHALL derive the daemon's deploy config from the environment's stored
deploy config — with the cloud-owned settings (bind address and port, API-key
delivery, the synced local weights path) resolved into it — write it under that
pinned config directory, and request the engine's start through the control API
once the daemon answers, so the boot start is the same explicit API start any
client performs. The engine command the daemon runs SHALL be equivalent to the
one the boot script previously installed for that runner.

#### Scenario: Boot starts the engine through the daemon

- **WHEN** an instance boots for an environment whose deploy config names a
  runner and model
- **THEN** the daemon starts that engine serving the synced weights, and the
  endpoint answers on the instance's serving port as before

#### Scenario: The daemon reads what the boot wrote

- **WHEN** the boot writes the deploy config under the daemon's pinned
  `OUTFIT_CONFIG_DIR` and requests a start
- **THEN** the daemon finds that deploy config and starts the engine, rather
  than reporting nothing to serve

#### Scenario: The control API is loopback-only

- **WHEN** the daemon's control API is listening on the instance
- **THEN** it is bound to loopback, unreachable from the network, and needs no
  bearer token

### Requirement: Control Lambdas read the daemon

The stats path SHALL obtain engine and system metrics by calling the
on-instance daemon's metrics endpoint over SSM, merging in what only the
control plane knows (environment, instance id and type, uptime), and SHALL
preserve the reply shape `outfit remote metrics` renders today. The control
plane SHALL NOT collect metrics by running per-metric shell commands on the
instance.

The idle check SHALL read the daemon's **status** endpoint over SSM and take
the idle duration it reports as the answer to "has this engine been working?".
It SHALL NOT compare counters itself, and the control plane SHALL keep no
activity history of its own — no stored counter, no last-change time, no
last-wake time.

A daemon that cannot be reached, and a daemon whose reply carries no
last-active time, SHALL both be treated as showing no activity, so an instance
in either state is terminated once the idle threshold passes rather than left
running. There SHALL be no second way of judging idleness for a daemon that
does not report one: an instance running an outfit older than this behaviour
is handled by deploying the control plane after the images that carry it, not
by a compatibility path in the check.

#### Scenario: Stats flow through the daemon

- **WHEN** `outfit remote metrics` runs against a running instance
- **THEN** the reported state, GPU, CPU, RAM and token figures come from the
  daemon's metrics endpoint and render in the existing bar, table and JSON
  formats unchanged

#### Scenario: Idle detection uses the daemon's own idle time

- **WHEN** the idle check runs against an instance whose daemon reports a
  last-active time
- **THEN** it decides from the idle duration the daemon reports, and reads no
  counters and no stored activity history

#### Scenario: An unreachable daemon shows no activity

- **WHEN** the idle check cannot reach the daemon on an instance
- **THEN** the instance is treated as showing no activity and is terminated
  once the idle threshold passes

#### Scenario: A daemon reporting no last-active time shows no activity

- **WHEN** the idle check reaches a daemon whose reply carries no last-active
  time
- **THEN** the instance is treated as showing no activity, and no counters are
  read to second-guess that
### Requirement: Crash recovery is preserved

The instance SHALL restart a crashed engine automatically: a boot-installed
check SHALL ask the daemon's status periodically and request a start when the
engine is `crashed`. The daemon itself remains restart-free; the recovery
loop is instance plumbing.

#### Scenario: Crashed engine comes back

- **WHEN** the engine process on the instance dies unexpectedly
- **THEN** within the check interval the engine is started again without human
  intervention

### Requirement: Engine logs keep shipping

The engine's output SHALL continue to reach its CloudWatch log group and stay
size-bounded on disk, sourced from the daemon's engine log file at its stable
path. The boot log's shipping is unchanged.

#### Scenario: Engine log reaches CloudWatch via the daemon's file

- **WHEN** the engine writes output while running under the daemon
- **THEN** those lines appear in the engine's CloudWatch log stream
