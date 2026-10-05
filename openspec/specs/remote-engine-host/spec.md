# remote-engine-host Specification

## Purpose

The instance-side half of the cloud deployment: spinloop installed during boot
from the deploy config's version (or the latest release), the boot writing a
deploy config and starting the engine through the daemon's control API, and the
control-plane Lambdas reaching that API over SSM.

It exists to make the cloud path and every other path the same path. The
instance used to run a hand-rolled systemd unit while the Lambdas collected
metrics by shelling commands over SSM, duplicating in TypeScript what spinloop
already does in Go. Now the Lambdas are thin relays to the same API a fleet
client speaks: they read metrics from it, and they read its judgement of engine
idleness rather than forming their own.

## Requirements
### Requirement: The instance engine runs under the daemon

At boot the instance SHALL run `spinloop daemon` as its engine host, bound to
loopback, instead of a per-runner engine unit. The daemon's config directory
SHALL be pinned to a fixed system path via `SPINLOOP_CONFIG_DIR` on its service
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
  runner and model, and the control plane's start request arrives
- **THEN** the daemon starts that engine serving the synced weights, and the
  endpoint answers on the instance's serving port as before

#### Scenario: The daemon reads what the boot wrote

- **WHEN** the boot writes the deploy config under the daemon's pinned
  `SPINLOOP_CONFIG_DIR` and requests a start
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
preserve the reply shape `spinloop cloud metrics` renders today. The control
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
does not report one: an instance running an spinloop older than this behaviour
is handled by deploying the control plane after the images that carry it, not
by a compatibility path in the check.

#### Scenario: Stats flow through the daemon

- **WHEN** `spinloop cloud metrics` runs against a running instance
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

### Requirement: Spinloop is installed during boot, not baked into the AMI

The runtime AMIs for both runners SHALL NOT include a pinned spinloop release.
Instead, the instance's boot sequence SHALL fetch and install the spinloop
binary early — before the `spinloop-daemon` systemd unit is written and
enabled — so the daemon has a binary to run once its unit starts. An AMI
bake SHALL NOT need to change when only spinloop's own code changes.

#### Scenario: The AMI carries no spinloop binary or version pin

- **WHEN** a runtime AMI is built
- **THEN** it contains no spinloop binary and declares no pinned spinloop version
  among its bake parameters

#### Scenario: Boot installs spinloop before the daemon can start

- **WHEN** an instance launches from a runtime AMI
- **THEN** the boot sequence installs the spinloop binary before the
  `spinloop-daemon` systemd unit is written and enabled

#### Scenario: A daemon-only change needs no new AMI

- **WHEN** an spinloop release ships a daemon-only change
- **THEN** an environment's instances pick it up through a fresh boot,
  without any runtime AMI being rebuilt

### Requirement: The spinloop install is idempotent

The boot's spinloop install step SHALL be safe to run more than once: given the
same resolved version, re-running it SHALL succeed without re-downloading an
already-correct install, and an interrupted attempt SHALL never leave a
partially-installed or corrupted binary in place. The downloaded release
SHALL be checksum-verified against its own published checksums before
installation is considered complete, matching the verification the AMI bake
performed previously.

#### Scenario: Re-running the install step is a no-op when already correct

- **WHEN** the boot's install step runs again for a version already correctly
  installed
- **THEN** it leaves the existing binary in place unchanged and does not fail

#### Scenario: An interrupted install never leaves a broken binary

- **WHEN** the install step is interrupted while downloading or verifying the
  release
- **THEN** the previous state (no binary, or a previously verified one) is
  preserved — never a partially-written or unverified binary

#### Scenario: The downloaded binary is checksum-verified

- **WHEN** boot downloads an spinloop release
- **THEN** it verifies the download against the release's published checksums
  before installing it, and fails the boot loudly on a mismatch rather than
  installing an unverified binary

### Requirement: Spinloop version resolves from the deploy config, defaulting to latest

The version of spinloop a boot installs SHALL come from the environment's
deploy config: an explicit pinned version SHALL install exactly that release,
and an absent pin SHALL install the latest published release. A pinned
version that does not exist as a release SHALL fail the boot loudly rather
than silently falling back to another version.

#### Scenario: No pin installs latest

- **WHEN** an environment's deploy config names no spinloop version
- **THEN** the boot installs the latest published spinloop release

#### Scenario: A pin installs exactly that version

- **WHEN** an environment's deploy config pins an spinloop version
- **THEN** the boot installs exactly that release, not the latest

#### Scenario: An unresolvable pin fails loudly

- **WHEN** a pinned version names a release that does not exist
- **THEN** the boot fails with a clear error rather than installing a
  different version in its place

#### Scenario: A version pin change takes effect on the next fresh launch

- **WHEN** an operator changes an environment's pinned spinloop version while
  its instance is stopped
- **THEN** the change is not applied until the environment's instance is next
  freshly launched — a re-wake of the existing stopped instance keeps
  whatever spinloop version it last installed at boot
