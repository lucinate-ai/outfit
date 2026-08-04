## Context

`outfit remote start` calls the Lambda Function URL to boot the instance and blocks until it reports ready. The control plane uses IAM-authenticated Lambda URLs that work from any network. However, the actual inference endpoint (HTTP on the instance's Elastic IP) is protected by a security group that admits only one CIDR set at deploy time. After changing networks, start succeeds but inference traffic is silently blocked.

The deploy flow already has `detectPublicCIDR` (calls `checkip.amazonaws.com`) to determine the caller's public IP for ingress setup. This same helper can identify the caller's current address for the warning message.

## Goals / Non-Goals

**Goals:**
- Detect when the endpoint is ready but unreachable from the caller's network
- Print a clear warning with a remediation command
- Keep the probe fast and non-blocking (short timeout)
- Preserve existing start behaviour — the probe only adds a warning

**Non-Goals:**
- Fixing the ingress automatically (that requires deploy with `--overwrite`)
- Probing during `status` or any other subcommand
- Making the probe failure an error exit
- Supporting IPv6 (the EIP is IPv4-only)

## Decisions

**TCP dial vs HTTP request.** A simple TCP dial to the endpoint port is sufficient and faster than an HTTP round-trip. The inference server listens on port 8000 (from the `base_url`), so dialing `host:8000` tells us whether the security group blocks us. An HTTP request would add latency for no gain — we only care whether the connection reaches the instance at all.

**Probe timeout of 5 seconds.** Long enough to allow for a normal connection on a slow network, short enough not to add perceptible delay to the start command. The variable is testable.

**Warning on stderr, not stdout.** Start progress already goes to stderr, and exports go to stdout for eval. The warning follows the same pattern so `outfit remote start` output remains parseable.

**Probe only after successful start, not on `--env`.** The `--env`/`-e` flag fetches exports from the env Lambda separately. The probe runs on the normal start success path; when `--env` is used, the start itself already completed and the probe already ran.

**Reuse `detectPublicCIDR` for the hint.** The deploy flow already calls `checkip.amazonaws.com` to detect the caller's public IP. The same call in the start flow produces the CIDR for the re-admit command. The variable `detectPublicCIDRFn` is already testable. To keep it fast, the probe runs first (TCP dial is quicker than an HTTPS call to Amazon); the IP detection only runs if the probe fails.

**Probe is in `internal/remote` for testability.** The TCP probe is a network operation that should be unit-testable without the CLI layer. Placing it in `internal/remote` keeps it alongside the other control-plane calls and allows injection via package-level variables for testing.

## Risks / Trade-offs

**Probe adds latency on failure.** If the network is unreachable, the 5-second dial timeout adds to the total command time. Mitigation: the timeout is short and only runs after start succeeds (which already takes minutes from cold).

**False positive when the instance is briefly slow to accept connections.** The inference server might not be listening on port 8000 immediately after the Lambda reports "ready". Mitigation: the probe runs once after start, and the control plane's "ready" state already means the server is configured. If the probe fails spuriously, the user can simply run `outfit remote deploy --overwrite --allowed-cidr` to verify.

**checkip.amazonaws.com could be slow or fail.** The IP detection call is only made when the probe fails, so it does not add latency in the normal case. If it fails, the hint uses a placeholder rather than failing the command.

## Migration Plan

No migration needed. This is additive: existing start behaviour is unchanged, and the probe only adds a stderr warning.

## Open Questions

None — the design is straightforward and low-risk.
