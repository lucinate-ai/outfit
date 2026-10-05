## 1. The lock

- [x] 1.1 Add `remote/lambda/shared/wake-lock.ts`: the parameter name, the value format, `acquireWakeLock(env, owner, expiresAt)` (create if absent, take over an expired or unreadable one, report held), and `releaseWakeLock(env, owner)` (delete only when the owner matches)
- [x] 1.2 Tests with a mocked SSM client: acquire when free, refuse when held, take over when expired, take over when malformed, release only own lock, error other than "already exists" is surfaced

## 2. The start Lambda

- [x] 2.1 Split `wake()` into a wrapper and `wakeLocked()`: the wrapper runs the unconfigured and undeployed checks, takes the lock, calls `wakeLocked()` and releases in a `finally`
- [x] 2.2 Reply 503 `starting` with `retry_after_seconds: 15` and the `retry-after` header when the lock is held, and when the lock cannot be taken for another reason (logged, nothing launched)
- [x] 2.3 Set the lock's expiry from the invocation's remaining time plus 30 seconds
- [x] 2.4 Check the instance state on each iteration of the agent-online, daemon-answering and health loops and in the first loop once the start command was issued; on `stopped`, `stopping`, `shutting-down` or `terminated` return 503 with the observed state and `retry_after_seconds: 15` (tolerating a lookup that fails), leaving the first loop's existing pre-start behaviour as it is
- [x] 2.5 Tests through the handler: two concurrent starts launch once; a refused start launches and looks up nothing; the retry finds the instance; the lock is released after ready, a retryable refusal, a terminal state and a thrown error, and only when still held by this start; different environments do not block each other; an expired lock is taken over and a valid one is not; a lock that cannot be written stops the start; a status read and a pause are not affected by a held lock
- [x] 2.6 Tests for a stop mid-start: stopped after the start command, right after a fresh launch, while waiting for the agent, terminated while the engine loads, each ends promptly with a retryable reply naming the state and releasing the lock; a lookup that fails keeps waiting; a `stopped` instance seen before the start command is still re-woken
- [x] 2.7 Add `wakeLockHeld(env)` to the lock module and make the GET status reply read it: `state` becomes `starting` while the lock is held and the instance is absent, stopped or pending, every reply carries `start_in_progress`, and a failure to read the lock is logged and treated as no start in progress
- [x] 2.8 Tests for status: a second client sees `starting` and the flag during another client's start; a running instance whose model is loading keeps `running` with the flag; no lock means no flag; an expired lock is ignored; an unreadable lock does not fail the read; a refused start's lock holder is unchanged by the read

## 3. Stack

- [x] 3.1 Grant the start Lambda's role `ssm:DeleteParameter` on `parameter/cloud-vm-llm/*/wake-lock` only
- [x] 3.2 Stack test: the delete grant exists, is scoped to the lock parameter, and no other Lambda has it; run `scripts/check-no-cloud-identifiers.sh`

## 4. Documentation

- [x] 4.1 Add a note to `docs/maintainer/internals.md` on the lock, its expiry, the takeover window and the status reading
- [x] 4.2 Note the lock, the "another start is in progress" reply and the `starting` state in `remote/README.md` and the troubleshooting page if it lists start replies
- [x] 4.3 Run `go test ./...`, `go vet ./...`, `gofmt`, `pnpm test` in `remote/`, `scripts/check-spec-purposes.sh` and `openspec validate wake-lock`
