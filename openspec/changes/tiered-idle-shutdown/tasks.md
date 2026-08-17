## 1. Idle decision model

- [x] 1.1 Update `remote/lambda/shared/idle.ts` to add `STOP_RETENTION_MINUTES` input and return distinct `stop` vs `terminate` decisions
- [x] 1.2 Add `stopRetentionMinutes` to `IdleDecisionInput` and update `decideIdle` logic for stopped instances
- [x] 1.3 Update `remote/test/idle.test.ts` for two-stage decisions

## 2. Stop Lambda sweep

- [x] 2.1 Modify `remote/lambda/stop/index.ts` `idleCheck`: running → idle calls `stopInstance` and writes the `Stopped-At` tag; passes session start (`Started-At` ?? `LaunchTime`) to `decideIdle`
- [x] 2.2 Add `stopInstance` helper in `remote/lambda/shared/aws.ts` and export it
- [x] 2.3 Add `STOP_RETENTION_MINUTES` env var reading in stop Lambda
- [x] 2.4 Update the sweep to see `stopped` instances and terminate them once `Stopped-At` is older than retention (self-heal a missing tag with now and a warning log)

## 3. Start Lambda re-wake

- [x] 3.1 Update `remote/lambda/start/index.ts` to re-wake an existing instance in state `stopped`: call `startInstance`, write the `Started-At` tag, then continue through the existing phase polling
- [x] 3.2 Adjust start state handling: `stopping` polls for the transition, `shutting-down`/`terminated` fail with a retryable 503, absent launches new
- [x] 3.3 Update start tests for the re-wake path and the transient-state handling

## 4. AWS helpers

- [x] 4.1 Add `stopInstance` and `startInstance` wrappers in `remote/lambda/shared/aws.ts`
- [x] 4.2 Expose `stoppedAt` / `startedAt` on `InstanceInfo` (parsed from `Stopped-At` / `Started-At` tags) and include `stopped` in the `findManagedInstance(s)` state filter

## 5. Configuration and deployment

- [x] 5.1 Document new env vars `STOP_RETENTION_MINUTES` in remote deployment README
- [x] 5.2 Update CDK stacks to set default `STOP_RETENTION_MINUTES` and pass to Lambdas
- [x] 5.3 Update `remote/Outfit` documentation for tiered idle behavior

## 6. Validation

- [x] 6.1 Run `pnpm test` for remote/ Lambda tests
- [x] 6.2 Validate spec changes with `openspec validate --change tiered-idle-shutdown`

## 7. Pause command

- [x] 7.1 Add `pause` subcommand handling in `cmd/outfit/remote.go` for `outfit remote pause`
- [x] 7.2 Extend the stop Lambda with a pause mode: write the `Stopped-At` tag, then `stopInstance` (manual stop stays `terminateInstance`)
- [x] 7.3 Add tests for pause vs stop semantics and status reporting
