# engine-activity Specification

## Purpose

How the daemon judges whether the engine it supervises is doing any work: a
background sampler that reads the engine's own counters on the host, the
last-active time it keeps from those samples, and the idle duration derived
from it. This is what makes "is this engine active?" a question outfit answers
once, on the box, rather than something each caller re-derives from raw
counters at whatever rate it happens to poll.

## Requirements

### Requirement: Background activity sampling

While an engine is running, the daemon SHALL sample that engine's own
token/request counters on a recurring schedule of its own, independently of
any request made to its control API. The interval SHALL be short relative to
the idle thresholds callers apply — on the order of tens of seconds — so that
a quiet moment between two requests cannot be mistaken for idleness.

Sampling SHALL run only while the supervised engine is running and only when a
scrape target is known; with no engine running, or an engine that exposes no
metrics endpoint, the daemon SHALL sample nothing rather than report errors.
A failed sample SHALL be treated as no observation — it SHALL NOT count as
activity, and SHALL NOT count as evidence of idleness either, so a transient
scrape failure neither extends nor shortens the idle duration.

The sampler SHALL start with the daemon and stop when the daemon shuts down,
leaving no goroutine running after shutdown.

#### Scenario: Sampling happens without being asked

- **WHEN** an engine is running and no client calls the control API
- **THEN** the daemon still reads the engine's counters repeatedly, at its own
  interval

#### Scenario: Nothing to sample

- **WHEN** no engine is running, or the running engine exposes no metrics
  endpoint
- **THEN** the daemon takes no samples and reports no sampling errors

#### Scenario: A failed sample changes nothing

- **WHEN** one sample fails because the engine's metrics endpoint does not
  answer, and a later sample succeeds showing no change
- **THEN** the last-active time is the same as it would have been had the
  failed sample never been taken

#### Scenario: Sampling stops with the daemon

- **WHEN** the daemon shuts down
- **THEN** the sampling loop exits

### Requirement: What counts as activity

A sample SHALL count as activity when it shows requests in flight, or when its
cumulative counter differs from the counter of the previous sample. "Differs"
rather than "increased": an engine restart resets its counters, and that is a
sign of life, not of idleness. The first sample after an engine starts SHALL
establish the baseline counter without being read as a change.

Starting an engine SHALL itself count as activity, so an engine that has just
been started is never reported as having been idle since before it existed.

#### Scenario: Requests in flight are activity

- **WHEN** a sample reports one or more requests in flight
- **THEN** the engine is treated as active at that moment

#### Scenario: Work between two samples is activity

- **WHEN** two consecutive samples both report nothing in flight, but the
  cumulative counter has moved between them
- **THEN** the engine is treated as active

#### Scenario: A counter reset is activity

- **WHEN** the cumulative counter is lower than the previous sample's
- **THEN** the engine is treated as active rather than as unchanged

#### Scenario: A freshly started engine is active

- **WHEN** an engine has just started and no sample has been taken yet
- **THEN** the last-active time is the start time, not an earlier time or none

### Requirement: Last-active time and idle duration

The daemon SHALL maintain a **last-active** timestamp, moved forward to the
time of each sample that counts as activity, and SHALL derive from it the
duration for which the engine has been idle.

The last-active time SHALL persist across a stop and a later start of the
engine within the same daemon run: stopping an engine SHALL NOT clear it, so a
daemon whose engine is stopped reports how long it has been since real work,
not an absence of information. A daemon that has never run an engine SHALL
report no last-active time at all, rather than reporting the daemon's own
start time as activity.

#### Scenario: Activity moves the timestamp forward

- **WHEN** a sample counts as activity
- **THEN** the last-active time becomes the time of that sample

#### Scenario: Idle duration grows while nothing happens

- **WHEN** several consecutive samples show no activity
- **THEN** the reported idle duration grows with elapsed time and the
  last-active time is unchanged

#### Scenario: Stopping the engine preserves the history

- **WHEN** the engine is stopped after a period of activity
- **THEN** the last-active time still reports when that activity happened

#### Scenario: A daemon that has served nothing

- **WHEN** the daemon has been running but no engine has ever been started
- **THEN** no last-active time is reported
