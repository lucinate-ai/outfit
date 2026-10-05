## ADDED Requirements

### Requirement: Concurrent starts of one environment are serialised

At most one start SHALL be working on an environment at a time, whoever calls it: the CLI, a gateway, a scheduled start, or any other client of the start endpoint. A start SHALL take the environment's lock before it checks the weights, looks up the instance, launches or re-wakes it, and SHALL release the lock on every way the start can end: ready, a retryable reply, a failure reply, or an error. A start that finds the lock held SHALL NOT check the weights, look up, launch or re-wake anything. It SHALL reply with a retryable state that says another start is in progress and when to retry. A start made after the holder has finished SHALL find the instance the holder started and report it as it would for any existing instance, so two starts never produce two launches.

The lock SHALL be per environment: a start for one environment SHALL NOT wait for, or be refused because of, a start for another. A read of an environment's state SHALL NOT take the lock.

#### Scenario: Two starts arrive together

- **WHEN** two starts for the same environment reach the control plane at the same moment and the environment has no instance
- **THEN** exactly one instance is launched, and the start that did not take the lock replies with a retryable "another start is in progress" state

#### Scenario: The refused start is retried

- **WHEN** a start refused because the lock was held is retried after the first start has finished
- **THEN** it finds the first start's instance and reports it, and launches nothing

#### Scenario: Different environments do not wait for each other

- **WHEN** a start for environment `a` holds its lock and a start for environment `b` arrives
- **THEN** the start for `b` proceeds without waiting

#### Scenario: Every ending releases the lock

- **WHEN** a start ends because the weights are absent, no capacity was found, the instance went into a terminal state, the boot failed, the deadline passed, or an unexpected error was raised
- **THEN** the lock is released, and the next start for that environment is not refused because of it

#### Scenario: Status does not take the lock

- **WHEN** a start holds the environment's lock and the state of the environment is read
- **THEN** the read is answered normally

### Requirement: An abandoned lock expires

A lock SHALL record when it expires, no earlier than the moment the start holding it would itself run out of time. A start that finds an expired lock SHALL take it over and proceed as if it had found none. A lock that has not expired SHALL NOT be taken over, so a start that is still legitimately running keeps its lock for as long as it could run.

#### Scenario: A killed start does not block the environment for ever

- **WHEN** a start took the lock and its Lambda was killed before releasing it, and its time limit has since passed
- **THEN** the next start for that environment takes over the lock and proceeds

#### Scenario: A running start keeps its lock

- **WHEN** a start has held the lock for longer than a typical wake but not past its time limit
- **THEN** another start does not take the lock over

### Requirement: A stop during a start ends the start

Once a start has launched the instance or issued the command to start it, and then sees the instance stopped, stopping, shutting down or terminated, the start SHALL end at once with a retryable reply that names the state it saw, and SHALL release the lock. It SHALL NOT keep polling an instance that is not coming up until its time limit. The reply SHALL be retryable so that a client still waiting on its start can ask again, which re-wakes the instance or launches a fresh one; a stop does not wait for, or take, the start's lock.

#### Scenario: The instance is stopped after the start command

- **WHEN** a start has issued the command to start a stopped instance and the instance is then stopped by a pause, a scheduled stop or the idle sweep
- **THEN** the start ends promptly with a retryable reply naming the state, and the lock is released

#### Scenario: The instance is terminated while the engine loads

- **WHEN** an instance is terminated after it has reached running, while the start waits for the model to answer
- **THEN** the start ends promptly with a retryable reply naming the state, and the lock is released

#### Scenario: A stop is never refused because a start is running

- **WHEN** a start holds the environment's lock and a pause, stop or scheduled stop for the environment arrives
- **THEN** the stop is carried out and is not refused or delayed by the lock

### Requirement: A start does not proceed without its lock

When the control plane cannot tell whether the lock is held, because the lock could not be read or written, the start SHALL NOT proceed to launch or re-wake. It SHALL reply with a retryable state and SHALL log the cause.

#### Scenario: The lock cannot be written

- **WHEN** a start cannot create the lock for a reason other than the lock already existing
- **THEN** nothing is launched, and the reply is retryable
