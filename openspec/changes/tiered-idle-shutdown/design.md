## Context

The remote control plane currently terminates an EC2 instance as soon as the idle check decides to stop it. The stop Lambda's `decideIdle` returns `action: 'stop'` which is implemented as `terminateInstance`. The start Lambda treats a stopped instance as terminal and errors instead of re-waking it. Two further constraints surfaced during implementation: `findManagedInstances` filters instance states to `pending`/`running`, so a stopped instance is invisible to both Lambdas; and EC2 exposes no stop time from `DescribeInstances`, so the stop-retention timer needs a timestamp the control plane owns.

## Goals / Non-Goals

**Goals:**
- Preserve instance boot disk and weights by stopping before terminating.
- Allow `outfit remote start` to re-wake a stopped instance in seconds.
- Provide explicit `outfit remote pause` for user-initiated stop without termination.
- Retain existing idle detection semantics, grace period, max runtime and retain-until override.
- Keep the shared idle sweep for all environments.

**Non-Goals:**
- Changing the daemon's activity sampling or status format.
- Modifying outfit Go CLI behavior beyond documenting start re-wake.
- Changing manual stop semantics — manual stop remains immediate termination.

## Decisions

**Two-stage idle decision**
- `decideIdle` takes the instance's state and returns distinct actions: `stop` (EC2 stop) for a running instance past its bounds, and `terminate` for a stopped instance past stop retention. Retain-until, grace period and max runtime bound the *running session*; a stopped instance is judged on stop retention alone (plus retain-until).
- Alternative: a single `stop` action that later becomes termination. Rejected because it conflates policy with state and forces callers to track which stage produced the action.

**Instance discovery**
- `findManagedInstances` gains `stopped` in its instance-state-name filter (`pending`, `running`, `stopped`), so the sweep can see stopped instances to terminate after retention, and the start Lambda's existing "existing instance" lookup finds the one to re-wake. `stopping`/`shutting-down` stay out: transient states where a second act is a footgun — the sweep skips them, the start Lambda waits them out.
- Alternative: a separate stopped-instance query. Rejected — one discovery path is the single place both Lambdas agree on what exists.

**Stop time: a `Stopped-At` tag**
- EC2 exposes no stop time from `DescribeInstances`, so the control plane writes the stop time on the instance as a tag (`Stopped-At`, ISO-8601 UTC) — the same mechanism as the existing `Retain-Until` tag. Both the idle sweep and a manual pause write it as they issue the stop.
- A stopped instance without the tag (stopped outside the control plane, or a Lambda crash between the EC2 call and the tag write) self-heals: the next sweep tags it with now and warns, so it gets the full retention instead of an immediate death.
- Alternative: an SSM parameter per environment. Rejected — a tag travels with the instance, is visible in the console, and cannot outlive it by accident.

**Session start and max runtime**
- A stop→start cycle keeps the same instance, so max runtime and the grace period must measure the *current session*, not first boot. The start Lambda writes a `Started-At` tag when it re-wakes a stopped instance; the sweep computes `sessionStart = Started-At ?? LaunchTime` and passes that as the session origin to `decideIdle`.
- Correctness no longer depends on how EC2 reports `LaunchTime` across stop/start cycles.
- Alternative: trust `LaunchTime` or the daemon's uptime. Rejected — neither is guaranteed per-session; daemon uptime also resets on crash-restarts inside one session.

**Instance start handling**
- `start/index.ts` checks the existing instance state: `stopped` → `startInstances`, write the `Started-At` tag, then continue through the existing phase polling (EIP association, SSM agent, health); `stopping` → keep polling for the transition; `shutting-down`/`terminated` → fail with a retryable 503 so a retry launches fresh; absent → launch new as before.
- The re-wake needs no new boot path for weights — the persistent root volume already holds them, so the instance boots fast — but it **does** need an engine start from the control plane: user data is not re-run on a stop→start cycle, so the boot script's start request never fires, and the baked crash-nudge timer only acts on a `crashed` engine, never on the daemon's fresh `idle` state. The start Lambda therefore asks the daemon for an engine start (same loopback `/v1/start` API the stop side uses) once the SSM agent is online. The ask is idempotent — the daemon 409s when an engine already runs — so the same call is harmless on a fresh launch before its boot script requests one.

**Configuration**
- One new env var on the stop Lambda: `STOP_RETENTION_MINUTES` (how long a stopped instance is kept before termination). `IDLE_THRESHOLD_MINUTES`, `GRACE_PERIOD_MINUTES` and `MAX_RUNTIME_MINUTES` stay unchanged.
- Alternative: reuse the idle threshold for both stages. Rejected — the retention trade-off (EBS cost vs re-wake speed) is independent of GPU-idle cost.

**Pause command handling**
- `outfit remote pause` invokes the stop Lambda in pause mode: write the `Stopped-At` tag, then `stopInstances`. Manual `outfit remote stop` remains immediate termination.
- Alternative: a separate pause Lambda. Rejected to keep control plane surface minimal and reuse auth/validation.

## Risks / Trade-offs

[Cost of stopped instances] → A stopped EC2 instance still bills its root volume (and the idle EIP). Mitigation: `STOP_RETENTION_MINUTES` is configurable; its default balances fast re-wake against storage cost.

[Missing stop tag after a crash] → A Lambda crash between the EC2 stop call and the tag write leaves a stopped instance with no `Stopped-At`. Mitigation: the sweep self-heals with now plus a warning log — the instance buys its full retention rather than being killed early; a few hours of storage on a rare crash path is acceptable.

[A paused instance is indistinguishable from an idle-stopped one] → The sweep will terminate a paused instance after retention, like any other stopped one. That is the intent (pause is "stop now, please"), and the `Retain-Until` override already exists to pin a paused instance.

[Manual stop expectations] → Users may expect `stop` to stop, not terminate. Mitigation: the CLI makes the distinction explicit — `stop` is deliberate termination, `pause` is deliberate stopping — and both are documented in the command help.

## Migration Plan

1. Update remote/ Lambda code and tests.
2. Deploy control plane with new env vars; existing instances continue to be terminated until first sweep.
3. Re-bake runtime AMIs is not required for this change.
4. Rollback: revert Lambda code and env vars; stopped instances will be terminated on next sweep.
