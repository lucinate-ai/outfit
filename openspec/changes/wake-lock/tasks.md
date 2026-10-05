## 1. The lock

- [ ] 1.1 Add `remote/lambda/shared/wake-lock.ts`: the parameter name, the value format, `acquireWakeLock(env, owner, expiresAt)` (create if absent, take over an expired or unreadable one, report held), and `releaseWakeLock(env, owner)` (delete only when the owner matches)
- [ ] 1.2 Tests with a mocked SSM client: acquire when free, refuse when held, take over when expired, take over when malformed, release only own lock, error other than "already exists" is surfaced

## 2. The start Lambda

- [ ] 2.1 Split `wake()` into a wrapper and `wakeLocked()`: the wrapper runs the unconfigured and undeployed checks, takes the lock, calls `wakeLocked()` and releases in a `finally`
- [ ] 2.2 Reply 503 `starting` with `retry_after_seconds: 15` and the `retry-after` header when the lock is held, and when the lock cannot be taken for another reason (logged, nothing launched)
- [ ] 2.3 Set the lock's expiry from the invocation's remaining time plus 30 seconds
- [ ] 2.4 Check the instance state on each iteration of the agent-online, daemon-answering and health loops and in the first loop once the start command was issued; on `stopped`, `stopping`, `shutting-down` or `terminated` return 503 with the observed state and `retry_after_seconds: 15` (tolerating `InvalidInstanceID.NotFound`), leaving the first loop's existing pre-start behaviour as it is
- [ ] 2.5 Tests through the handler: two concurrent starts launch once; a refused start launches and looks up nothing; the retry finds the instance; the lock is released after ready, no-capacity, terminal state, boot failure, deadline and a thrown error; different environments do not block each other; status does not take the lock; a scheduled start takes the lock
- [ ] 2.6 Tests for a stop mid-start: stopped after the start command, stopped while waiting for the agent, terminated while the engine loads, each ends promptly with a retryable reply naming the state and releasing the lock; a `stopped` instance seen before the start command is still re-woken; a stop request is not affected by a held lock

## 3. Stack

- [ ] 3.1 Grant the start Lambda's role `ssm:DeleteParameter` on `parameter/cloud-vm-llm/*/wake-lock` only
- [ ] 3.2 Stack test: the delete grant exists, is scoped to the lock parameter, and no other Lambda has it; run `scripts/check-no-cloud-identifiers.sh`

## 4. Documentation

- [ ] 4.1 Add a note to `docs/maintainer/internals.md` on the lock, its expiry and the takeover window
- [ ] 4.2 Note the lock and the "another start is in progress" reply in `remote/README.md` and the troubleshooting page if it lists start replies
- [ ] 4.3 Run `go test ./...`, `go vet ./...`, `gofmt`, `pnpm test` in `remote/`, `scripts/check-spec-purposes.sh` and `openspec validate wake-lock`
