## Context

See `proposal.md` — Why. What shapes the approach:

- The cloud already runs the daemon this way. Its instances start `outfit daemon
  --api-addr 127.0.0.1:4242` with no Outfit path, the control plane writes the
  deploy config, and the key is delivered as a file the engine reads with
  `--api-key-file`. Nothing here is a new pattern; it is the existing one applied
  to a fleet node, with the client in the control plane's seat.
- `scrapeTargetFor` already lifts `--api-key`/`--api-key-file` out of the
  engine's argv so a gated `/metrics` still answers the collector. Whichever way
  the key arrives, the daemon reads it back from the command it built — so the
  metrics path needs no new wiring.
- `daemon.Push` already persists the deploy config at 0600 with the comment
  "serve args can carry sensitive flags". Persisting a key alongside it is the
  same trust decision already taken, made explicit.
- `outfit serve --api` keeps its Outfit. It is the foreground
  run-what-this-file-says command; only `outfit daemon` becomes API-driven.

## Goals / Non-Goals

**Goals:**

- One party holds the engine's key: the client that starts the engine.
- The key never reaches a process command line, on any path.
- A node holds no workload configuration — no Outfit, no preset, no fleet file.
- A hand-started LAN daemon has somewhere convenient to put its bearer token
  now that an Outfit's `.env` is gone.

**Non-Goals:**

- Fetching keys from a keychain or secret manager. `engineTokenEnv` becomes the
  seam for that; this change does not walk through it.
- TLS on the control API. The key crosses the wire in the start body, as the
  bearer token already does in its header; both rely on the network being
  trusted (a LAN, a tailnet). Changing that is its own change.
- Rotating a running engine's key. A key takes effect at start, as it does today.
- Changing `outfit serve --api`.

## Decisions

### The key travels with the start, not with the node

The alternative was to keep the key on the node — its preset gates its engine,
and the client learns the value out of band through `engineTokenEnv`. That
preserves the daemon's "never hand out credentials" property, but it leaves the
same secret in two places with nothing reconciling them: a mismatch surfaces as
a 401 on the agent's first request, and a woken node is ungated whatever the
preset says because the derivation drops `api-key`.

Supplying it with the start collapses that. The client is already trusted with
the node's bearer token, which lets it run arbitrary engine commands there and
read logs containing prompts and completions; a key is strictly less than what
it already holds. And because the client sets it, it knows it — so the value it
gives the agent is the value the engine checks, by construction.

The daemon's no-credentials property survives intact: it still never *returns* a
key. It only ever receives one.

### `--api-key-file`, never `--api-key`

A command line is world-readable. `--api-key <value>` discloses the key to every
local user on a shared node, which is the population the key exists to exclude.
The daemon writes the key 0600 into its state directory and passes the path.

This is also why the *bearer* token's literal flag is a reversal worth naming
rather than a symmetric addition — see below.

### The key persists with the config

`outfit fleet start <node>` sends no body. If the key did not persist, that
restart would silently produce an ungated engine — reintroducing, in a smaller
place, the inconsistency this change removes. So a key is stored with the deploy
config it arrived with, at the same 0600 protection, and replaced when a new one
arrives.

The cost is a secret at rest on the node. It is bounded: one key, replaced not
accumulated, in a directory that already holds serve arguments that may carry
credentials.

*Alternative considered*: require a key on every start and never persist. It
makes `fleet start` unusable without a client that knows the key, which is most
of the value of `fleet start`.

### The daemon reads nothing

`resolveDaemonOutfit`, the `OUTFIT_ALIAS` gate (`defaultOutfitNamed`), and the
Outfit branch of `BuildArgv` all go. The daemon keeps exactly two inputs: its
flags and its API.

The temptation is to leave the Outfit path working "for convenience". It is not
convenient — it is the reason the fleet examples needed a paragraph explaining
that a node's Outfit and a client's Outfit are different documents, and the
reason a node's `BASEURL` silently disabled routing for anyone who reused the
file. Removing it makes a node the same thing on every machine.

No fleet file either. A `fleet.yaml` names *other* machines and holds references
to their tokens; handing it to every node widens what one compromised node
exposes, and gives it knowledge it has no use for. Nodes discovering each other
is a registry, which is a different product.

### The bearer token gains a file and a flag

Removing the Outfit removes the `.env` beside it, which is where a hand-started
LAN daemon's `OUTFIT_API_TOKEN` conventionally lived. Three sources replace it:
`--api-token-file`, `OUTFIT_API_TOKEN`, and `--api-token`.

The current spec says the token is supplied via the environment "never as a
command-line flag", for exactly the `ps` reason that governs the engine key
above. `--api-token` reverses that deliberately, for the case of typing a daemon
into a terminal. It is documented with its cost stated, and `--api-token-file`
is the recommended form — the one a service manager should use.

Giving two sources at once fails rather than resolving a precedence, because a
silent winner between two credentials is how you end up debugging a 401 against
the wrong value.

### Where the client's key comes from

Unchanged in mechanism, changed in meaning: `engineTokenEnv` names a variable,
resolved from the environment then the `.env` beside the fleet file. It was "the
key the node was gated with, told to you"; it becomes "the key you will gate it
with". Nothing about the file format changes, which is what makes the later move
to a keychain or secret manager a change to one resolver.

## Risks / Trade-offs

- **A secret now sits on each node's disk.** → 0600 in the daemon's state
  directory, one key, replaced not accumulated, on a machine whose bearer token
  already grants engine control and log access.
- **The key crosses the network in a request body, in plaintext.** → The bearer
  token already does, in a header, on the same connection; this adds no new
  exposure class but does extend it to a longer-lived secret. Documented as
  requiring a trusted network, which the daemon's non-loopback token guard
  already implies.
- **`outfit daemon ./Outfit` breaks.** → It fails with a message naming the
  start request rather than silently ignoring the argument. Both fleet examples
  and the daemon docs change with it. There is no deprecation window; if one is
  wanted, accepting the path with a warning for a release is a small addition.
- **`outfit fleet start <node>` needs a prior push.** → After one routed launch
  or one push it works as before. A daemon that has never been told anything
  fails saying so, which is more honest than serving whatever file happened to
  be in the directory it started in.
- **`--api-token` is disclosed by `ps`.** → Accepted deliberately and documented;
  `--api-token-file` exists precisely so nobody has to accept it.

## Migration Plan

1. Land `fleet-harness-routing` first — this modifies `fleet-routing` and
   `fleet-config`, which that change introduces. `concord check` reports
   `target-missing` for both until it archives; that is expected, not a defect
   in this change.
2. Ship the daemon changes and the client changes together. A new client against
   an old daemon sends a key the daemon ignores, producing an ungated engine the
   client thinks is gated — so the daemon must be upgraded first, or both at
   once. The fleet is small enough that "upgrade the nodes, then the clients" is
   a realistic instruction, and it is the right order.
3. Rollback is the previous binary on the nodes; the stored deploy config
   remains readable, and a stored key is ignored by a daemon that does not know
   about it.
