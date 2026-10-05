## Why

The `spinloop remote` commands call a control plane (Lambdas in the user's AWS account) that was deployed from a particular spinloop release. When the CLI is upgraded and the control plane is not, the two can disagree about request and response shapes, and nothing tells the user. Issue 213 asks for a warning on any cloud command when the versions differ, without an extra API call.

## What Changes

- Every response from a control plane Lambda carries the control plane's version in a response header, `x-spinloop-control-plane-version`.
- `spinloop remote bootstrap` stamps the control plane with the version of the CLI that deploys it, through a CDK context value that becomes a Lambda environment variable.
- The CLI reads that header on every control plane call and, when it differs from the CLI's own version, prints one warning to stderr per process naming both versions and the fix (`spinloop remote bootstrap`).
- No warning when the header is absent (a control plane that predates this change) or when either side is a development build.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `remote-version-reporting`: adds the control plane version header and the CLI's mismatch warning.
- `endpoint-provisioning`: bootstrap records the deploying CLI's version in the control plane.

## Impact

- `remote/lib/llm-stack.ts`, `remote/lib/config.ts`, `remote/lambda/shared/http.ts` (CDK and Lambda response helper).
- `internal/remote/remote.go` (read the header in `send` and `callStats`), `cmd/spinloop/remote*.go` (print the warning, pass the version into bootstrap).
- Docs for `spinloop remote` and the `remote/` README.
- No new API calls and no change to response bodies.
