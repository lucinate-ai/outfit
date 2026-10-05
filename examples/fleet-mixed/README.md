# A mixed fleet

One `fleet.yaml`, one set of commands, two kinds of node: machines running
`spinloop daemon` and [`spinloop cloud`](../../docs/commands/cloud.md)
environments. The same fan-out reaches every node, so the fleet reads as a
single table.

## How to run

### 1. Bring up a daemon (a machine node)

On the box you want in the fleet, run the daemon — it takes no Spinloop of
its own, just its flags:

```sh
SPINLOOP_API_TOKEN=… spinloop daemon
```

Put that token in a `.env` beside `fleet.yaml` (copy [`.env.example`](.env.example)).
This is the same as [`examples/fleet`](../fleet/README.md); for daemons in
containers rather than real machines, see
[`examples/fleet-docker`](../fleet-docker/README.md). What it runs is decided
by whoever starts it — see the next section.

### 2. Give each node a Spinloop source, then bring them up

Every node here finds its Spinloop through the subdirectory convention —
[`gpu-box/Spinloop`](gpu-box/Spinloop) for the machine,
[`qwen/Spinloop`](qwen/Spinloop) and [`llama/Spinloop`](llama/Spinloop) for
the two environments — so none of them declares a `file` field in
`fleet.yaml`. A registered `spinloop alias` named after a node would resolve
the same way, and win over the subdirectory; an explicit `file` field can
point anywhere else again, which is what
[`examples/fleet-docker`](../fleet-docker/) uses instead. See
[the fleet file](../../docs/fleet-file.md#a-nodes-spinloop-source) for
the full resolution order.

```sh
spinloop fleet start gpu-box   # tell the daemon what to run, and start it
spinloop fleet deploy --all    # create both environments from this file
```

Creating the environments this way is the same as running `spinloop cloud
deploy --env <name>` once per node in this file, from the directory holding
that node's `Spinloop` — see [`spinloop
cloud`](../../docs/commands/cloud.md) — just one command for both.

### 3. Observe the whole fleet

```sh
spinloop status        # one row per node: the machine and the environments
spinloop metrics -w    # a live dashboard
spinloop fleet start qwen    # wake a sleeping environment from zero
spinloop fleet stop gpu-box  # stop the machine's engine
```

Every node renders as the same kind of row: a daemon that is down shows
`unreachable`, an environment that is not deployed yet shows `config-error`,
and the rest of the fleet still shows.

## See also

- [`examples/fleet`](../fleet/README.md) — a fleet of daemons only
- [`examples/fleet-cloud`](../fleet-cloud/README.md) — a fleet of cloud environments only
- [`spinloop fleet`](../../docs/commands/fleet.md) — the commands over these nodes
- [The fleet file](../../docs/fleet-file.md) — the file's format, node kinds, and routing
