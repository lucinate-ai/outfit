## 1. Idle decision model

- [ ] 1.1 Update `remote/lambda/shared/idle.ts` to add `STOP_RETENTION_MINUTES` input and return distinct `stop` vs `terminate` decisions
- [ ] 1.2 Add `stopRetentionMinutes` to `IdleDecisionInput` and update `decideIdle` logic for stopped instances
- [ ] 1.3 Update `remote/test/idle.test.ts` for two-stage decisions

## 2. Stop Lambda sweep

- [ ] 2.1 Modify `remote/lambda/stop/index.ts` `idleCheck` to handle stopped instances and call `stopInstance` instead of `terminateInstance` for running → idle
- [ ] 2.2 Add `stopInstance` helper in `remote/lambda/shared/aws.ts` and export it
- [ ] 2.3 Add `STOP_RETENTION_MINUTES` env var reading in stop Lambda
- [ ] 2.4 Update idle sweep to terminate stopped instances older than retention

## 3. Start Lambda re-wake

- [ ] 3.1 Update `remote/lambda/start/index.ts` to detect existing instance state `stopped` and call EC2 `startInstances` before launching new
- [ ] 3.2 Adjust terminal states to treat `stopped` as re-wakable, not terminal
- [ ] 3.3 Update start tests for stopped re-wake scenario

## 4. AWS helpers

- [ ] 4.1 Add `stopInstance` and `startInstance` wrappers in `remote/lambda/shared/aws.ts`
- [ ] 4.2 Expose `stoppedTime` from `InstanceInfo` in `findManagedInstance(s)`

## 5. Configuration and deployment

- [ ] 5.1 Document new env vars `STOP_RETENTION_MINUTES` in remote deployment README
- [ ] 5.2 Update CDK stacks to set default `STOP_RETENTION_MINUTES` and pass to Lambdas
- [ ] 5.3 Update `remote/Outfit` documentation for tiered idle behavior

## 6. Validation

- [ ] 6.1 Run `pnpm test` for remote/ Lambda tests
- [ ] 6.2 Validate spec changes with `openspec validate --change tiered-idle-shutdown`

## 7. Pause command

- [ ] 7.1 Add `pause` subcommand handling in `cmd/outfit/remote.go` for `outfit remote pause`
- [ ] 7.2 Extend stop Lambda to accept pause mode and call `stopInstance` instead of `terminateInstance`
- [ ] 7.3 Add tests for pause vs stop semantics and status reporting
