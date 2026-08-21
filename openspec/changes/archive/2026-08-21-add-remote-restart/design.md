## Context

The remote endpoint already has every part a restart needs: the stop Lambda's
pause mode (EC2 stop, boot disk and weights preserved, replied to as
`stopping`/`stopped`), and the start Lambda's wake, which already treats a
`stopped` instance as the normal re-wake path and a `stopping` one as
transient — it polls until stopped, issues the re-wake itself, and blocks
until the model serves. The stop side today always asks the on-instance
daemon to stop the engine first (best-effort, `POST /v1/stop`); that is the
graceful step `--force` must be able to skip. See proposal.md for the
motivation.

Two constraints shape the approach. The wake is one long-lived blocking
request that can run for minutes, against the Lambda's own timeout budget;
stop is a short call. And the stop Lambda's modes are already selected by
query parameters on a shared Function URL (`action=pause` was added exactly
this way), so a parameter is the established way to vary its behaviour
without touching the environment's config or the CDK stack.

## Goals / Non-Goals

**Goals:**

- One command that returns the endpoint to serving with a fresh engine, on
  the fastest path (reuse the boot disk, keep the address stable).
- A way to stop the box when the engine or its daemon will not answer a
  polite stop.
- Reuse the wake's existing deadline, retry and re-wake-race handling rather
  than re-implementing it.
- Degrade gracefully: a new client against an old control plane still
  restarts (without force); a new control plane is invisible to old clients.

**Non-Goals:**

- Not a deploy: what the endpoint serves is untouched.
- No retention interplay: `restart` neither sets nor clears the Retain-Until
  tag (that is `keep`'s and `start --keep`'s job).
- No `--env` on `restart`: the address does not change, so there is nothing
  new to export.
- No new infrastructure: no new Lambda, no new URL, no config schema change.

## Decisions

**Client-side composition, not a combined control-plane action.** `restart`
is the stop call followed by the existing wake, both made by the CLI. The
alternative — an `action=restart` on the control plane that does both — was
rejected because the wake holds one long-lived request while the instance
boots (minutes); combined, the stop would spend the wake's timeout budget,
and the handler would have to duplicate the re-wake race handling the start
Lambda already owns. Composition reuses that handling for free, and the two
halves stay independently testable.

**The stop half is pause-style, never terminate.** A restart wants a fresh
engine, not a fresh machine: keeping the boot disk means the re-wake loads
weights that are already synced, and the address is stable by construction.
The alternative — terminate and launch — is already available as
`stop && start`, and is much slower. A consequence worth noting: the stop
reply (`stopping`) needs no separate wait. The wake's own polling already
walks `stopping` → `stopped` and issues the re-wake at the right moment, so
the client does not add a wait loop.

**`force` is a query parameter on the existing stop endpoint, honoured in
both manual modes.** `force=true` (exact value, matching the existing
`action === 'pause'` style) skips the daemon engine-stop step; the stop-time
tag, the EC2 call and the reply are unchanged in both pause and terminate
modes. The idle sweep never passes the parameter. Alternatives: a second
Lambda or URL (rejected — a config schema change and an environment
re-deploy for one flag); pausing the parameter to pause mode only (rejected —
a mode-dependent meaning of one flag is a trap, and the symmetric form is a
one-line gate per path). The parameter signs as part of the query string
under SigV4, so nothing changes in the signing code.

**The pairing lives in `internal/remote`.** A `Restart` function there makes
the pause-style stop (with force when requested) and then calls the existing
`Start`, passing the caller's progress and state observers and no retention.
Stop failing means the wake never starts; the wake failing after the stop
took effect yields an error that says the instance is stopped and that
`outfit remote start` will bring it back — the state is exactly what a
manual pause leaves behind. Keeping the pairing in the transport package
(rather than composing two calls in the command) puts the error
classification beside the retry/deadline semantics it belongs with, and
keeps the command body to progress and output.

**`Pause` gains a force argument rather than a sibling function.** There is
one caller (`outfit remote pause`), so `Pause(ctx, cfg, force)` with the URL
builder appending `force=true` when set is simpler than a near-duplicate
`ForcePause`.

**Progress and output.** `restart` reuses the start progress heartbeat for
the wait, with one line up front identifying the stop phase (and, under
`force`, that the graceful engine stop is being skipped), then the wake's
own per-poll notices. On success it reports the elapsed time and the base
URL — confirmation that it is the same address. A lenient `status` call
first prints whether the instance was running or already stopped; it is
output-only — the stop Lambda is correct for every state (running, stopped,
absent) — so a failed status check does not gate the restart. Flags:
`--force`/`-F` and `--timeout`/`-t` (15m default, as `start`). Unlike a
fresh `start`, there is no reachability probe: the address is unchanged, so
a network that admitted the endpoint before admitted it throughout the
restart.

## Risks / Trade-offs

- [The wake fails after the stop succeeded, leaving the endpoint down] → the
  error names the state and the one command that recovers it (`outfit remote
  start`); that state is otherwise reached by a plain `pause`, so there is
  no new way to strand an endpoint.
- [`--force` kills in-flight requests] → intentional and opt-in; the progress
  line says the graceful stop was skipped, and a polite stop is the default.
- [A new client against an old control plane] → `force=true` is silently
  ignored by Lambda code that predates it: the restart still happens, just
  with the graceful stop. No failure mode, only a slower stop of the
  wedged-engine case the flag exists for.
- [The idle sweep stops the instance between the stop call and the wake] →
  the wake already handles an instance found `stopped` after discovery
  (it issues the re-wake itself), so the race resolves to the same outcome.
- [Force is useless when the SSM agent is the thing that is wedged] → the
  EC2 stop does not go through SSM, which is precisely why skipping the
  daemon step still brings the box down; worst case is the ordinary EC2
  stop latency.

## Migration Plan

1. Ship the control plane side: the stop Lambda honours `force`. This is a
   Lambda code update only — no CDK infrastructure diff, no environment
   config change, nothing in any `remote.json`.
2. Ship the CLI side: the `restart` subcommand and the `Pause`/`Restart`
   transport functions.
3. Order between the two is safe in either direction (see the old-control-
   plane degradation above). Rollback is reverting either side; `force`
   simply degrades to a graceful stop against an old Lambda, and an old CLI
   against a new Lambda simply never sends the parameter.
