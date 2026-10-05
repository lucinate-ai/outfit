## Context

See proposal.md for why. `wake()` in `remote/lambda/start/index.ts` reads the deploy config, finds the environment's Elastic IP and security group, runs the weights check (which can start a seed), looks the instance up by tag, then launches or re-wakes it and polls until the model answers. That poll can run for the Lambda's whole 15-minute limit. The tag lookup is `DescribeInstances`, which is eventually consistent, so it is not a safe way to tell whether another start has already launched.

The Lambda's role already has `ssm:GetParameter` and `ssm:PutParameter` on `/cloud-vm-llm/*`. It has no `ssm:DeleteParameter`. Issue 224 says no new grant is needed; releasing a lock by deleting the parameter does need one.

## Goals / Non-Goals

**Goals:**
- Two starts for one environment never launch two instances, from any caller.
- A start that cannot take the lock does nothing but reply that it should retry.
- A crashed start cannot block an environment for longer than the time that start could have run.

**Non-Goals:**
- Serialising stops, pauses or the idle sweep against starts. Those race a start in a different way (a stop between a start's lookup and its poll is already handled in `wake()`) and are left as they are.
- Making the lock safe against two starts that both find the same expired lock in the same few milliseconds. See Risks.
- A queue, so that a refused start waits its turn on the server. The caller retries.
- Any change to the CLI or gateway.

## Decisions

**An SSM parameter as the lock.** `/cloud-vm-llm/<env>/wake-lock`, created with `Overwrite: false`, so creation fails with `ParameterAlreadyExists` when another start holds it. This needs no new infrastructure. DynamoDB with a conditional put would give true compare-and-set but adds a table, a construct and grants to a stack that has none today; the SSM lock is enough for the case that matters (starts racing within seconds of each other, where creation is atomic).

**What the lock holds.** A JSON value `{"owner": <Lambda request id>, "expiresAt": <ISO time>}`. The owner lets a release remove only a lock this start created. `expiresAt` is now plus the time remaining on this invocation plus a 30 second margin, so a lock lives exactly as long as the start that took it could run. A fixed TTL longer than the 900 second limit would leave a killed start's environment blocked for longer than needed.

**Where it is taken and released.** `wake()` keeps its signature and becomes a wrapper: it runs the existing read-only checks that reply "unconfigured" and "undeployed" (they change nothing, so they need no lock), takes the lock, runs the rest of the existing body as `wakeLocked()`, and releases the lock in a `finally`. The lock is taken before the weights check, because that check can start a seed and two starts racing there could start two. The lock is held through the whole poll, not only the launch: the lookup cannot be trusted to see a just-launched instance for a while, so releasing at launch would reopen the race.

**What a refused start returns.** HTTP 503 with state `starting`, a message saying another start for the environment is in progress, and `retry_after_seconds: 15` with the matching `retry-after` header. The Go client already retries any 503 using `retry_after_seconds` and prints "instance starting; retrying in 15s", and the gateway wake path treats a 503 the same way, so neither changes. When the holder finishes, the retry finds the running instance and returns ready.

**Taking over an expired lock.** When creation fails because the parameter exists, the start reads it. If `expiresAt` is in the future, it replies as above. If it has passed, the start reads the lock again, checks the owner and expiry are unchanged, deletes it, and tries the create-if-absent again; whichever start's create succeeds holds the lock, and the other replies as above. An unreadable or malformed lock value counts as expired, since nothing else could ever clear it.

**Failures other than "already exists".** Any other error creating the lock is logged and answered with a 503 `starting`, retryable, with nothing launched. A failure releasing the lock is logged and ignored: the reply to the caller is already decided, and the lock expires on its own.

**IAM.** One statement on the start Lambda's role: `ssm:DeleteParameter` on `parameter/cloud-vm-llm/*/wake-lock`. Create and read use the existing grants.

## Risks / Trade-offs

- **Two starts taking over the same expired lock together.** SSM has no compare-and-set, so between one start's re-read and its delete, another can delete and re-create the lock, and the first then deletes the new one. This needs an abandoned lock (a killed start) and two starts arriving within milliseconds after its expiry. The result is the old race for that one start, no worse than today. A DynamoDB lock would remove it; this change accepts it and records it.
- **A killed start blocks the environment until the lock expires.** At most the remaining time of that invocation plus 30 seconds, which is at most about 15 minutes. Callers see "another start is in progress" for that time.
- **A refused start does not wait on the server.** Callers poll every 15 seconds, so a start can learn the instance is ready up to 15 seconds late.
- **Deployments that have not taken the new grant.** The start cannot delete its lock, so a start would hold the environment until expiry. The release failure is logged. Deploying the stack fixes it; the proposal says so.

## Open Questions

None that change what gets built.
