# outfit fleet

Observe and drive every engine you run, from one place. Each machine runs
[`outfit daemon`](serve.md#the-control-api---api-and-outfit-daemon); a
`fleet.yaml` names them, and `outfit fleet` fans out over their control APIs.

```sh
outfit fleet status          # one row per node: state and what it serves
outfit fleet metrics         # each node's engine + system metrics
outfit fleet metrics -w      # a live dashboard, redrawn in place
outfit fleet start gpu-box   # start one node's engine
outfit fleet stop gpu-box    # stop it
```

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
| `--format` | `metrics` only: `bar` (default), `table`, or `json` |
| `-w`, `--watch` | `metrics` only: redraw on an interval until interrupted |

## See also

- [`examples/fleet-docker/`](../../examples/fleet-docker/) — a runnable fleet
- [`outfit daemon`](serve.md) — what runs on each node
- [HTTP Control API](../http-api.md) — the API the fleet client speaks
- [Environment variables](../env-vars.md)
