## Why

A node currently decides two things about its own engine that the client is
better placed to decide, and the split shows up as behaviour nobody would
choose deliberately.

**The engine's key comes from whichever end happens to supply it.** A daemon
started from its own Outfit gates its engine with whatever that machine's
preset says; a node woken by routing gets no key at all, because the derivation
drops `api-key` — so a preset that says "gate this engine" is silently ignored.
Meanwhile the client, which needs the key to hand to the agent, learns it from
a third place: `engineTokenEnv` in its own `fleet.yaml`. Two ends hold the same
secret independently and nothing reconciles them, so a mismatch surfaces as a
401 on the agent's first request.

**A node holds workload configuration it has no business holding.** `outfit
daemon` resolves an Outfit (and its preset, and `OUTFIT_ALIAS`) so it can serve
something without being asked. Nothing in production uses this: the cloud runs
`outfit daemon --api-addr 127.0.0.1:4242` with no path and is driven entirely by
pushed configs. What it does produce is confusion — a node's Outfit and a
client's Outfit are different documents with different jobs, and the fleet
examples had to grow a paragraph explaining why they cannot be the same file.

The remote path already resolved both of these: the control plane holds the key,
tells the instance what to run, and hands the key to the caller. This makes a
fleet node work the same way, with the client in the control plane's seat.

## What Changes

- **A start request carries the engine's API key.** `POST /v1/start` accepts one
  alongside the deploy config, and the daemon starts the engine gated with it.
  The client already holds the node's bearer token, so it is already trusted to
  run arbitrary engine commands there; supplying a key is strictly less than
  that.
- The daemon SHALL write the key to a file and pass `--api-key-file`, never
  `--api-key`, so the secret is not visible in `ps` to every local user. This is
  how the cloud already delivers it.
- Routing supplies the key on the implicit start too, so a woken node is gated
  exactly as an explicitly started one is. The client knows the key it just set,
  so it hands that to the launched agent rather than resolving one separately.
- `engineTokenEnv` stops meaning "the key the node was gated with, told to you
  out of band" and starts meaning "where the client looks up the key it will
  set" — the same field, now purely client-side, and the seam for resolving keys
  from something better than an environment variable later.
- **`outfit daemon` becomes a worker: it reads no Outfit and no preset**, and
  takes no Outfit path. It serves what a start request carries or what was
  pushed and stored, and a start with neither fails saying so. **BREAKING**:
  `outfit daemon ./Outfit` and `OUTFIT_ALIAS`-driven daemons stop working.
- It reads no `fleet.yaml` either. That file is the *client's* map of the fleet;
  a node has no use for its peers' addresses, and handing every node the map
  widens what one compromised node exposes.
- **The control-API bearer token gains two more sources**: `--api-token-file`
  (recommended) and `--api-token`, beside the existing `OUTFIT_API_TOKEN`. The
  daemon no longer loads an Outfit's `.env`, so the environment alone would
  leave a hand-started LAN daemon with nowhere convenient to put it.

## Capabilities

### New Capabilities
- `engine-credentials`: where an engine's API key comes from, how it reaches
  the engine without being exposed, and what the daemon will and will not say
  about it.

### Modified Capabilities
- `daemon-api`: the start request carries an engine key; the bearer token may
  come from a file or a flag as well as the environment.
- `serve-daemon`: the daemon takes no Outfit path and resolves no Outfit or
  preset; what it serves comes from the request or the stored config alone.
- `fleet-routing`: the requirement that resolved a key the node was already
  gated with is replaced by one where the wake supplies the key it will hand to
  the agent — the direction reverses, so it is a replacement rather than an
  edit.
- `fleet-config`: `engineTokenEnv` is the client's lookup for the key it sets.

## Impact

- `internal/daemon`: start accepts a key; the key is written 0600 and passed as
  `--api-key-file`; token resolution gains the file and flag forms.
- `cmd/outfit`: `daemon` loses its Outfit path and its Outfit/preset
  resolution, and gains `--api-token`/`--api-token-file`; the routing wake sends
  the key it resolved.
- `internal/fleet`: the wake pushes a key; `engineKeyFor` becomes "resolve the
  key to set" rather than "resolve the key the node needs".
- `docs/openapi.yaml`, `docs/commands/{fleet,serve}.md`, `docs/env-vars.md`, and
  both fleet examples — `examples/fleet-local` currently documents starting the
  daemon beside an Outfit, and `examples/fleet-docker` ships a `node/Outfit` and
  a `CMD` that passes it.
- **Depends on `fleet-harness-routing`** landing first: this modifies
  `fleet-routing` and `fleet-config`, which that change introduces.

### A decision this reverses

`daemon-api` currently states the token is supplied via the environment "never
as a command-line flag", because a command line is readable by any local user
through `ps` — the same reason this change insists on `--api-key-file` for the
engine's key. Supporting `--api-token` is therefore a deliberate reversal, taken
for the convenience of a hand-started daemon. `--api-token-file` carries no such
exposure and is the documented recommendation; the flag's help text and the
docs should say plainly what it costs.
