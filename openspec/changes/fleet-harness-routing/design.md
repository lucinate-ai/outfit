## Context

See `proposal.md` — Why. What matters for the approach is what already exists:

- `internal/fleet` parses `fleet.yaml`, resolves each node's bearer token, and
  fans out over the nodes' control APIs concurrently (`FanOut`, `NodeResult`,
  the typed `Outcome` values). Selection is a consumer of that, not a new
  transport.
- The daemon control API already takes a deploy config in the body of `POST
  /v1/start`, validating it against the engines that host can serve and
  persisting it before starting. Waking a node is that call — no new endpoint.
- `outfit harness` already resolves a dynamic endpoint before launching, for
  `REMOTE`: `fetchRemoteEnv` runs *before* `applySelection`, fills
  `sel.BaseURL`, and injects `OPENAI_BASE_URL`/`OPENAI_API_KEY` into the child
  environment. Fleet routing plugs into exactly that seam with a selection step
  instead of a Lambda call.
- What is missing is one fact: `daemon.StatusResponse` says the engine is
  running but not where it serves. `scrapeTargetFor` in `cmd/outfit` already
  derives that address for the metrics collector — the Outfit's `BASEURL` or the
  engine's default bind — and already lifts `--api-key`/`--api-key-file` from
  the argv.

## Goals / Non-Goals

**Goals:**

- One selection abstraction that both this change and a future gateway use, so
  the routing rules are written once.
- No new endpoint on the daemon, and no new long-running process anywhere.
- Every failure resolved before the harness config is written.
- The daemon never hands out its engine's key.

**Non-Goals:**

- Building the gateway. Its shape is settled below so that building it is
  additive, but no server is written here.
- Load balancing per request. Selection happens once, at launch; an agent stays
  on the node it was given for the life of that session.
- Queueing, budgets, cost tracking, per-user keys, model aliasing across
  providers — the litellm features deliberately left out.
- Multi-engine nodes. One daemon supervises one engine; that stays true.

## Decisions

### Client-side selection now, the gateway as a second target for the same instruction

Client-side selection needs no new infrastructure and works the moment a node
answers. It also has to exist anyway: something must know the selection rules,
and a gateway would be a second implementation of them if the client had none.

The seam that keeps the gateway additive is the `FLEET` value itself. A path
means "read this fleet file and choose"; a URL means "this already is the
endpoint — use it". A gateway then needs no new keyword, no new flag, and no
change to the launch path:

```
FLEET ./fleet.yaml            # client-side: outfit chooses a node
FLEET http://gw.internal:4000 # gateway: outfit points straight at it
```

Parsing accepts both from the start (a value with a scheme is a URL, anything
else is a path); this change implements only the path branch, and the URL branch
is rejected with "gateway routing is not implemented yet" rather than being left
as an unhandled shape. That keeps the eventual gateway change from having to
revisit the Outfit format.

*Alternative considered*: a separate `GATEWAY` keyword. Rejected — it would make
"which of the two is in force" a precedence question in every Outfit, for two
things that answer the same question.

### The outfit gateway, sketched

Recorded here so the seam above is a real plan rather than a hope. Not built in
this change.

- `outfit gateway --fleet ./fleet.yaml --listen :4000` — a foreground process
  like `outfit serve`, holding the same `fleet.yaml`.
