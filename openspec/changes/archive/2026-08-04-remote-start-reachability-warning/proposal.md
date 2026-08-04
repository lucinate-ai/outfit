## Why

After `outfit remote start` reports ready, inference traffic to the endpoint can still fail silently if the caller changed networks since deploy: the ingress security group admits only one CIDR, so a new address gets blocked. The control-plane calls succeed (they are SigV4 Lambda URLs), but the actual inference endpoint times out. The user's first sign is a hanging curl or a harness that can't connect.

## What Changes

- After `start` reports the endpoint is ready, perform a short TCP probe of the base URL
- If the probe fails, print a warning explaining that the endpoint is ready but unreachable from this network, with a command to re-admit
- The probe is a warning only — `start` still exits 0 and prints exports as normal
- The probe is skipped in `--dry-run` mode and not run during `status`

## Capabilities

### New Capabilities

- `remote-start-probe`: After a successful start, probe the endpoint's TCP reachability and warn if the control plane says ready but the network blocks inference traffic.

### Modified Capabilities

- `remote-endpoint`: The `start` subcommand gains a post-start reachability check. The existing behaviour (blocking until ready, printing exports) is unchanged; the probe runs after success and only adds a warning to stderr.

## Impact

- `cmd/outfit/remote.go` — `cmdRemoteStart` gains the post-start probe call
- `internal/remote/remote.go` — new `ProbeReachability` function for the TCP check
- `cmd/outfit/remote_test.go` — tests for the probe path and warning output
- `internal/remote/remote_test.go` — unit tests for `ProbeReachability`
- No new dependencies; uses stdlib `net` for TCP dial
