## Context

See proposal.md for why. The control plane (`remote/`) already runs one shared set of Lambdas for every environment, finds an environment's resources by name, and exposes each Lambda through a SigV4-signed Function URL. Environment state lives in SSM parameters under `/cloud-vm-llm/<env>/` and in tags on the instance. The stop Lambda is already invoked on a five-minute EventBridge rule, and tells that call from a Function URL call by the event's `source`. `spinloop remote keep` is the nearest existing per-environment setting and follows the same client → Function URL → Lambda route.

## Goals / Non-Goals

**Goals:**
- Schedules that fire with no user machine running.
- Cron expressions with a time zone, correct across daylight saving.
- One CLI to set, show and clear them, and status that shows what runs next.
- Reuse the existing start and stop code, so a scheduled run cannot behave differently from a manual one.

**Non-Goals:**
- Running schedules from the CLI or a local daemon.
- Local (non-cloud) nodes. Only remote environments get schedules.
- Adding a single schedule or removing a single one by id. The list is replaced whole.
- Scheduled terminate. A scheduled stop pauses.

## Decisions

**EventBridge Scheduler, not an EventBridge rule per schedule.** EventBridge Scheduler takes a cron expression with an IANA time zone and handles daylight saving. Classic rules are UTC only. The cost is that schedules are created at runtime, so the stack creates one schedule group (`spinloop-remote`) and one role that Scheduler assumes to invoke the start and stop Lambdas.

**A schedule Lambda owns the schedules.** A new Lambda with a Function URL takes `PUT` (replace the list), `GET` (read it) and `DELETE` (clear), for the environment named by `?env=`, like the other Lambdas. It is the only code with `scheduler:*` permission, limited to the group. This keeps the CLI user's policy to "invoke this URL", as for every other command.

**The list is stored in SSM, the schedules are derived from it.** The list is kept as JSON in `/cloud-vm-llm/<env>/schedules`. A replace writes the parameter, then makes the Scheduler schedules in the group named `<env>--<n>` match it: create or update those in the list, delete the rest of that environment's prefix. If the Scheduler calls fail part way, the parameter already holds the intended list and a repeat of the same request converges. Validation (cron, zone, action, env name) runs before anything is written.

**Cron form.** The CLI and API take the usual five-field cron. Scheduler's own cron has six fields (with year) and a `?` rule for day fields, so the Lambda converts: it adds `*` for the year and replaces the day-of-month or day-of-week `*` with `?` when the other is set. The conversion is one pure function with table tests. Expressions that cannot be converted (for example both day fields set) are rejected, naming the expression.

**Targets and payload.** Each schedule's target is the start or stop Lambda with a fixed JSON input `{"source":"spinloop.schedule","action":"start|stop","environment":"<env>"}`. The start Lambda and stop Lambda each gain a branch for that source, ahead of the Function URL branch. Scheduler invokes Lambda asynchronously, so the 15-minute start is not cut short. The scheduled start calls the same start function as the URL path; the scheduled stop calls the same pause function, after reading the instance's `Retain-Until` tag.

**A scheduled start takes the environment's start lock.** The scheduled start calls the same `wake()` as a client's start, and `wake()` takes the per-environment lock added by the start lock change (`endpoint-lifecycle`). A schedule firing while someone runs `spinloop remote start` therefore cannot launch a second instance. The scheduled run finds the lock held, ends with a retryable reply that is only logged, and the next firing tries again. A scheduled stop does not take the lock; stops are never locked, and a start whose instance is stopped under it ends at once.

**Next runs.** There is no `remote status` command (`spinloop status` is a fleet-wide table with one row per node), so the next runs are shown by `schedule show` rather than a status line. The schedule Lambda computes the next firing of each action from the stored list, in each schedule's zone, with its own small cron evaluator in `lambda/shared/schedules.ts` (no cron library). The result is not read from Scheduler, so it is the same whether or not Scheduler has caught up. A time the clocks skip is not a firing; a time they repeat fires at the first occurrence.

**Client.** `internal/remote` gains `SetSchedules`, `GetSchedules`, `ClearSchedules` calling a new `schedule_url` from the config. A missing `schedule_url` returns the "re-run bootstrap" error. The CLI group is `remote schedule {set,show,clear}`, built the way `keep` is.

## Risks / Trade-offs

- Scheduler creates schedules a few seconds after the call returns; a `set` followed at once by a firing minute is not guaranteed to be seen. Accepted.
- A scheduled start for an environment whose weights are not seeded starts a seed (the existing behaviour) and then ends without a ready instance. The log says so; the next firing retries.
- A stop skipped for `Retain-Until` is not retried; the idle sweep ends the instance later as usual.
- Existing deployments need a bootstrap re-run for the new Lambda, URL and role.
- `remote/` is a public repo: the group name and prefixes are fixed strings, nothing account-specific is committed.

## Open Questions

None that change what gets built. Assumption recorded: scheduled stop pauses rather than terminates, and skips when `Retain-Until` is in the future.
