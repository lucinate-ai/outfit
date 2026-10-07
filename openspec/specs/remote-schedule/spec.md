# remote-schedule Specification

## Purpose
Let a cloud environment start and stop itself on cron schedules, so a team that
works set hours has the model up when it sits down and stops paying for it when
it leaves. The control plane stores and runs the schedules, so they fire with no
machine of the operator's switched on; the CLI sets, shows and clears them.

## Requirements

### Requirement: An environment holds a list of schedules

Each environment SHALL hold zero or more schedules. A schedule SHALL have an action (`start` or `stop`), a cron expression in the five-field form (minute, hour, day of month, month, day of week) and an IANA time zone. When no time zone is given it SHALL be UTC. Setting schedules for an environment SHALL replace its whole list, so the list after the call is exactly the one sent. An invalid cron expression, an unknown time zone, an unknown action or an invalid environment name SHALL be rejected, and a rejected request SHALL leave the existing schedules unchanged. The schedules SHALL be stored in the control plane, not on any user's machine, so they fire whether or not any user machine is on.

#### Scenario: Setting office-hours schedules

- **WHEN** schedules are set for an environment with a `start` at `0 8 * * 1-5` and a `stop` at `0 18 * * 1-5`, in `Europe/London`
- **THEN** the environment's list holds those two schedules and no others

#### Scenario: Setting replaces the previous list

- **WHEN** an environment has two schedules and a set request is made with one different schedule
- **THEN** the environment has only that one schedule afterwards, and the two earlier ones no longer fire

#### Scenario: An invalid expression is rejected

- **WHEN** a set request contains a cron expression that is not five valid fields
- **THEN** the request is refused, the error names the expression, and the environment's earlier schedules still fire

#### Scenario: Schedules are per environment

- **WHEN** schedules are set for one environment
- **THEN** no other environment's schedules change and no other environment's instance is started or stopped

### Requirement: A schedule fires in its own time zone

A schedule SHALL fire when its cron expression matches in its time zone, including across daylight saving changes: a schedule at 08:00 in `Europe/London` fires at 08:00 local time on both sides of a clock change.

#### Scenario: A schedule follows local time

- **WHEN** a `start` schedule is set at `0 8 * * *` in `Europe/London`
- **THEN** it fires at 07:00 UTC in summer and 08:00 UTC in winter

### Requirement: A scheduled start brings the environment up

When a `start` schedule fires, the control plane SHALL start the environment's instance with the same behaviour as an on-demand start, including the weights check and the wait until the model is answering. When the instance is already running, the scheduled start SHALL leave it as it is. When the start cannot find capacity or the weights are absent, the run SHALL record why in the start Lambda's log and SHALL NOT retry until the next firing.

#### Scenario: A stopped environment is started on schedule

- **WHEN** a `start` schedule fires and the environment has no running instance
- **THEN** the instance is launched or re-woken, as an on-demand start would do

#### Scenario: A running environment is left alone

- **WHEN** a `start` schedule fires and the instance is already running
- **THEN** nothing is launched and the instance keeps running

#### Scenario: A scheduled start and a manual start launch one instance

- **WHEN** a `start` schedule fires while a start from a client for the same environment holds its start lock
- **THEN** the scheduled start launches nothing and ends with a retryable reply, and only one instance exists afterwards

### Requirement: A scheduled stop pauses the environment

When a `stop` schedule fires, the control plane SHALL pause the environment's instance: stop it without terminating it, as `spinloop cloud pause` does, so it can be woken again. When the instance carries a `Retain-Until` deadline that has not passed, the scheduled stop SHALL be skipped and the skip recorded in the stop Lambda's log. When there is no running instance, the scheduled stop SHALL do nothing.

#### Scenario: A running environment is paused on schedule

- **WHEN** a `stop` schedule fires and the instance is running with no retention deadline
- **THEN** the instance is stopped and not terminated

#### Scenario: A retained instance is not stopped

- **WHEN** a `stop` schedule fires and the instance has a `Retain-Until` time in the future
- **THEN** the instance keeps running

#### Scenario: Nothing to stop

- **WHEN** a `stop` schedule fires and the environment has no running instance
- **THEN** the run does nothing and reports no error

### Requirement: The schedule command sets, shows and clears schedules

`spinloop cloud schedule set` SHALL accept one or more `--start CRON` and `--stop CRON` flags and an optional `--timezone ZONE`, and SHALL replace the environment's schedules with them. `spinloop cloud schedule show` SHALL print the environment's schedules, one per line, giving the action, expression and time zone, and SHALL print that there are none when the list is empty. `spinloop cloud schedule clear` SHALL remove every schedule. The commands SHALL select the environment as the other `cloud` subcommands do: `--env <name>` or the per-user default. `set` with neither `--start` nor `--stop` SHALL fail and say to use `clear` to remove schedules. When the deployment's control plane has no schedule endpoint, the commands SHALL fail with an error naming the fix (re-run `spinloop cloud bootstrap`).

#### Scenario: Setting two schedules from the command line

- **WHEN** the user runs `spinloop cloud schedule set --start "0 8 * * 1-5" --stop "0 18 * * 1-5" --timezone Europe/London`
- **THEN** the environment has those two schedules and the command prints them

#### Scenario: Showing no schedules

- **WHEN** the user runs `spinloop cloud schedule show` for an environment with none
- **THEN** the output says there are no schedules and the command succeeds

#### Scenario: Clearing

- **WHEN** the user runs `spinloop cloud schedule clear`
- **THEN** the environment has no schedules and none fires afterwards

#### Scenario: Set with nothing to set

- **WHEN** the user runs `spinloop cloud schedule set` with no `--start` or `--stop`
- **THEN** the command fails and tells the user to use `clear` to remove schedules

#### Scenario: An older control plane

- **WHEN** the deployment's configuration has no schedule endpoint
- **THEN** the command fails with an error telling the user to re-run `spinloop cloud bootstrap`

### Requirement: Show reports the next scheduled runs

When the environment has schedules, `spinloop cloud schedule show` (and `set`, which prints the same listing) SHALL report the next time a start fires and the next time a stop fires, as absolute UTC times, each on its own "next start" or "next stop" line. An action with no schedule, or whose schedule never fires again, SHALL have no line. When the environment has no schedules, the output SHALL say so and have no "next" line.

#### Scenario: A scheduled environment shows its next runs

- **WHEN** the user runs `spinloop cloud schedule show` for an environment with a start and a stop schedule
- **THEN** the output includes "next start" and "next stop" lines with absolute times

#### Scenario: An action with no schedule has no line

- **WHEN** the environment has only a start schedule
- **THEN** the output has a "next start" line and no "next stop" line

#### Scenario: An unscheduled environment omits the lines

- **WHEN** the user runs `spinloop cloud schedule show` for an environment with no schedules
- **THEN** the output says there are no schedules and has no "next start" or "next stop" line
