## 1. Control plane: schedule model

- [ ] 1.1 Add `remote/lambda/shared/schedules.ts`: types, validation (action, zone, env name), five-field cron → Scheduler cron conversion, next-run calculation
- [ ] 1.2 Table tests for conversion and validation, including day-field `?` rules, rejected expressions, and daylight saving next-run

## 2. Control plane: schedule Lambda

- [ ] 2.1 Add `remote/lambda/schedule/index.ts`: `PUT` replaces, `GET` reads (with next runs), `DELETE` clears; stores the list in `/cloud-vm-llm/<env>/schedules`
- [ ] 2.2 Reconcile the Scheduler schedules in the group to the stored list (create, update, delete by environment prefix)
- [ ] 2.3 Tests with mocked SSM and Scheduler clients: replace, clear, invalid input leaves state unchanged, other environments untouched

## 3. Control plane: start and stop

- [ ] 3.1 Start Lambda: handle a `spinloop.schedule` event for `start`, reusing the on-demand start
- [ ] 3.2 Stop Lambda: handle a `spinloop.schedule` event for `stop`, pausing, skipping when `Retain-Until` is in the future
- [ ] 3.3 Tests for the running, stopped, retained and no-instance cases

## 4. Stack

- [ ] 4.1 Add the schedule Lambda, its Function URL, the Scheduler group, and the role Scheduler assumes to invoke start and stop
- [ ] 4.2 Grant the schedule Lambda SSM read/write on the schedules parameter and Scheduler permissions limited to the group, plus `iam:PassRole` for the Scheduler role
- [ ] 4.3 Add the stack output and the `schedule_url` field in `SpinloopRemoteConfig`, and let the remote CLI user invoke the URL
- [ ] 4.4 Stack test (assertions on the template) and run `scripts/check-no-cloud-identifiers.sh`

## 5. CLI client

- [ ] 5.1 Add `schedule_url` to the remote config, loading and bootstrap output parsing
- [ ] 5.2 Add `SetSchedules`, `GetSchedules`, `ClearSchedules` in `internal/remote`, with the "re-run bootstrap" error when the URL is missing
- [ ] 5.3 Add next start / next stop to the status response and the `remote status` output
- [ ] 5.4 Add `spinloop remote schedule set|show|clear` with `--start`, `--stop`, `--timezone`, `--env`, plus tab completion
- [ ] 5.5 Unit tests for the client calls, the command's argument handling and the status lines

## 6. Documentation and specs

- [ ] 6.1 Update `docs/commands`, the remote guide and the control-plane HTTP API page
- [ ] 6.2 Add a note to `docs/maintainer/internals.md` on the cron conversion and the Scheduler/SSM split
- [ ] 6.3 Run `go test ./...`, `go vet ./...`, `gofmt`, and `pnpm test` in `remote/`
