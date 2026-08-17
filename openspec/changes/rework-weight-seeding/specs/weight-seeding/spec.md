## Purpose

Define the seed job as a supervised unit of work: how a request to fetch a
model's weights is identified so that repeated requests converge on one job,
what the job guarantees about finishing and about ceasing to cost money, how it
reports its progress and its outcome so those survive the compute that produced
them, and how its state is determined when it dies without reporting.

## ADDED Requirements

### Requirement: A seed is identified by the weights it produces

A seed request SHALL be identified by what it would put into storage, derived
from the same inputs that determine where the weights live. Two requests for the
same weights SHALL therefore carry the same identity, and requests for different
weights SHALL carry different identities, without a caller supplying or
remembering an identifier.

The identity SHALL be legible to an operator — something that can be read in a
listing and typed into a command — rather than an opaque digest, and SHALL be
stable across restarts of the control plane.

A seed's identity SHALL NOT include the environment that asked for it: weights
are shared, so one seed serves every environment that names that model.

#### Scenario: The same weights yield the same identity

- **WHEN** two seed requests name the same engine, model and quantisation
- **THEN** both resolve to the same seed identity

#### Scenario: Different weights yield different identities

- **WHEN** two seed requests name weights that would be stored in different
  locations
- **THEN** they resolve to different seed identities

#### Scenario: An operator can name a seed

- **WHEN** an operator sees a seed in a listing
- **THEN** its identity can be typed into a status or stop request without
  transcribing a digest

### Requirement: Repeated requests converge on one job

Requesting a seed that is already in flight SHALL join the existing job rather
than start a second one, and SHALL report that job's identity. Convergence SHALL
NOT depend on the caller having a lock or on the requests being spaced apart:
two requests arriving at the same moment SHALL still produce one job.

Requesting a seed for different weights SHALL start its own job, so unrelated
seeds proceed in parallel.

The number of jobs in flight at once SHALL be capped, and a request that would
exceed the cap SHALL be refused with a reply that says so, so that a caller in a
loop cannot launch unbounded compute.

Once weights are present, a repeat request SHALL do nothing unless it explicitly
asks to seed them again, so that re-seeding is deliberate rather than accidental.

#### Scenario: A second request joins the first

- **WHEN** a seed is requested for weights whose seed is already running
- **THEN** no second job is started, and the reply names the running job

#### Scenario: Simultaneous requests produce one job

- **WHEN** two requests for the same weights arrive concurrently
- **THEN** exactly one job exists afterwards, and both replies name it

#### Scenario: Unrelated seeds run in parallel

- **WHEN** seeds are requested for two different models
- **THEN** each gets its own job

#### Scenario: The cap is enforced

- **WHEN** a request would exceed the cap on jobs in flight
- **THEN** it is refused, and the reply says the cap was reached

#### Scenario: Re-seeding must be asked for

- **WHEN** a seed is requested for weights that are already present
- **THEN** nothing is started unless the request asks to seed them again

### Requirement: A seed always stops costing money

A seed's compute SHALL cease on success and on failure alike, without an
operator intervening. No single failure mode SHALL be able to leave it running:
the guarantee SHALL NOT rest solely on the job's own code completing, because a
job killed by the kernel or hung on a network read runs no code.

A seed SHALL have a bounded maximum lifetime, after which its compute ceases
whatever state it believes it is in. A seed that has stopped making progress for
longer than a stall threshold SHALL be treated as failed and its compute ceased,
rather than left to hold resources until the maximum lifetime expires.

Judging whether a seed is still making progress SHALL use the seed's own progress
reports. It SHALL NOT be judged by the means used to judge an idle inference
instance, which reports activity through an engine supervisor that a seed does
not run.

An operator override that holds an instance alive for debugging SHALL be honoured
by the automatic paths, so a seed can be kept for inspection deliberately while
still never being left running by accident.

#### Scenario: A successful seed stops

- **WHEN** a seed transfers every file and writes its manifest
- **THEN** its compute ceases without an operator acting

#### Scenario: A failed seed stops

- **WHEN** a seed fails part way — a missing model, an authentication failure, a
  transfer that cannot be retried
- **THEN** its compute ceases without an operator acting

#### Scenario: A killed job still stops

- **WHEN** a seed's process dies without running any of its own shutdown code
- **THEN** its compute still ceases

#### Scenario: The maximum lifetime is enforced

- **WHEN** a seed has been running longer than the maximum seed lifetime
- **THEN** its compute ceases

#### Scenario: A stalled seed is reaped early

- **WHEN** a seed has reported no progress for longer than the stall threshold
- **THEN** it is treated as failed and its compute ceases, without waiting for the
  maximum lifetime

#### Scenario: Idleness is judged from the seed's own reports

- **WHEN** the periodic sweep considers a seed
- **THEN** it judges progress from that seed's progress reports, and does not
  require the seed to answer as an inference instance would

#### Scenario: A held seed is not reaped

- **WHEN** a seed carries an operator's hold-until override that has not expired
- **THEN** the automatic paths leave it running

### Requirement: Progress and outcome are reported durably

A seed SHALL report its progress and its outcome to CloudWatch as it works, in a
form that both yields metrics and preserves a readable per-seed history. The
reports SHALL outlive the compute that produced them, so a failed seed can be
diagnosed after the instance that failed is gone.

Reports SHALL carry enough to answer "what is it doing and how far has it got":
the phase of work, the proportion transferred, and on failure a message naming
what went wrong. Terminal outcomes — succeeded, failed, stopped — SHALL be
reported as metrics, so that an alarm can be raised on them without reading logs.