- It serves an OpenAI-compatible surface: `/v1/models` (the union of what the
  fleet's nodes serve, refreshed by the same status fan-out), and a reverse
  proxy for `/v1/chat/completions` and `/v1/completions` that picks a node with
  the same selector this change builds, then streams through untouched.
- It authenticates callers with one bearer token, the way the daemon does, and
  holds each node's engine key itself — so the machine running an agent needs no
  fleet file and no per-node secrets, which is the gateway's real payoff.
- It wakes an idle node on a request for a model nothing is serving, holding the
  request while the engine loads.
- Explicitly not: budgets, spend tracking, per-user keys, retries across
  providers, request logging beyond a line per route. Those are what makes
  litellm a service to operate rather than a binary to run.

The reason not to build it now is that it turns "reach a machine on your
network" into "run and monitor a service", which needs the selector, the engine
endpoint reporting, and the wake path — all of which this change delivers — to
be worth anything at all.

### The daemon reports a port, not a URL

A daemon knows its engine binds `127.0.0.1:8080`. That string is useless to
anyone else, and the daemon cannot know the name a client reaches it by — a LAN
name, a tailscale name, a published container port. So status reports the parts
a caller can compose against a host it already has: port, path prefix, and a
loopback-only flag; the client supplies the host from `fleet.yaml`.

The loopback flag is what turns an unfixable symptom into an instruction. An
engine bound to loopback on a remote node yields a connection refused with no
hint; reported as a flag, routing can say "this engine answers only on that
machine — bind it to a reachable address, or set the node's engine override".

The values come from the same derivation `scrapeTargetFor` already performs (the
Outfit's `BASEURL` or the engine's `defaultBaseURL`, and the argv's
`--api-key`/`--api-key-file`), so the endpoint status reports and the endpoint
metrics scrapes cannot drift apart. That derivation moves into a shared helper
the daemon is handed alongside `BuildArgv`, keeping the engine table in
`cmd/outfit` where it lives now.

*Alternative considered*: the daemon reports a full base URL and the client
rewrites the host. Rejected — same information, but it invites a client that
forgets to rewrite and works only on loopback, which is exactly the fleet's
least interesting case.

### The fleet file holds the engine key, the daemon never returns it

The daemon says `requiresKey: true` and nothing more. A node's engine key is
referenced by `engineTokenEnv` in `fleet.yaml`, resolved the same way the daemon
token already is (environment, then the adjacent `.env`).

This keeps the existing property that a control-API token authorises driving a
node, not extracting credentials from it, and it keeps `fleet.yaml` a file of
references. It costs the operator one more variable per gated node — which is
the trade the gateway later removes by holding those keys centrally.

### Waking is the existing start-with-config call

Selection derives a `remote.DeployConfig` from the Outfit exactly as `outfit
remote deploy` does (runner from `PROVIDER`, model from `MODEL`, context, served
name from `ALIAS`, serve args from the `PRESET`), and `POST /v1/start` with it
in the body.

Capability negotiation falls out for free: a node that cannot serve that runner
or model rejects the config with the daemon's existing `ValidateConfig`, and the
selector tries the next candidate. No capabilities endpoint, no second
description of what a host can serve that could disagree with the first.

*Alternative considered*: `GET /v1/capabilities`. Rejected — it duplicates
validation logic that already exists in a place that can go stale relative to
it.

### Selecting on idle time from status, and never displacing a running engine

The status fan-out is one round trip the router makes anyway, and `idleSeconds`
is a figure the daemon already derives — so ranking on it costs nothing, needs
no second metrics fan-out, and involves no interpretation of token counters. It
is a crude load signal and that is accepted: the alternative is a scheduler, and
a fleet is a handful of machines.

Which direction to rank in is a setting rather than a decision, because both
answers are right for different fleets:

- **`idle`** — spread. Several people share the fleet, or one person runs
  several agents; the thing to avoid is a second session landing on the engine
  that is mid-request, which is exactly the node `idleSeconds` ranks last.
- **`active`** — consolidate. One person, several machines, and the reason to
  route at all is capacity rather than sharing. Keeping sessions on one engine
  leaves the others genuinely free — available to be woken for a different
  model, or left asleep rather than idling a GPU.

`idle` is the default because piling onto a busy engine degrades a session
someone is already in, while over-spreading only costs the odd extra wake.

The setting resolves flag-then-file-then-default: `--prefer` on the launch, then
`prefer:` in `fleet.yaml`, then `idle`. It sits in the fleet file rather than the
Outfit because it describes how a *cluster* should be used — the same Outfit
routed at a shared fleet and a personal one wants different answers, and the
fleet file is the thing that differs.

*Alternative considered*: an `OUTFIT_FLEET_PREFER` environment variable, for
symmetry with `OUTFIT_HARNESS` and `OUTFIT_ALIAS`. Left out — those name *what
to run*, which changes shell to shell; this names *how a cluster is shared*,
which does not. It can be added later without disturbing the precedence.

*Alternative considered*: inferring the direction — spread when several nodes
are busy, consolidate when one is. Rejected as unpredictable: a routing decision
that changes with the weather is worse than one that is occasionally
suboptimal, and it is not something a user could reason about from the output.

A running engine is never stopped to make room, including a pinned one. On a
shared machine the cost of being wrong is someone else's session dying mid
request; the cost of the conservative rule is an error message telling the user
what the node is serving.

### Readiness is the engine answering, not the daemon saying `running`

The supervisor reports `running` when the process exists. llama.cpp then loads
weights, which on a cold node is minutes. So the wake path polls the node's
status until `running`, then probes the resolved engine endpoint until it
answers, bounded by `--wake-timeout` (default 5 minutes, a package-level
variable so tests do not wait). Progress is reported while waiting — a silent
five-minute pause reads as a hang.

A timeout leaves the started engine running. It is probably still loading, and
stopping it throws away the only expensive part of the work.

### Ordering: route, then wake, then apply, then launch

The whole route resolves before `applySelection` writes anything, mirroring how
`fetchRemoteEnv` runs ahead of the apply today. A failed route therefore leaves
the harness config exactly as it was, which is the property that makes routing
safe to attempt automatically.

### Where the code goes

- `internal/fleet`: a `Selector` over `[]NodeResult` (pure ranking, trivially
  testable), the engine-endpoint resolution, and the wake-and-wait. It gains no
  knowledge of Outfits — the caller hands it a wanted model and a deploy config.
- `internal/outfit`: the `FLEET` keyword, the URL-vs-path distinction, and the
  `FLEET`/`REMOTE` exclusivity.
- `internal/daemon`: the endpoint fields on `StatusResponse`, populated from a
  helper the CLI supplies.
- `cmd/outfit`: `--fleet`, `--node`, `--no-wake`, `--wake-timeout` on `harness`;
  `fleet route`; the deploy-config derivation shared with `remote deploy`.

## Risks / Trade-offs

- **A stale base URL is written into the harness config.** Routing fills the
  same `BASEURL` slot a `REMOTE` fills, so the config records where the last
  launch pointed. Running the harness directly, outside `outfit`, then hits a
  node that may have moved on. → The same is already true of `REMOTE`; each
  `outfit harness` run rewrites it, and `outfit show` reports the address that
  is written.
- **Auto-waking starts processes on other people's machines.** A launch can
  spin up an engine on a shared box without anyone asking. → It only ever wakes
  a node that is *not* running, never displaces one, announces what it is doing,
  and `--no-wake` turns it off. The blast radius is a GPU going busy, not a
  session dying.
- **Selection is racy.** Two people launching at once can pick the same idle
  node, and both wake it; the second start loses to the daemon's
  already-running rejection. → The loser re-reads status and, finding the node
  now serving the model it wanted, uses it. A start rejected as already-running
  is treated as success-by-another-route rather than an error.
- **`idleSeconds` is a weak load signal.** A node that has been busy for an hour
  and one that finished a second ago both look "recently active". → Accepted;
  see the decision above. It is good enough for what `prefer: idle` is for —
  a node mid-request always ranks last — and if it proves too coarse, the
  metrics fan-out is a drop-in better signal behind the same selector
  interface, with no change to the setting or its values.
- **`prefer: active` can pile sessions onto one engine.** That is what it is
  for, but a fleet set to it will happily put a third and fourth agent on a
  node already at capacity, because nothing here measures capacity. → It is
  opt-in, it is a per-fleet choice made once by whoever runs the fleet, and
  `--prefer idle` overrides it for a launch that should go elsewhere.
- **More network in the launch path.** Every fleet-routed `outfit harness` now
  fans out over the fleet before it launches anything. → Bounded by the same
  `RequestTimeout` the fleet client already uses, and skipped entirely for an
  Outfit with no `FLEET`.
- **A container-published engine port will not match what the daemon reports.**
  Inside the container the engine binds 8080; outside it is published as
  something else. → The per-node engine override exists for exactly this, and
  the `fleet-docker` example is the place to prove it.

## Migration Plan

Additive throughout. An Outfit with no `FLEET`, a `fleet.yaml` with no engine
block, and an older daemon that reports no engine endpoint all behave as they do
today. A client that routes against a daemon too old to report an endpoint fails
with a message naming the node and saying to upgrade it or set the node's engine
override — the override is the escape hatch that makes a mixed-version fleet
workable.

Rollback is removing the `FLEET` instruction: nothing else changes behaviour
unless something asks it to.
