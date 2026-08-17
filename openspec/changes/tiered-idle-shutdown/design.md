## Context

The remote control plane currently terminates an EC2 instance as soon as the idle check decides to stop it. The stop Lambda's `decideIdle` returns `action: 'stop'` which is implemented as `terminateInstance`. The start Lambda treats a stopped instance as terminal and errors instead of re-waking it.

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
- Extend `IdleDecision` to distinguish `stop` (EC2 stop) from `terminate`. Keep `decideIdle` for the first stage (running → stopped). A second check for stopped instances uses `stoppedSince` to decide termination.
- Alternative: a single decision with a `stop` action that later becomes termination. Rejected because it conflates policy with state.

**Instance start handling**
- `start/index.ts` will check existing instance state. If state is `stopped`, call EC2 `startInstances` and wait for `running`, then continue to EIP association and health checks. If state is `terminated` or absent, launch new instance as before.
- Alternative: always terminate stopped instances before launching new ones. Rejected because it loses fast re-wake benefit.

**Configuration**
- Add two env vars to the stop/start Lambdas: `STOP_RETENTION_MINUTES` (how long a stopped instance is kept before termination) and `IDLE_THRESHOLD_MINUTES` remains for running → stopped. Existing `IDLE_THRESHOLD_MINUTES`, `GRACE_PERIOD_MINUTES`, `MAX_RUNTIME_MINUTES` stay.
- Alternative: reuse idle threshold for both stages. Rejected because stop retention should be independent.

**Termination of stopped instances**
- The idle sweep will process both running and stopped instances. For running, use existing `decideIdle`. For stopped, if `stoppedSince` > `STOP_RETENTION_MINUTES` and retain-until not in future, call `terminateInstance`.
- Alternative: a separate timer. Rejected to keep single sweep.

**Pause command handling**
- `outfit remote pause` invokes the stop Lambda in pause mode, calling EC2 `stopInstances` instead of `terminateInstance`. Manual `outfit remote stop` remains immediate termination.
- Alternative: a separate pause Lambda. Rejected to keep control plane surface minimal and reuse auth/validation.

## Risks / Trade-offs

[Cost of stopped instances] → Stopped EC2 still incurs EBS charges. Mitigation: `STOP_RETENTION_MINUTES` defaults to a few hours, balancing fast re-wake vs cost.

[State drift on launchTime] → EC2 `LaunchTime` resets on stop/start? It does not; use instance `stoppedTime` field. Mitigation: track `stoppedSince` from EC2 `stateTransitionReason` or store timestamp on first stop.

[Manual stop expectations] → Users may expect stop to stop, not terminate. Mitigation: manual stop remains terminate; CLI `stop` is explicit termination. Document change.

## Migration Plan

1. Update remote/ Lambda code and tests.
2. Deploy control plane with new env vars; existing instances continue to be terminated until first sweep.
3. Re-bake runtime AMIs is not required for this change.
4. Rollback: revert Lambda code and env vars; stopped instances will be terminated on next sweep.