Metric identity SHALL NOT include the seed's identity, so that seeding a model
does not create a metric that is billed for ever after that model is forgotten.
Per-seed detail SHALL be carried in the record rather than in metric identity.

A seed's records SHALL be addressable by its identity, so reading one seed's
history does not require scanning others'. A re-seed of the same weights SHALL
NOT have its records interleaved with the earlier attempt's.

#### Scenario: Progress is visible while the seed runs

- **WHEN** a seed is transferring files
- **THEN** its phase and the proportion transferred are readable from CloudWatch
  without connecting to the instance

#### Scenario: A failure is diagnosable after the instance is gone

- **WHEN** a seed fails and its compute ceases
- **THEN** the records naming what went wrong are still readable

#### Scenario: Outcomes are alarm-able

- **WHEN** a seed reaches a terminal outcome
- **THEN** that outcome is recorded as a metric

#### Scenario: Seeding a model creates no lasting metric cost

- **WHEN** many different models have been seeded over time
- **THEN** the number of distinct billed metrics does not grow with the number of
  models seeded

#### Scenario: A re-seed is distinguishable from the attempt before it

- **WHEN** weights are seeded again after an earlier attempt
- **THEN** the later attempt's records are read without the earlier attempt's
  being mistaken for them

### Requirement: A seed's state combines what it reported with whether it exists

A seed's state SHALL be derived from both its reports and the existence of its
compute, because either alone is misleading: reports stop arriving when a job
dies mid-way, and a live job that has not yet reported anything is not idle.

A seed whose compute no longer exists and whose last report was not terminal
SHALL be reported as failed, never as still in progress. A seed whose compute
exists but which has reported nothing yet SHALL be reported as starting.

#### Scenario: A seed that died mid-transfer is not reported as running

- **WHEN** a seed's compute is gone and its last report said it was part way
  through
- **THEN** its state is reported as failed

#### Scenario: A seed that has not reported yet

- **WHEN** a seed's compute exists but no report has arrived
- **THEN** its state is reported as starting

#### Scenario: A finished seed is reported from its records

- **WHEN** a seed reported a terminal outcome and its compute is gone
- **THEN** that outcome is reported

### Requirement: The transfer is robust without staging the whole model

A seed SHALL NOT require local storage proportional to the size of the model it
transfers, so that seeding a larger model does not require resizing anything.

A transfer failure SHALL cost only the portion being transferred when it failed,
not the whole file and not the whole job, so that a network blip part way through
a large file does not restart it. Where a portion cannot be transferred after
bounded retries, the seed SHALL fall back to a means that does complete rather
than fail the job, so that robustness is never traded away for the absence of
staging.

Each file's integrity SHALL be verified against the checksum the source
publishes, and a file that fails verification SHALL fail the seed rather than be
stored, so that the manifest's guarantee is real.

#### Scenario: A model larger than local storage is seeded

- **WHEN** a seed transfers a model larger than the instance's disk
- **THEN** the transfer succeeds

#### Scenario: A blip does not restart a file

- **WHEN** a transfer fails part way through a large file
- **THEN** only the failed portion is retried

#### Scenario: A portion that keeps failing does not fail the job

- **WHEN** a portion cannot be transferred after its retries are exhausted
- **THEN** the seed completes that file by other means rather than failing

#### Scenario: A corrupted file fails the seed

- **WHEN** a transferred file does not match the source's published checksum
- **THEN** the seed fails and no manifest is written

### Requirement: Which files a seed takes is declared per engine

Which of a model repository's files an engine needs SHALL be stated as a
selection rule per engine, not as a script fragment, so that adding an engine
does not mean writing boot shell.

Where an engine requires exactly one file and the selection matches more than
one, the seed SHALL fail and say so, rather than choose one and store an
incomplete model.

#### Scenario: An engine that takes the whole repository

- **WHEN** a seed runs for an engine whose selection takes every file
- **THEN** every file is transferred

#### Scenario: An engine that takes one file

- **WHEN** a seed runs for an engine whose selection identifies a single file
- **THEN** that file is transferred, under the name the engine expects

#### Scenario: An ambiguous selection fails loudly

- **WHEN** an engine requires one file and the selection matches several
- **THEN** the seed fails naming the ambiguity, and no manifest is written

### Requirement: Seeding depends on no baked machine image

Starting a seed SHALL NOT require a machine image to have been built first, so
that seeding cannot fail because a bake has not run, and so that a seed does not
borrow an image built for something else in order to obtain its tooling.

Whatever the seed needs beyond its base image SHALL be obtained from sources
within the deployment's own control or within its cloud provider, rather than
from a third party reached over the internet at boot.

#### Scenario: Seeding into a deployment with no baked image

- **WHEN** a seed is requested in an account where no machine image has been built
- **THEN** the seed runs

#### Scenario: The seed does not borrow an inference image

- **WHEN** a seed runs
- **THEN** it does not require the machine image of any inference engine

### Requirement: Seed credentials are not disclosed

The credential used to fetch from a gated model source SHALL NOT appear in any
log the seed produces, in the boot output of its compute, or in that compute's
console output. It SHALL be read by the program that needs it, not passed through
a shell.

#### Scenario: The source credential stays out of the logs

- **WHEN** a seed runs with a credential for a gated model source
- **THEN** that credential's value appears in no log or console output the seed
  produces
