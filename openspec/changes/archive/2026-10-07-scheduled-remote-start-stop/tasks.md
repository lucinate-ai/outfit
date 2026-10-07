## 1. Control plane: schedule model

- [x] 1.1 Add `remote/lambda/shared/schedules.ts`: types, validation (action, zone, env name), five-field cron → Scheduler cron conversion, next-run calculation
- [x] 1.2 Table tests for conversion and validation, including day-field `?` rules, rejected expressions, and daylight saving next-run

## 2. Control plane: schedule Lambda

- [x] 2.1 Add `remote/lambda/schedule/index.ts`: `PUT` replaces, `GET` reads (with next runs), `DELETE` clears; stores the list in `/cloud-vm-llm/<env>/schedules`
- [x] 2.2 Reconcile the Scheduler schedules in the group to the stored list (create, update, delete by environment prefix)
- [x] 2.3 Tests with mocked SSM and Scheduler clients: replace, clear, invalid input leaves state unchanged, other environments untouched

## 3. Control plane: start and stop

- [x] 3.1 Start Lambda: handle a `spinloop.schedule` event for `start`, reusing the on-demand start
- [x] 3.2 Stop Lambda: handle a `spinloop.schedule` event for `stop`, pausing, skipping when `Retain-Until` is in the future
- [x] 3.3 Tests for the running, stopped, retained and no-instance cases

## 4. Stack

- [x] 4.1 Add the schedule Lambda, its Function URL, the Scheduler group, and the role Scheduler assumes to invoke start and stop
- [x] 4.2 Grant the schedule Lambda SSM read/write on the schedules parameter and Scheduler permissions limited to the group, plus `iam:PassRole` for the Scheduler role
- [x] 4.3 Add the stack output and the `schedule_url` field in `SpinloopRemoteConfig`, and let the remote CLI user invoke the URL
- [x] 4.4 Stack test (assertions on the template) and run `scripts/check-no-cloud-identifiers.sh`

## 5. CLI client

- [x] 5.1 Add `schedule_url` to the remote config, loading and bootstrap output parsing
- [x] 5.2 Add `SetSchedules`, `GetSchedules`, `ClearSchedules` in `internal/remote`, with the "re-run bootstrap" error when the URL is missing
- [x] 5.3 Print next start / next stop from `schedule show` and `set`, omitting an action with no next run
- [x] 5.4 Add `spinloop remote schedule set|show|clear` with `--start`, `--stop`, `--timezone`, `--env`, plus tab completion
- [x] 5.5 Unit tests for the client calls, the command's argument handling and the status lines

## 6. Documentation and specs

- [x] 6.1 Update `docs/commands`, the remote guide, the README and `remote/README.md`
- [x] 6.2 Add a note to `docs/maintainer/internals.md` on the cron conversion and the Scheduler/SSM split
- [x] 6.3 Run `go test ./...`, `go vet ./...`, `gofmt`, and `pnpm test` in `remote/`
