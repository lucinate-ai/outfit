## Context

The daemon runs `outfit daemon --api-addr 127.0.0.1:4242` on every instance, with a SIGTERM handler that calls `sup.Stop()` — which SIGTERMs the engine process group (SIGKILL after 10s grace). When the Lambda calls EC2 `StopInstances`, the engine blocks the instance shutdown (it's a long-running process that doesn't respond to SIGTERM during EC2's shutdown sequence), leaving the instance stuck in `stopping` for up to 12 minutes before EC2 force-kills it.

The daemon already exposes `POST /v1/stop` on its control API (loopback, no token), which the supervisor's `Stop()` method handles by terminating the engine process group.

## Goals / Non-Goals

**Goals:**
- Stop the engine via the daemon API before calling EC2 `StopInstances`, so the instance shuts down promptly
- Work for any supported engine (llama.cpp, vLLM, or future runners) — no engine-specific logic in the Lambda
- Be resilient: if the daemon is unreachable, still stop the EC2 instance

**Non-Goals:**
- Changing how the daemon itself handles engine shutdown (the existing signal handler + supervisor is sufficient)
- Adding engine-specific stop logic in the Lambda (the daemon handles that)
- Modifying the boot script or systemd unit (the daemon already handles SIGTERM on its own)

## Decisions

**Call the daemon API, not SIGTERM on the engine.** The Lambda already uses SSM to reach the daemon for idle checks (`GET /v1/status`). Using the same mechanism for `POST /v1/stop` keeps the Lambda engine-agnostic: it asks the daemon to shut down whatever engine is running, and the daemon's supervisor handles the rest.

**Best-effort, not blocking.** The daemon might be unreachable if it hasn't started yet, crashed, or if the instance is in an inconsistent state. The Lambda should still stop the EC2 instance rather than failing the operation — the existing force-kill behavior is the fallback.

**Unified helper in shared/aws.ts.** A `stopEngineDaemon` function wraps the SSM call to `POST /v1/stop`, used by `pauseInstance`, `idleCheck`, and `manualStop`. This avoids duplication and keeps the Lambda's stop paths coherent.

## Risks / Trade-offs

[SSM latency adds time to the stop] → Mitigation: the call uses a short timeout (10s, matching existing daemon calls), and is best-effort — if it doesn't answer, the Lambda proceeds. Total added latency in the happy path is one extra SSM round-trip (~2–3s).

[Daemon crash leaves the engine running anyway] → Mitigation: the EC2 stop still runs. The engine will block the stop as before, but at least the common case (daemon is alive) is fast. This is no worse than today.
