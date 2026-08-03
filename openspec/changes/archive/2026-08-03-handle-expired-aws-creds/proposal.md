## Why

`outfit remote status <outfit>` with expired AWS credentials prints a blank
line and exits 0, giving no hint that anything is wrong. The control Lambda
rejects the request with HTTP 403 and a JSON body such as
`{"message":"The security token included in the request is expired"}`; that
body parses cleanly into an empty response, so the command reports an empty
state instead of an error. A user cannot tell a broken command from a stopped
endpoint, and the fix (refresh your credentials) is never surfaced.

## What Changes

- Treat a non-2xx reply from the control Lambdas as a failure for **every**
  remote subcommand. Today `status` and `stop` return whatever parsed, so a 403
  or 5xx is swallowed; `deploy`, `start`, and `stats` already re-check the
  status code, so the behaviour becomes consistent.
- Detect the expired- or invalid-credential signature — an authorizer 403, and
  the SDK markers `ExpiredToken` / `InvalidClientTokenId` / "security token ...
  expired" — and report one clear, actionable message telling the user to
  refresh their AWS credentials (env vars, profile, or SSO session), rather than
  the raw truncated Lambda body.
- Apply the detection in one place (`internal/remote`) so `status`, `stop`,
  `start`, `deploy`, and `stats` all benefit, and expired temporary credentials
  (which resolve fine and only fail at the API) are caught alongside the
  already-handled "no credentials resolved" case.

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities
- `remote-endpoint`: the "Authenticated control requests" requirement gains that
  a non-success control reply SHALL surface as an error (never blank output),
  and that a request rejected because the credentials are expired or invalid
  SHALL say to refresh them — distinct from the existing "lacks permission" and
  "no credentials resolved" cases.

## Impact

- `internal/remote/remote.go` — `call`, `Status`, `Stop`, and the shared 403
  hint / status-code handling; the expired-credential classifier.
- `internal/remote/remote_test.go` — cases for a 403 expired-token body on
  `status`/`stop` and the classified message.
- No new dependencies; no change to the request-signing path or stored config.
  Not a breaking change — commands that previously succeeded still succeed; only
  the silently-empty failures now report an error and non-zero exit.
