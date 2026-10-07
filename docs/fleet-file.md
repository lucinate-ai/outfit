# The `fleet.yaml` file

A **fleet file** is the `fleet.yaml` that names the machines you run — and how
to reach each one's [`spinloop daemon`](commands/serve.md#the-control-api-api-and-spinloop-daemon) —
so [`spinloop fleet`](commands/fleet.md) can observe and drive them all from one
place, and [`spinloop harness open`](commands/harness.md#launching-against-your-fleet)
can pick one of them for you. Like a [Spinloop](spinloop-file.md), it is a small
declarative file meant to be committed: it names the fleet, and it holds **no
secrets** — a token is named by the environment variable that holds it, never
written in the file.

```yaml
# fleet.yaml — the machines, and how to reach each
prefer: idle               # optional; idle (default) or active
wake: on                   # optional; on (default) or off
apiKeyEnv: REMOTE_KEY      # optional; the default engine key for kind: cloud nodes

nodes:
  - name: studio           # required, unique; what you type at `fleet start <node>`
    host: studio.local     # required for a daemon node; a LAN name, tailscale name, or address
    port: 4242             # optional; the daemon's control API port (4242 when omitted)
    tokenEnv: STUDIO_TOKEN                    # optional; the *name* of the variable holding the daemon token
    engineTokenEnv: STUDIO_ENGINE_KEY         # optional; ditto, for the key its engine is gated with
    file: ./studio/Spinloop                   # optional; what `fleet start`/`deploy` reads for this node
    engine:                               # optional; where its *engine* answers, when the daemon cannot say
      host: https://engine.example
      port: 18080
      path: /v1

  - name: qwen             # for a kind: cloud node, the registered environment's name
    kind: cloud
    instance-type: g6e.2xlarge          # optional; the EC2 type its environment launches as

gateway:                       # optional; where a spinloop gateway serves this fleet
  url: http://gateway.internal:4000    # required in the section, with a scheme
  tokenEnv: GATEWAY_TOKEN              # optional; OPENAI_API_KEY when absent
  name: remote-llms                  # optional; labels the gateway in a model picker
```

The file is found the way a `Spinloop` is: `./fleet.yaml` in the working
directory, or `--fleet <path>` — short `-f` on every `spinloop fleet`
subcommand except `fleet logs`, where `-f` is the follow flag and the fleet
file takes the long form only. A missing file fails, naming the expected path
and how to create one.

## Fields

### Top level

| Field       | Required? | Meaning                                                                                                    |
| ----------- | --------- | ---------------------------------------------------------------------------------------------------------- |
| `nodes`     | yes       | The node entries — at least one                                                                             |
| `prefer`    | no        | `idle` (default) or `active` — how routing ranks several nodes that could all serve; see [Spreading or consolidating](#spreading-or-consolidating) |
| `wake`      | no        | `on` (default) or `off` — whether routing may start an engine on a node that is not running one; see [Waking](#waking) |
| `concurrency` | no      | The most work the fleet may have in flight at once, for the [`spinloop orchestrator`](commands/orchestrator.md); see [Concurrency](#concurrency) |
| `apiKeyEnv` | no        | The variable holding the key the fleet's `kind: cloud` nodes share; see [Tokens](#tokens)                 |
| `gateway`   | no        | The address a [`spinloop gateway`](commands/gateway.md) serves the fleet under; see [Gateway](#gateway)    |

### A node

| Field            | Required?            | Meaning                                                                                                                        |
| ---------------- | -------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| `name`           | yes                  | Unique in the file; what you type at `fleet start <node>`. For a `kind: cloud` node, the registered environment it drives    |
| `host`           | for `kind: daemon`   | Where the daemon answers — a LAN name, a tailscale name, or an address                                                          |
| `port`           | no                   | The daemon's control API port; 4242 when omitted                                                                                 |
| `kind`           | no                   | `daemon` (default) or `cloud`; see [Cloud environments](#cloud-environments)                                                  |
| `tokenEnv`       | no                   | The variable holding this daemon's bearer token; none means no authentication (a loopback-only daemon)                           |
| `engineTokenEnv` | no                   | The variable holding the key this node's *engine* is gated with; see [Tokens](#tokens)                                           |
| `engine`         | no                   | An override of where the engine serves — `host`, `port`, `path`, each optional; see [Where a node's engine answers](#where-a-nodes-engine-answers) |
| `file`           | no                   | The [Spinloop](spinloop-file.md) that describes what this node runs; see [A node's Spinloop source](#a-nodes-spinloop-source)  |
| `instance-type`  | no, `kind: cloud` only | The EC2 instance type the environment launches as, e.g. `g6e.xlarge`; see [Cloud environments](#cloud-environments)          |
| `tags`           | no                   | Key/value pairs naming the kind of work the node takes on; only the [`spinloop orchestrator`](commands/orchestrator.md) reads them; see [Tags](#tags) |

### The `gateway` section

| Field      | Required? | Meaning                                                                                          |
| ---------- | --------- | ------------------------------------------------------------------------------------------------ |
| `url`      | yes       | The gateway's address, carrying a scheme — `http://` or `https://`                                |
| `tokenEnv` | no        | The variable holding the gateway's token; `OPENAI_API_KEY` when absent                             |
| `name`     | no        | The label the gateway wears in a model picker; the address's host when absent                      |

## Where a node's engine answers

A node's `host` and `port` name its **daemon**, which is a different port from
the **engine** it supervises. For [routing](commands/fleet.md#which-node-would-i-get)
spinloop needs the engine's, and the daemon reports it — so most nodes need
nothing more. Declare an `engine` block for the cases a daemon cannot describe:

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

Each field falls back independently to what spinloop would otherwise derive: the
node's own `host`, and the port and path the daemon reports.

An engine bound to loopback answers only on its own machine. Routing to it from
elsewhere fails with that explanation rather than a bare connection refused —
bind the engine to a reachable address (llama.cpp's `--host 0.0.0.0`), or
declare an `engine` block, which is you taking responsibility for reachability.

## Cloud environments

`kind` (defaulted to `daemon`) says how the fleet reaches a node. A node can
also be an [`spinloop cloud`](commands/cloud.md) environment rather than a
machine: its `name` is the registered environment it drives — no `host` needed
— and it is reached through its control plane, which signs each call with your
AWS credentials, so it needs no bearer token:

```yaml
nodes:
  - name: qwen          # the registered environment, and what you type at `fleet start <node>`
    kind: cloud
```

The environment's control URLs live in its `cloud.json` (under
`~/.config/spinloop/clouds/<name>/`), written by `spinloop cloud deploy` — or
by [`spinloop fleet deploy`](commands/fleet.md#deploying-cloud-nodes), which
creates it from the fleet file itself — and never stored in the fleet file. So a
daemon and an environment sit side by side as the same kind of row, and an
environment that has not been deployed yet shows as `config-error` on its row
rather than blanking the fleet. See
[`examples/fleet-cloud`](https://github.com/spinloop-ai/spinloop/blob/main/examples/fleet-cloud/README.md) and
[`examples/fleet-mixed`](https://github.com/spinloop-ai/spinloop/blob/main/examples/fleet-mixed/README.md).

A `kind: cloud` node may also name the EC2 instance type its environment
launches as, with `instance-type` (a family and size separated by a dot, e.g.
`g6e.xlarge`):

```yaml
nodes:
  - name: qwen
    kind: cloud
    instance-type: g6e.2xlarge
```

It is a property of the cloud environment, not of the fleet's view of it:
`fleet deploy` records it on the environment, and the environment's next
**fresh** launch uses it. A re-wake of a stopped instance keeps the type it was
launched with — EC2 cannot resize a running or stopped box — so a changed value
takes effect only after the instance is terminated (an idle sweep or
`spinloop cloud stop`) and launched again. Omitted, the environment launches as
its control plane's default type. Naming `instance-type` on a `kind: daemon`
node is a configuration error: a daemon's hardware is the operator's to choose,
not the fleet file's.

## A node's Spinloop source

Both `fleet deploy` (for a `kind: cloud` node's environment) and `fleet start`
(for a `kind: daemon` node's engine) need to know what Spinloop file describes
what a node runs. A node names it with `file`, resolved relative to the fleet
file:

```yaml
nodes:
  - name: qwen
    kind: cloud
    file: ./envs/qwen.Spinloop
```

`file` is optional, because the node's own `name` already doubles as a lookup
key. When it is absent, resolution tries, in order:

1. `name` registered as a `spinloop alias` (`spinloop alias add qwen
   ./envs/qwen.Spinloop`) — the same lookup `spinloop cloud deploy` performs
   for a Spinloop argument;
2. a subdirectory named after the node, beside the fleet file — `qwen/Spinloop`
   next to `fleet.yaml` for a node named `qwen`, no fields needed on either side.

A fleet laid out as one subdirectory per node therefore needs nothing beyond
each node's own `name`:

```
fleet.yaml
qwen/Spinloop
llama/Spinloop
```

Nothing resolving is a per-node error naming all three ways a source could have
been given. For `fleet deploy` that always fails the node (there is nothing to
create an environment from); for `fleet start` on a `kind: daemon` node it
likewise fails that node's start — there is no fallback to a plain, config-less
start once this field exists. A `kind: cloud` node's `start` is unaffected by
any of this: what it serves is fixed at deploy time, not pushed at start time.

This does not apply to `spinloop dashboard`'s `s` key, which still starts
the selected node with a plain start, whatever the CLI's `fleet start` would
resolve for it.

## Spreading or consolidating

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

`spinloop harness open --prefer <value>` and `spinloop fleet route --prefer <value>`
override the file for one command, which is the cheap way to see what the other
setting would do before committing to it.

## Waking

`wake` decides whether routing may start an engine on a node that is not
running one:

```yaml
wake: off      # or: on
nodes: …
```

- **`on`** (the default, and the behaviour of a file that declares nothing) —
  when nothing is serving, spinloop starts a node and waits for its engine to
  answer before the agent launches or the request is answered.
- **`off`** — a request nothing is serving fails rather than starting anything,
  naming the node that would have been woken and the `spinloop fleet start
  <node>` command that would start it. Use it where the machines are not to be
  started on demand — the models are loaded by hand, or someone else drives the
  starts.

An explicit `--no-wake` still refuses to start anything, whatever the file
says; an explicit `spinloop fleet start` does the opposite — it always starts,
because it was asked.

A node MAY declare its own `wake`, overriding the file's setting for that
node alone:

```yaml
wake: on
nodes:
  - name: gpu-box
    host: 198.51.100.7
  - name: prod
    kind: cloud
    wake: off   # this one node stays asleep even though the fleet wakes
```

This matters most for a `kind: cloud` node, whose wake boots a billed cloud
instance rather than starting a process on a machine you already run — so you
can leave the fleet's daemons on `wake: on` while deciding a given cloud
environment's waking separately, in either direction: `wake: off` on one node
under a fleet that otherwise wakes, or `wake: on` on one node under a fleet
that otherwise does not.

## Tags

A node's `tags` name the kind of work the node takes on — key/value pairs the
operator chooses:

```yaml
nodes:
  - name: gpu-box
    host: 198.51.100.7
    tags:
      gpu: a100
      os: linux
```

They are how an [`spinloop orchestrator`](commands/orchestrator.md) item chooses its
node: an item names the tags of the nodes it may run on, and it matches a node
only where every one it names is a tag the node carries. A node with no tags
takes only items that name none. Routing, waking and the dashboard do not read
them — tags belong to the orchestrator's matching alone.

## Concurrency

`concurrency` is the pace this fleet works at, for the
[`spinloop orchestrator`](commands/orchestrator.md): how much work it may take at once.
The limits are a ceiling the operator sets, not a measurement of the engines'
load:

```yaml
concurrency:
  total: 8          # the most items the fleet may have in flight at once
  tags:
    "gpu=a100": 4   # and, per tag, the most in flight on nodes carrying it
```

An admitted item counts against `total` and against the limit of every tag it
names, and it frees its counts when it ends. A limit on a tag no node carries
is a configuration error naming the tag, as is a limit that is not a positive
integer. A file that declares no `concurrency` has no limit: the orchestrator
admits as fast as the nodes match.

## Gateway

`gateway` names the address this fleet is served under by a
[`spinloop gateway`](commands/gateway.md): a launch routed through this file is
pointed at the gateway rather than at a node. The gateway has done the choosing,
so the launch queries no node and wakes none:

```yaml
nodes: …
gateway:
  url: http://gateway.internal:4000   # required, with a scheme
  tokenEnv: GATEWAY_TOKEN             # optional; OPENAI_API_KEY when absent
  name: remote-llms                   # optional; labels the gateway (see below)
```

A launch through such a file is pointed at the section's address — the agent's
base URL, with the OpenAI-compatible `/v1` prefix added when it carries no path
— and the token is resolved from the variable the section names —
`OPENAI_API_KEY` when it names none — the way a key is resolved elsewhere: an
`ENV` instruction, then the process environment, then the `.env` beside the
Spinloop. A variable set nowhere fails the launch before anything is written,
naming the variable.
As with a node's choice, the launch reports the address on stderr before the
agent starts.

This is how a machine that holds the fleet file points a harness at the fleet:
A launch reads the section when it is there, so a Spinloop
beside the file needs only the model, and the address travels with the file.
`spinloop fleet route` answers a file that names a gateway the same way — the
address, and that no node is queried and nothing is started.

When a launch through the gateway has no model of its own to route by (see
[Launching the harness](commands/fleet.md#launching-the-harness)), it needs a
way to label the provider it configures — otherwise every gateway a fleet might
name would collide under the same generic id. `name` supplies that label
directly; with none given, the section's address's host stands in (e.g.
`localhost:4000`). Either way opencode and Pi show it the way a cloud
environment is shown — `Gateway (remote-llms)` rather than a bare
`OpenAI-compatible`, the same pattern as `llama.cpp (dev-2)`.

## Tokens

The file names secrets by the variables that hold them; the values are resolved
from the process environment first, then a `.env` beside the `fleet.yaml` — the
same precedence spinloop uses everywhere, so an exported value wins and the
`.env` only fills a gap. Put the secrets there:

```sh
# .env beside fleet.yaml (gitignored)
GPU_BOX_TOKEN=…
```

There are three references, all resolved the same way:

- **`tokenEnv`** — the node's daemon bearer token. A node with no `tokenEnv`
  is contacted without authentication, which is correct for a daemon bound to
  loopback. Any node reachable over the network needs a token — the daemon
  refuses to listen on a non-loopback address without one.
- **`engineTokenEnv`** — the key the node's **engine** is gated with. The two
  are different credentials — one authorises driving the node, the other
  authorises using its engine — and a node may need either, both, or neither.
  The daemon never hands its engine's key out: it says only that one is
  required.
- **`apiKeyEnv`** (top level) — the default key for every `kind: cloud` node.
  A cloud environment is always keyed, so a node that names no
  `engineTokenEnv` of its own takes the fleet-wide reference. A node's own
  `engineTokenEnv` overrides it, so one cloud node may carry a distinct key while
  the rest of the fleet shares one. A `kind: daemon` node never takes the
  fleet-wide reference: it is gated only by its own `engineTokenEnv`.

```yaml
  - name: gated
    host: gated.local
    tokenEnv: GATED_TOKEN             # to drive the daemon
    engineTokenEnv: GATED_ENGINE_KEY  # to talk to its engine
```

A `kind: cloud` environment is always keyed, so it needs an engine key too —
its `engineTokenEnv` works as above, and a fleet-wide `apiKeyEnv` is the
default for every cloud node that does not name one of its own:

```yaml
apiKeyEnv: REMOTE_ENGINE_KEY   # the default for every kind: cloud node
nodes:
  - name: qwen
    kind: cloud
    engineTokenEnv: OTHER_KEY  # overrides it for this node
```

A reference naming a variable that is set nowhere is reported against that
node, naming the variable, in the same way a missing daemon token is — on the
row as `config-error`, so a typo shows up there rather than as a mysterious
`unauthorized`, and a launch fails before it starts the agent rather than
pointing it at a gate it cannot pass.

## What the file refuses

A fleet file is checked when it is read, and a file that fails is refused with
the reason named:

- no `nodes` — list at least one;
- a node with no `name`, or two nodes with the same name;
- a `kind: daemon` node with no `host`;
- an unknown `kind` — only `daemon` and `cloud` are supported;
- a `kind: cloud` node whose `name` is not shaped like a registered
  environment name (no `/`, no trailing `.json`) — the name is the key of the
  environment it drives;
- an `instance-type` that is not shaped like an EC2 instance type (a family and
  size separated by a dot, e.g. `g6e.xlarge`), and `instance-type` on a
  `kind: daemon` node at all;
- a `prefer` other than `idle` or `active`, naming both accepted values;
- a `wake` other than `on` or `off`, naming both accepted values;
- a `gateway` section with no `url`, and a `url` with no scheme.

Token references are not checked at read time — a `tokenEnv` that resolves to
nothing shows up per node when a command needs it, as described under
[Tokens](#tokens).

## Examples

Fleet files, each with a walkthrough:

- [`examples/fleet-local/`](https://github.com/spinloop-ai/spinloop/tree/main/examples/fleet-local) — a fleet of one, on your own machine
- [`examples/fleet/`](https://github.com/spinloop-ai/spinloop/tree/main/examples/fleet) — a small LAN fleet, all defaults
- [`examples/fleet-docker/`](https://github.com/spinloop-ai/spinloop/tree/main/examples/fleet-docker) — a runnable multi-node fleet in containers
- [`examples/fleet-cloud/`](https://github.com/spinloop-ai/spinloop/tree/main/examples/fleet-cloud) — a fleet of cloud environments
- [`examples/fleet-mixed/`](https://github.com/spinloop-ai/spinloop/tree/main/examples/fleet-mixed) — daemons and environments side by side
- [`examples/gateway-docker/`](https://github.com/spinloop-ai/spinloop/tree/main/examples/gateway-docker) — a fleet behind its gateway

## See also

- [`spinloop fleet`](commands/fleet.md) — the commands that read this file
- [`spinloop gateway`](commands/gateway.md) — the server a `gateway` section names
- [The `Spinloop` file](spinloop-file.md) — the format a node's `file` points at
- [`spinloop daemon`](commands/serve.md) — what runs on each node
