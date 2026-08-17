## Why

Remote inference instances are currently terminated immediately when idle. Termination destroys the instance and its local weights cache, so a subsequent start requires a full boot and weight sync, increasing cold-start latency and cost. Stopping the instance first preserves the boot disk and weights, allowing a fast re-wake, while still allowing eventual cleanup.

## What Changes

- Idle idle check now performs a two-stage shutdown: stop the EC2 instance for fast re-wake, then terminate after a further idle period.
- `outfit remote start` must be able to re-wake a stopped instance instead of launching a new one.
- Manual `outfit remote stop` remains immediate termination for explicit user intent.
- New configuration parameters control the stop-after-idle threshold and the terminate-after-stop threshold.
- The control plane idle sweep tracks instance state to apply the tiered policy.

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities
- `endpoint-lifecycle`: change the "Stopping when unused" requirement from immediate termination to tiered stop-then-terminate, and update bounds precedence to include stopped-state retention.

## Impact

- Affected code: `remote/lambda/shared/idle.ts`, `remote/lambda/stop/index.ts`, `remote/lambda/start/index.ts`, `remote/lambda/shared/aws.ts` (stopInstance API).
- Affected spec: `openspec/specs/endpoint-lifecycle/spec.md`.
- Affected CLI behavior: `outfit remote start` will start stopped instances; `outfit remote status` will report stopped state.
- No changes to outfit Go code required for the control plane behavior, but remote deployment outputs may need new env vars.
