## Why

The `outfit remote` control commands (`deploy`, `start`, `stop`, `status`) read
only the real process environment, so the `.env` beside an Outfit — the file
local commands already use for provider keys — is ignored. That environment
carries the AWS credentials the control calls are SigV4-signed with, the region,
and the `OUTFIT_REMOTE_*` overrides, so today an Outfit cannot travel with the
credentials it needs to reach its own deployment.

## What Changes

- The `remote` control commands SHALL load the `.env` beside the resolved Outfit
  into the process environment before any config or AWS work, so the AWS
  credential chain, `AWS_REGION`, and the `OUTFIT_REMOTE_*` overrides all see it.
  An already-set process-environment variable wins — the `.env` only fills gaps.
- A new repeatable **`ENV`** keyword in the Outfit file (`ENV KEY=VALUE`), whose
  values override both the `.env` and the process environment. Net precedence,
  highest to lowest: **`ENV` keyword → process environment → `.env`**.
- `ENV` is local-only: it applies on the device invoking `outfit` and SHALL NOT
  be sent to the deployed instance or included in the deploy payload.
- Scope: only the `remote` control commands apply this loading for now. The
  provider-key path (`apply`/`serve`/`harness`) is unchanged; its existing
  `.env`-before-process-environment precedence (in `provider-selection`) is
  deliberately left as-is and tracked as a separate defect to reconcile later.

## Capabilities

### New Capabilities
- `remote-local-environment`: how the `remote` control commands establish the
  local process environment from the Outfit's adjacent `.env` and `ENV` lines —
  the precedence (`ENV` > process environment > `.env`), that it happens before
  any AWS/control work, and that `ENV` is local-only and never reaches the
  deployed instance.

### Modified Capabilities
- `outfit-files`: the Outfit file format gains the `ENV` keyword — repeatable,
  taking a single `KEY=VALUE` token, parsed into the selection. (Its runtime
  semantics live in `remote-local-environment`.)

## Impact

- Code: `internal/outfit/outfit.go` (parser, `Selection`, `Format`);
  `internal/opencode/opencode.go` (whole-file `.env` parser alongside the
  existing per-key reader); `cmd/outfit/remote.go` (`resolveRemoteConfig`,
  `cmdRemoteDeploy`, and the start/stop/status handlers that route through it).
- No change to the deploy payload shape (`remote.DeployConfig`) — the local-only
  rule is preserved by construction.
- No new runtime dependencies (dotenv parsing stays hand-rolled).
- Known inconsistency held intentionally: `provider-selection` keeps `.env`-first
  precedence while `remote-local-environment` uses process-environment-first; a
  GitHub defect will track reconciling them.
