# Run a fleet

Every machine you run serves engines from a [`spinloop daemon`](daemon.md); a
`fleet.yaml` names those machines, and `spinloop` observes and drives all of
them from one place — status, metrics, an interactive dashboard, starts and
stops, and logs. A fleet can also hold [cloud environments](cloud.md) as
nodes, beside daemons, in the same rows.

```sh
spinloop status                # one row per node: state and what it serves
spinloop dashboard             # the interactive tiled view — watch it, drive it
spinloop fleet metrics         # each node's engine + system metrics
spinloop fleet route my-spinloop   # which node a harness launch would pick
spinloop fleet start gpu-box   # start one or more nodes' engines
spinloop fleet start --all     # start every node in the fleet
spinloop fleet stop gpu-box    # stop one or more nodes' engines
```

`status` and `dashboard` are top-level verbs — they take the same target
(`./fleet.yaml`, `--fleet <path>`, or `--env <name>` for one environment as a
fleet of one), and the `fleet` subcommands drive the nodes: `start`, `stop`,
`deploy`, `route`, `metrics`, and `logs`.

## Name your machines

`fleet.yaml` is a list of nodes and how to reach each one. It holds **no
secrets** — a node that needs a bearer token names the environment variable
holding it:

```yaml
nodes:
  - name: studio            # what you type at `fleet start <node>`
    host: studio.local      # LAN name, tailscale name, or an address

  - name: gpu-box
    host: 198.51.100.7      # a tailscale address, say
    port: 4242              # optional; the daemon's default when omitted
    tokenEnv: GPU_BOX_TOKEN # the *name* of the variable, never the token

  - name: qwen              # a cloud environment, beside the daemons
    kind: cloud
```

A `kind: cloud` node is a registered [cloud environment](cloud.md): its
`name` is the registered one, no `host` is needed, and it is reached through
its control plane. The file is found the way a `Spinloop` is — `./fleet.yaml`
in the working directory, or `--fleet <path>`.

The daemon's port is not the engine's: a node's `host` and `port` name its
*daemon*, and the daemon reports where its *engine* answers, so most nodes
need nothing more.

## Try it without any hardware

[`examples/fleet-docker/`](https://github.com/spinloop-ai/spinloop/tree/main/examples/fleet-docker)
brings up a real three-node fleet in containers — real daemons, real auth, a
fake engine — so you can see all of this working before setting up a single
machine:

```sh
cd examples/fleet-docker && cp .env.example .env
docker compose up -d --build
set -a && . ./.env && set +a
spinloop status --fleet ./fleet.yaml
```

## Point your agent at the fleet

A launch routed through a fleet file picks a node and launches against it, so
the machine you are sitting at needs no engine of its own:

```sh
spinloop code -f fleet.yaml             # apply ./Spinloop, route it, launch
spinloop code -f fleet.yaml --node gpu-box   # pin the node
```

Routing prefers a node already serving the wanted model; when nothing is,
spinloop picks a node that is not running, tells it what to serve, starts it,
and waits for its engine to answer. A node already running is never stopped to
make room — a fleet with every machine busy on other models fails rather than
displacing anyone. `--no-wake` refuses to start anything.

Two settings shape the choice, in the fleet file:

- **[`wake`](../fleet-file.md#waking)** (`on`, the default) decides whether
  routing may start an engine on a node that is not running one. Set it `off`
  where machines are not to be started on demand; a node may declare its own
  `wake` to override the file — most useful for a cloud node, whose wake
  boots a billed cloud instance.
- **[`prefer`](../fleet-file.md#spreading-or-consolidating)** ranks nodes that
  could all serve you: `idle` (the default) takes the machine quietest
  longest; `active` consolidates onto the busy one.

A fleet file that names a [gateway](gateway.md) points the agent there instead,
so the address lives in the file rather than in every Spinloop.

## Where next

- [`spinloop fleet`](../commands/fleet.md) — the driving subcommands, in full
- [The `fleet.yaml` file](../fleet-file.md) — every field, and what the file refuses
- [Serve the fleet as a gateway](gateway.md) — one OpenAI-compatible address for all of it
- [Work a backlog](work-items.md) — one-shot agents against the fleet, at its declared pace
