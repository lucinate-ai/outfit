## MODIFIED Requirements

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
