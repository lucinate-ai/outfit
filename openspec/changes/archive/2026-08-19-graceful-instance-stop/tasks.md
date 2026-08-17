## 1. Shared daemon stop command

- [x] 1.1 Add `DAEMON_STOP_CMD` constant to `remote/lambda/shared/daemon.ts` — the SSM curl command for `POST /v1/stop`
- [x] 1.2 Add `stopEngineDaemon` helper to `remote/lambda/shared/aws.ts` — calls the daemon API via SSM, returns boolean (success/failure), best-effort semantics

## 2. Stop Lambda integration

- [x] 2.1 Call `stopEngineDaemon` in `pauseInstance` before `stopInstance` — stop the engine first, then EC2
- [x] 2.2 Call `stopEngineDaemon` in `idleCheck` before `stopInstance` — idle sweep stops engine first
- [x] 2.3 Call `stopEngineDaemon` in `manualStop` before `terminateInstance` — manual terminate also stops the engine first (the engine blocks EC2 terminate too)

## 3. Tests

- [x] 3.1 Add tests for `stopEngineDaemon` in `remote/test/stop.test.ts` — verify daemon stop is called, best-effort fallback when daemon is unreachable
- [x] 3.2 Update existing pause/terminate/idle tests to expect the daemon stop call before EC2 action
- [x] 3.3 Run full test suite (`pnpm test`, `pnpm build`) and verify all pass
