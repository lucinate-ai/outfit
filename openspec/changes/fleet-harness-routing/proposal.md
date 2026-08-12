## Why

`outfit fleet` can already see every engine you run and drive them one at a
time, but nothing connects that to the agent you actually work in. To use a
fleet node today you look at `outfit fleet status`, work out the engine's
address yourself, and export `OPENAI_BASE_URL` by hand — and if the node you
picked is idle, you start it first and wait. The fleet knows which machines
exist, which are running, and what each is serving; the harness launch should
use that rather than making the user do the lookup.

`outfit harness` already does exactly this for a cloud endpoint: an Outfit with
a `REMOTE` instruction fetches the endpoint's base URL and key and injects them
into the launched agent. A fleet is the same problem with more than one
candidate — which makes it a routing problem, and the reason nothing has been
built yet.

## What Changes

- A new `FLEET <path>` Outfit instruction, mirroring `REMOTE`: it names the
  fleet a launch routes through. `outfit harness --fleet=<path>` overrides it,
  and `--node <name>` pins a node instead of choosing one.
- `outfit harness` against a fleet-routed Outfit SHALL pick a node, resolve its
  engine's OpenAI-compatible base URL, and inject `OPENAI_BASE_URL` (plus the
  node's engine key when it needs one) into the launched agent — the same
  injection path `REMOTE` uses today, with a selection step in front.
- Selection prefers a node already serving the wanted model, and which of
  several such nodes wins is a setting: `prefer: idle` (the default) takes the
  node inactive longest, so a second agent does not pile onto the engine that is
  already working; `prefer: active` consolidates onto the busy node instead,
  leaving the rest free to wake for another model or sleep. It is set per fleet
  in `fleet.yaml` and overridden per launch with `--prefer`.
- When no node is serving what is wanted, the selected node is **woken**: outfit
  pushes the Outfit's model as that node's deploy config, starts it through the
  daemon's existing start endpoint, and waits for it to answer before launching
  the agent. `--no-wake` turns that off.
- The daemon's status reply gains the one fact a router needs and no caller can
  guess: where its engine serves — the port, whether it is bound to loopback,
  and whether it demands a key.
- `fleet.yaml` nodes gain an optional `engine:` block overriding the engine's
  host/port/path for that node, and an `engineTokenEnv` naming the variable
  holding its engine key. As with the daemon token, no secret is written in the
  file.
- `outfit fleet route` reports the node a launch would choose and why, so the
  routing decision is inspectable without launching an agent.
- An Outfit naming both `FLEET` and `REMOTE` is an error naming the conflict:
  each is a different answer to "where does the address come from", and two
  answers is a mistake rather than a precedence puzzle. An explicit `BASEURL`
  still wins over `FLEET`, as it already does over `REMOTE`, and outfit says it
  is not routing.

Server-side routing — the **outfit gateway**, a single lightweight
OpenAI-compatible endpoint that fans out over the fleet — is *designed* here but
not built. `design.md` records the seam it slots into: a `FLEET` value that is a
URL rather than a file skips selection entirely, so a gateway is a second target
for the same instruction rather than a second mechanism.

## Capabilities

### New Capabilities
- `fleet-routing`: choosing a fleet node for a harness launch — the selection
  rules, waking an idle node, resolving the chosen node's engine URL and key,
  and what the launched agent's environment ends up carrying.

### Modified Capabilities
- `outfit-files`: the `FLEET` instruction, and its exclusivity with `REMOTE`
  and `BASEURL`.
- `daemon-api`: status reports the supervised engine's serving endpoint — port,
  path, whether the bind is loopback-only, and whether a key is required.
- `fleet-config`: a fleet-wide `prefer` setting, plus per-node `engine:`
  overrides and `engineTokenEnv`, resolved the same way the daemon token
  reference already is.
- `fleet-client`: `outfit fleet route` alongside the existing subcommands,
  reporting the selection a launch would make.

## Impact

- `internal/outfit`: a new keyword and its conflict rules.
- `internal/fleet`: node selection, wake-and-wait, engine URL resolution; the
  `NodeConfig` gains its engine block.
- `internal/daemon`: `StatusResponse` gains an engine endpoint; the CLI supplies
  it from the engine table it already owns (`serveEngine.defaultBaseURL` and the
  Outfit's `BASEURL`, the same source `scrapeTargetFor` uses).
- `cmd/outfit`: `harness` gains `--fleet`/`--node`/`--no-wake` and a selection
  step before the apply; `fleet` gains `route`.
- `docs/openapi.yaml`, `docs/commands/{harness,fleet}.md`, `docs/outfit-file.md`,
  and `examples/fleet` are updated with the new field, flags, and instruction.
- No breaking changes: an Outfit with no `FLEET` and a `fleet.yaml` with no
  `engine:` block behave exactly as they do now, and the new status field is
  additive.
