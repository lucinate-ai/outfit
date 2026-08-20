# A fleet of remote environments

`outfit fleet` observes [`outfit remote`](../../docs/commands/remote.md)
environments the same way it observes machines running `outfit daemon`: each
environment becomes a node. There are no bearer tokens here — the control plane
signs each call with your AWS credentials — and the file names environments,
never an account, so it is safe to keep under version control.

## How to run

### 1. Have some environments

An environment is created and registered when you deploy into it: an `Outfit`
that says `REMOTE <name>`, run through `outfit remote deploy`, writes that
environment's control URLs to `~/.config/outfit/remotes/<name>/remote.json`.
Deploying needs the shared control plane once before it — see [`outfit
remote`](../../docs/commands/remote.md). List what you already have:

```sh
outfit remote ls
```

### 2. Name them as a fleet

[fleet.yaml](fleet.yaml) lists the environments — the node's name is the environment:

```yaml
nodes:
  - name: qwen        # the registered environment, and what you type at `fleet start qwen`
    kind: remote
```

### 3. Observe from anywhere

From any machine your AWS credentials reach:

```sh
outfit fleet status        # one row per environment
outfit fleet metrics -w    # a live dashboard
outfit fleet start qwen    # wake a sleeping environment from zero
outfit fleet stop qwen     # scale it back down
```

An environment that has not been deployed yet shows as `config-error` on its
row, and one that is down shows as `unreachable` — either way, the rest of the
fleet still renders.

## See also

- [`outfit fleet`](../../docs/commands/fleet.md) — the fleet file, its node kinds, and routing
- [`outfit remote`](../../docs/commands/remote.md) — the environments these nodes drive
