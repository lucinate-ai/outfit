# outfit fleet

Observe and drive every engine you run, from one place. Each machine runs
[`outfit daemon`](serve.md#the-control-api---api-and-outfit-daemon); a
`fleet.yaml` names them, and `outfit fleet` fans out over their control APIs.

```sh
outfit fleet status          # one row per node: state and what it serves
outfit fleet metrics         # each node's engine + system metrics
outfit fleet metrics -w      # a live dashboard, redrawn in place
outfit fleet route my-outfit # which node a harness launch would pick
outfit fleet start gpu-box   # start one node's engine
outfit fleet stop gpu-box    # stop it
```

A fleet is also where [`outfit harness`](harness.md#launching-against-your-fleet)
sends an agent: an Outfit naming a `FLEET` picks a node and launches against it,
so the machine you are sitting at needs no engine of its own.

## Try it without any hardware

[`examples/fleet-docker/`](../../examples/fleet-docker/) brings up a real
three-node fleet in containers — real daemons, real auth, a fake engine — so
you can see all of this working before setting up a single machine:

```sh
cd examples/fleet-docker && cp .env.example .env
docker compose up -d --build
set -a && . ./.env && set +a
outfit fleet status --fleet ./fleet.yaml
```

## `fleet.yaml`

A list of nodes and how to reach each one. It holds **no secrets** — a node
that needs a bearer token names the environment variable holding it:

```yaml
nodes:
  - name: studio          # what you type at `fleet start <node>`
    host: studio.local    # LAN name, tailscale name, or an address

  - name: gpu-box
    host: 198.51.100.7    # a tailscale address, say
    port: 4242            # optional; the daemon's default when omitted
    tokenEnv: GPU_BOX_TOKEN   # the *name* of the variable, never the token
```

The file is found the way an `Outfit` is: `./fleet.yaml` in the working
directory, or `--fleet <path>`.

### Where a node's engine answers

A node's `host` and `port` name its **daemon**, which is a different port from
the **engine** it supervises. For [routing](#which-node-would-i-get) outfit
needs the engine's, and the daemon reports it — so most nodes need nothing
more. Declare an `engine` block for the cases a daemon cannot describe:

```yaml
nodes:
  - name: containerised
    host: docker-host
    engine:
      port: 18080          # published port, not the one it binds inside

  - name: proxied
    host: node.local
    engine:
      host: https://engine.example   # a reverse proxy in front of the engine
      path: /openai                  # when it is not the usual /v1
```

Each field falls back independently to what outfit would otherwise derive: the
node's own `host`, and the port and path the daemon reports.

An engine bound to loopback answers only on its own machine. Routing to it from
elsewhere fails with that explanation rather than a bare connection refused —
bind the engine to a reachable address (llama.cpp's `--host 0.0.0.0`), or
declare an `engine` block, which is you taking responsibility for reachability.

### Spreading or consolidating

`prefer` decides which node wins when several could all serve you:

```yaml
prefer: idle      # or: active
nodes: …
```

- **`idle`** (the default) — the machine quiet longest wins. A node that is
  mid-request is the *least* idle of all, so it is the last one chosen. Use it
  when several people share the fleet, or you run several agents at once.
- **`active`** — the most recently active wins, consolidating sessions onto one
  engine and leaving the others free to be woken for another model, or left
  asleep.

`outfit harness --prefer <value>` and `outfit fleet route --prefer <value>`
override the file for one command, which is the cheap way to see what the other
setting would do before committing to it.

### Tokens

`tokenEnv` names an environment variable; the value is resolved from the
process environment first, then a `.env` beside the `fleet.yaml` — the same
precedence outfit uses everywhere, so an exported value wins and the `.env`
only fills a gap. Put the secrets there:

```sh
# .env beside fleet.yaml (gitignored)
GPU_BOX_TOKEN=…
```

A node with no `tokenEnv` is contacted without authentication, which is
correct for a daemon bound to loopback. Any node reachable over the network
needs a token — the daemon refuses to listen on a non-loopback address without
one.

A `tokenEnv` naming a variable that is set nowhere is reported against that
node as `config-error`, so a typo shows up on its row rather than as a
mysterious `unauthorized`.

A node whose **engine** needs a key names that separately, with
`engineTokenEnv`. The two are different credentials — one authorises driving the
node, the other authorises using its engine — and a node may need either, both,
or neither. It is resolved exactly as `tokenEnv` is, and the daemon never hands
its engine's key out: it says only that one is required.

```yaml
  - name: gated
    host: gated.local
    tokenEnv: GATED_TOKEN             # to drive the daemon
    engineTokenEnv: GATED_ENGINE_KEY  # to talk to its engine
```

## A node that is down never blanks the view

Fan-out is for observing, so a node that cannot be reached is a **row**, not a
failure — the rest of the fleet still renders and the command still exits 0:

```
NODE     STATE         SERVING
studio   running       llamacpp  org/qwen  (up 1h 2m 5s)  (last active 12s ago)
gpu-box  idle          llamacpp  org/qwen
offline  unreachable   dial tcp 10.0.0.9:4242: connect: connection refused
```

"last active" comes from the activity each daemon tracks, so a glance answers
"which of my nodes is doing nothing?". It is absent until a node's engine has
actually done some work — a daemon that has served nothing reports no activity
rather than claiming it has been quiet since it started. The wording avoids
"idle" deliberately: that word is already an engine *state*, meaning nothing
has been started at all.

| Outcome | Meaning |
| --- | --- |
| *(a state)* | The node answered: `idle`, `running`, `stopped`, `crashed` |
| `unreachable` | No answer at all — refused, timed out, no such host |
| `unauthorized` | The box is up; the token was rejected |
| `config-error` | The node could not be called — usually a `tokenEnv` that resolves to nothing |
| `failed` | The daemon answered with an error — the node is fine, the request was refused |

## Metrics

`outfit fleet metrics` renders each node's engine and system metrics in the
same `bar` (default), `table`, and `json` formats as
[`outfit remote metrics`](remote.md) — they share the renderers, so a node in
your fleet and a cloud endpoint look the same.

Each node's block carries the same `last active` figure the status table
shows, for the reasons given above, and on the same terms: absent until the
node's engine has done some work. A node whose engine has *stopped* still
shows it — the daemon keeps the record across a stop, and "how long since this
did anything?" is worth more about a stopped engine than about a busy one.

`--watch`/`-w` redraws the whole fleet on an interval, clearing the screen in
place with no scrollback. Each refresh is rendered into a buffer first, so a
slow node delays the refresh but never tears the display. Ctrl+C exits
cleanly.

The `json` format is labelled by node and **includes the nodes that failed**,
with their outcome and reason — so a consumer sees the whole fleet rather than
silently missing whatever was down:

```json
[
  { "node": "studio", "outcome": "ok", "metrics": { "state": "running", "…": "…" } },
  { "node": "offline", "outcome": "unreachable", "error": "dial tcp …: connection refused" }
]
```

## Logs

`outfit fleet logs` prints what your engines actually said — the answer to the
question `fleet status` raises when it reports a node as `crashed`.

```sh
outfit fleet logs              # the tail of every node's engine log
outfit fleet logs gpu-box      # just that node
outfit fleet logs -f           # follow, until you interrupt it
outfit fleet logs --limit 500  # more backlog per node
```

Each node's daemon captures its engine's stdout and stderr to a file, and
serves a slice of it over [`GET /v1/logs`](../http-api.md). Reading is safe, so
unlike `start` and `stop` this fans out across the whole fleet by default;
naming a node narrows it to one.

With more than one node talking, every line is prefixed with the node it came
from. Reading a single node leaves the prefix off, so it reads like that node's
own log. Lines are **not** interleaved between nodes: engine output carries no
timestamp we can trust, so merging several machines' lines would invent a
chronology that isn't there. Each node's output stays in its own order.

Following resumes each node from a byte offset that node reported, so a line is
never printed twice and none is missed — no overlap window, no guessing. Nodes
are polled independently, because each log is its own file with its own
position.

Nodes with nothing to give say so rather than vanishing: one that has never run
an engine, one that is unreachable, and one whose daemon is older than the
endpoint (which names itself as needing an upgrade — a fleet mid-rollout will
legitimately hold a mix).

| Flag | Meaning |
| ---- | ------- |
| `--limit` | Lines of backlog per node (default 200) |
| `-f`, `--follow` | Keep printing new output until interrupted |
| `--format` | `text` (default) or `json` |

Two things worth knowing. Engine output can carry prompts and model output, and
it crosses the network to whoever holds the node's token — the same trust
boundary as `start` and `stop`, but the content is more revealing. And the
daemon does **not** rotate its engine log: it grows for the daemon's lifetime,
so a long-lived node accumulates. Reads are always bounded, so this costs disk
on the node rather than anything at the client.

## Which node would I get?

`outfit fleet route` reports the node a
[harness launch](harness.md#launching-against-your-fleet) would pick for an
Outfit, and **changes nothing** — no config pushed, no engine started, no
harness config written:

```sh
outfit fleet route my-outfit
outfit fleet route --prefer active my-outfit
```

```
Outfit: ./my-outfit/Outfit
Fleet:  ./fleet.yaml
Prefer: idle

Would use gpu-box at http://gpu-box:8080/v1
  serving qwen3-27b, last active 312s ago (prefer idle)
```

When nothing is serving that model it shows the whole fleet's state and names
the node a real launch would wake, without waking it:

```
no node in ./fleet.yaml is serving qwen3-27b:
  studio           idle
  gpu-box          running  some-other-model
  laptop           unreachable (connection refused)

A launch would wake studio and wait for its engine. Nothing has been started.
```

Use it to check a route before an agent depends on it, to see what the other
`prefer` setting would choose, or to work out why a launch landed where it did.

## Starting and stopping

`fleet start` and `fleet stop` take **one node**:

```sh
outfit fleet start gpu-box
```

They deliberately refuse to act on the whole fleet — mutating every engine at
once is a footgun — so with no node they list the fleet and do nothing. An
unknown name fails, naming the nodes you could have meant. The daemon's own
rules still hold: starting a node whose engine is already running reports its
conflict, and stopping one that is not running succeeds quietly.

## Flags

| Flag | Meaning |
| ---- | ------- |
| `--fleet <path>` | The fleet file (default `./fleet.yaml`) |
| `--node <name>` | `route` only: report this node rather than choosing one |
| `--prefer` | `route` only: rank by `idle` or `active`, overriding the file |
| `--format` | `metrics`: `bar` (default), `table`, or `json`; `logs`: `text` (default) or `json` |
| `-w`, `--watch` | `metrics` only: redraw on an interval until interrupted |
| `-f`, `--follow` | `logs` only: keep printing new output until interrupted |
| `--limit` | `logs` only: lines of backlog per node (default 200) |

## See also

- [`examples/fleet-local/`](../../examples/fleet-local/) — a fleet of one, on your own machine
- [`examples/fleet-docker/`](../../examples/fleet-docker/) — a runnable fleet
- [`outfit daemon`](serve.md) — what runs on each node
- [HTTP Control API](../http-api.md) — the API the fleet client speaks
- [Environment variables](../env-vars.md)
