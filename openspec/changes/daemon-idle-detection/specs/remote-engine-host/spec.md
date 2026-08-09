## MODIFIED Requirements

### Requirement: Control Lambdas read the daemon

The stats path SHALL obtain engine and system metrics by calling the
on-instance daemon's metrics endpoint over SSM, merging in what only the
control plane knows (environment, instance id and type, uptime), and SHALL
preserve the reply shape `outfit remote metrics` renders today. The control
plane SHALL NOT collect metrics by running per-metric shell commands on the
instance.

The idle check SHALL read the daemon's **status** endpoint over SSM and take
the idle duration it reports as the answer to "has this engine been working?",
rather than comparing counters itself or keeping activity history in the
control plane. A daemon that reports no last-active time is one baked before
this behaviour existed; against it the check SHALL fall back to reading the
in-flight and cumulative counters from the daemon's metrics endpoint and
comparing them against control-plane state, exactly as it does today, so a
fleet part-way through a re-bake is judged correctly either way. A daemon that
cannot be reached at all SHALL be treated as showing no activity, on both
paths.

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

#### Scenario: An older daemon falls back to counters

- **WHEN** the idle check runs against an instance whose daemon reports no
  last-active time
- **THEN** it decides from the daemon-reported in-flight and cumulative token
  counters compared against the control plane's stored activity state, as
  before

#### Scenario: An unreachable daemon shows no activity

- **WHEN** the idle check cannot reach the daemon on an instance
- **THEN** the instance is treated as showing no activity and is terminated
  once the idle threshold passes
