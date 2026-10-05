# spinloop fleet

Observe and drive every engine you run, from one place. Each machine runs
[`spinloop daemon`](serve.md#the-control-api-api-and-spinloop-daemon); a
`fleet.yaml` names them, and `spinloop fleet` fans out over their control APIs.

```sh
spinloop status                  # one row per node: state and what it serves
spinloop dashboard               # the interactive tiled view — watch it, drive it
spinloop metrics           # each node's engine + system metrics
spinloop metrics -w        # the same, redrawn in place until interrupted
spinloop fleet route my-spinloop # which node a harness launch would pick
spinloop fleet start gpu-box     # start one or more nodes' engines
spinloop fleet start --all       # start every node in the fleet
spinloop fleet stop gpu-box      # stop one or more nodes' engines
spinloop fleet deploy --all      # create every kind: cloud node's AWS environment
```

[`spinloop status`](status.md) and [`spinloop dashboard`](dashboard.md) are
top-level commands, not part of this group: they read whatever target you name
— a fleet file, or a single registered environment — so there is one command
for "what is running", however it is configured.

A fleet is also where [`spinloop harness open`](harness.md#launching-against-your-fleet)
sends an agent: a launch routed through a fleet file picks a node and launches
against it, so the machine you are sitting at needs no engine of its own.

## Which fleet a command acts on

Every `spinloop fleet` command takes its target one of three ways:

| | target |
| --- | --- |
| `--env <name>` | one registered environment, as a fleet of one |
| `--fleet <path>` (`-f`, except on `logs`) | that fleet file |
| neither | the `fleet.yaml` in the working directory |

`--env` and `--fleet` name two different things, so passing both fails saying
so rather than picking one. A `fleet.yaml` merely sitting in the working
directory is not a conflict: only a flag states a target, so `--env` simply
wins and the file is not read.

### One environment, no fleet file

A registered environment and a one-node fleet file naming it describe the same
thing, so `--env` lets you skip writing the file:

```sh
spinloop status --env qwen      # the same row a one-node fleet file gives
spinloop dashboard --env qwen   # the tiled view, on one environment
spinloop logs --env qwen        # its engine's log
```

Because such a fleet has no file, it carries none of the settings a fleet file
supplies — no `prefer`, no wake policy, no `gateway`, no concurrency limits —
and takes each of their defaults. All of them describe how several nodes are
used, which a fleet of one has no occasion for; put the environment in a
`fleet.yaml` when you want any of them.

To launch an agent against a single environment, use
[`spinloop code --env <name>`](code.md), which configures the harness from what
that environment reports is deployed.

## Try it without any hardware

[`examples/fleet-docker/`](https://github.com/spinloop-ai/spinloop/tree/main/examples/fleet-docker)
brings up a real three-node fleet in containers — real daemons, real auth, a fake engine — so
you can see all of this working before setting up a single machine:

```sh
cd examples/fleet-docker && cp .env.example .env
docker compose up -d --build
set -a && . ./.env && set +a
spinloop status --fleet ./fleet.yaml
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

The file is found the way a `Spinloop` is: `./fleet.yaml` in the working
directory, or `--fleet <path>`. The full format reference — every field, a
node's [Spinloop source](../fleet-file.md#a-nodes-spinloop-source),
[cloud environments](../fleet-file.md#cloud-environments),
[`prefer`](../fleet-file.md#spreading-or-consolidating) and
[`wake`](../fleet-file.md#waking), [tags](../fleet-file.md#tags) and
[concurrency](../fleet-file.md#concurrency), the [gateway section](../fleet-file.md#gateway),
and [tokens](../fleet-file.md#tokens) — is [the `fleet.yaml` file](../fleet-file.md).

## A node that is down never blanks the view

Fan-out is for observing, so a node that cannot be reached is a **row**, not a
failure — the rest of the fleet still renders and the command still exits 0:

```
NODE     STATE         SERVING
studio   running       llamacpp  org/qwen  (up 1h 2m 5s)  (active 12s ago)
gpu-box  idle          llamacpp  org/qwen
offline  unreachable   dial tcp 10.0.0.9:4242: connect: connection refused
```

"active" comes from the activity each daemon tracks, so a glance answers
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

`spinloop metrics` renders each node's engine and system metrics in the
same `gauge` (default), `bar`, `table`, and `json` formats as
[`spinloop metrics --env <name>`](cloud.md) — they share the renderers, so a node in
your fleet and a cloud endpoint look the same. `gauge` draws the current
reading per series as a filled progress gauge; `--format=bar` draws each
series as a sparkline of the node's daemon's retained history instead, and a
node whose daemon reports no history falls back to the gauge drawing of its
current reading, so a fleet mixed with older daemons renders each node the
best way it can. A stopped node keeps its readings, so its sparkline runs to
the stop.

Each node's block carries the same `active` figure the status table
shows, for the reasons given above, and on the same terms: absent until the
node's engine has done some work. A node whose engine has *stopped* still
shows it — the daemon keeps the record across a stop, and "how long since this
did anything?" is worth more about a stopped engine than about a busy one.

A `kind: cloud` environment carries a relative keep after that figure, on the
same line — `active  2m 5s ago  keep for 2h` — on the same omitted-when-absent
terms: it shows how long the idle sweep will hold the box while the deadline is
in the future, and is gone once it has passed or was never set. It is the same
line the dashboard draws on a kept environment's tile and detail screen, from
the same read.

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

## The dashboard

`spinloop dashboard` is that same board as a live view: one tile per
node, repainted in place, each drawing exactly what `metrics`' gauge
format prints for the node — state and uptime, what it serves, the CPU/GPU/RAM
gauges, the token counters — so the view and the one-shot command never
word a number differently. `g` toggles every tile between the gauge drawing
of the current reading and the sparklines; the board opens in gauge. A node
that
is down is a tile that says why, and a node whose token reference resolves to
nothing holds that reason for the life of the view:

```sh
spinloop dashboard                # ./fleet.yaml
spinloop dashboard --fleet f.yaml # another fleet file
```

| Key | Does |
| --- | ---- |
| `j`/`k` or the arrows | Move the selection, in file order (no wrap) |
| `PgUp`/`PgDn` | Page the grid when there are more nodes than fit |
| `Enter` | Open a full-screen view of the selected node |
| `r` | Force a refresh of every node, now |
| `g` | Toggle every tile's resource series between bar (sparklines of the retained history) and gauge (the current reading) |
| `s` | Start the selected node — without confirmation — shown only for a node that is not running, and only while it has no action in flight |
| `k` | Keep a cloud environment for a duration you type — shown only for a node that can be kept, and only while it has no action in flight |
| `a` | Abandon a start in flight on the selected node — the wait ends, the node is free again (a stop in flight is not abortable) |
| `x` | Stop the selected node — it asks first (`y` sends, `n` or `esc` cancel) — shown only for a node that is running, and only while it has no action in flight |
| `q` or `Ctrl+C` | Leave |

The board keeps its own cadence: local machines are read every two seconds,
and a [`kind: cloud`](../fleet-file.md#cloud-environments) environment every 60 — one
status call a minute, because its status is a signed control-plane call, not a
local socket, and a cold instance changes state on the scale of minutes. `r`
is due for every node whatever those deadlines say.

`start` runs the same node operation `fleet start` does, without
confirmation, and carries no deadline, because a cloud wake takes minutes and
the call holds for the lot. While it runs, the node's tile carries the start —
the verb and the control plane's own status lines, in place of the node's last
report — because that is the truth until the report returns. An action is one
per node, not one per board: while one node is waking, select another and
start it, and the two wakes run side by side, each reported on its own tile.
When an action finishes, its tile goes back to the node's next report and its
outcome lands on the status line at the foot of the view. A start's wait can
be abandoned: `a` ends the dashboard's wait on the node's in-flight start,
and the tile is free to start or stop again. The abort ends the wait, not
the work — a cancelled client cannot take a wake the cloud is carrying back
— so the line says the wait was *abandoned*, not that the node failed, and a
wake that was in fact completing shows up as a running node on the next
refresh. A stop in flight is not abortable: it targets an engine already
running rather than a cold wake with no deadline of its own, and `a` drives
nothing while one is in progress.

`keep` is a cloud-environment action: a local daemon has no idle sweep, so
there is no deadline to set, and the key does not show for one. Pressing it
opens a prompt at the foot of the view, pre-filled with `4h`, asking how long
the environment should be retained. The prompt is the confirmation — there is
no second one — so the operator sees the duration it will set before choosing
to send it: a keep overwrites the deadline and ends nothing, where a stop
ends something and so asks. Type the duration and press `enter` to send it;
`esc` cancels; `q` or `Ctrl+C` cancel the prompt and leave the dashboard, as
the stop confirmation does. An entry that does not parse as a positive
duration leaves the prompt open and shows the parse reason in the footer's
hint slot, so the entry is kept and corrected in place. While the keep runs
its tile carries it, and it is not abortable — one fast signed call, so `a`
drives nothing on it. When it finishes, the status line reports the deadline
the control plane set and the node is re-read at once, which is what brings
the relative `keep for …` figure onto the tile and detail screen at the node's
next round rather than waiting out its full cadence.

Everything else in the view is `status`/`metrics`/`logs` in place — it
is read-only apart from those four action keys. It needs a real terminal: a
piped run is refused, and it says so by way of `metrics --watch`, which
is the streamable surface.

### The node detail view

`Enter` on a tile opens a full-screen view of that node in place of the grid:
its metrics, unclipped to the tile's 42 columns, its engine log tailed and
followed the way `logs -f` follows one node, and a footer naming the
keys the view answers to. `Esc` closes it and returns to the grid with the
same node still selected.

```sh
spinloop dashboard
# select a node, press Enter for its full metrics and log, Esc to go back
```

`s`, `k`, `x` and `a` drive the node shown exactly as they drive the selected
node on the grid — the same no-confirmation start, the same keep prompt, the
same stop confirmation, the same abandon. `q`/`Ctrl+C` are grid keys only and
do nothing here — `Esc` back to the grid first, then quit from there — so a
stray quit keystroke while looking at a node can't end the session out from
under you. The one exception is the keep prompt: while it is open it answers
to `q`/`Ctrl+C` the way the stop confirmation does, cancelling and leaving. The rest of the
fleet keeps refreshing behind the view, and any action already in flight on
another node keeps running. A node whose engine has never run shows the same
explanation `logs` gives for it, not an empty pane.

`f` pauses and resumes the log's follow, independently of everything else in
the view — the metrics section keeps refreshing either way. The header names
the state (`log: following` / `log: paused`). Pausing does not lose anything:
resuming fetches whatever the engine wrote in the meantime, the same as a
poll that simply ran late.

## Logs

`spinloop logs` prints what your engines actually said — the answer to the
question `status` raises when it reports a node as `crashed`.

```sh
spinloop logs              # the tail of every node's engine log
spinloop logs gpu-box      # just that node
spinloop logs -f           # follow, until you interrupt it
spinloop logs --limit 500  # more backlog per node
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

`spinloop fleet route` reports the node a
[harness launch](harness.md#launching-against-your-fleet) would pick for an
Spinloop, and **changes nothing** — no config pushed, no engine started, no
harness config written:

```sh
spinloop fleet route my-spinloop
spinloop fleet route --prefer active my-spinloop
```

```
Spinloop: ./my-spinloop/Spinloop
Fleet:  ./fleet.yaml
Prefer: idle

Would use gpu-box at http://gpu-box:8080/v1
  serving qwen3-27b, active 312s ago (prefer idle)
```

A file that names a [gateway](../fleet-file.md#gateway) is answered the way a launch answers
it — the gateway's address, and that no node is queried and nothing is
started.

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

## Launching the harness

Launching an agent against a fleet is [`spinloop code`](code.md) — or
[`spinloop harness open`](harness.md#launching-against-your-fleet), which it
shortens. There is no fleet-level spelling: `spinloop fleet harness` was
removed, and typing it names its replacement.

```sh
spinloop code                            # the Spinloop and fleet.yaml beside it
spinloop code my-spinloop --fleet fleet.yaml
spinloop code -O=./client/Spinloop --fleet fleet.yaml
spinloop code --fleet fleet.yaml --node gpu-box   # the launch's steering flags
```

A fleet file that names a [gateway](../fleet-file.md#gateway) points the agent there, so the
address lives in the file rather than in every Spinloop.

The fleet comes from `--fleet`/`-f`, or from the `fleet.yaml` in the working
directory when the Spinloop was not named explicitly. A Spinloop you give the
path of travels to its fleet only by flag — so `spinloop code -O=./x/Spinloop`
needs `--fleet` to route, while a bare `spinloop code -O` beside a `fleet.yaml`
picks it up.

Routing is the launch's routing: at the gateway where the file names one,
otherwise by node selection and, where the file's
[wake policy](../fleet-file.md#waking) allows, a wake — `--node`, `--prefer`, `--no-wake` and
`--wake-timeout` steer it. A Spinloop that pins a `BASEURL` is not routed, and
a variable already set in spinloop's environment wins.
model per request — so a launch through one needs no Spinloop at all: the
harness is configured with a generic OpenAI-compatible provider at the
gateway's address, its model list populated from the gateway's own
`GET /v1/models`, and no default model — labelled and keyed by the gateway's
[`name`](../fleet-file.md#gateway), or its address when the section names none, so a second
gateway gets its own block rather than overwriting this one (see
[Gateway](../fleet-file.md#gateway)). This applies equally to a Spinloop that is given but
names neither a `MODEL` nor an `ALIAS`. The populated model list is only as
fresh as the last run of the command — rerun it to pick up a newly-served
model — and, since not every harness's config format holds more than one
model per provider, it applies to opencode and Pi; a launch against lucinate
still gets a working connection to the gateway, just with no model list to
populate.

## Starting and stopping

`fleet start` and `fleet stop` take one or more node names, or `--all` for
the whole fleet:

```sh
spinloop fleet start gpu-box           # one node
spinloop fleet start gpu-box gpu-box-2 # several
spinloop fleet start --all             # every node in the file
spinloop fleet stop --all
```

With neither a node nor `--all` they list the fleet and do nothing, rather
than acting on the whole fleet by accident; `--all` together with node names
is refused as ambiguous. An unknown name fails before anything is touched,
naming the nodes you could have meant. A fleet directory's
[`spinloop up`](up.md) skips the choice: bare `up` starts every node,
`up <node>` the named ones. Several targeted nodes are driven
independently — one node's failure is reported against it alone and does not
stop the others, and the command exits non-zero if any of them failed. The
daemon's own rules still hold: starting a node whose engine is already
running reports its conflict, and stopping one that is not running succeeds
quietly.

**Starting a `kind: daemon` node now requires its [Spinloop
source](../fleet-file.md#a-nodes-spinloop-source) to resolve.** When it does, `fleet start`
derives a deploy config from it and pushes it with the start (`StartWith`) —
telling the daemon what to run, the same way a routed `harness` launch
already tells a node what to run when it wakes one. When it does not resolve,
`fleet start` fails that node rather than starting it with whatever the
daemon already happens to have configured. This is a breaking change: every
fleet file with a `kind: daemon` node needs a `file` field, a matching alias,
or a matching subdirectory added, or `fleet start` fails for that node. A
`kind: cloud` node's `start` is unaffected either way.

## Deploying cloud nodes

`fleet deploy` creates the AWS environment for one or more `kind: cloud`
nodes — the step that otherwise has to happen outside the fleet file
entirely, one `spinloop cloud deploy --env <name>` at a time, run from the
directory holding each node's Spinloop:

```sh
spinloop fleet deploy qwen           # one node
spinloop fleet deploy qwen llama     # several
spinloop fleet deploy --all          # every kind: cloud node in the file
```

Each node deploys from its own resolved [Spinloop
source](../fleet-file.md#a-nodes-spinloop-source), reusing the exact derivation, consent, and
registration `spinloop cloud deploy` uses for the same file — the two can
never disagree about what a given Spinloop deploys — and the environment each
node creates is named after the node itself. A `kind: daemon` node
named explicitly fails the command, explaining that `deploy` provisions cloud
environments and that node is not one; `--all` only ever selects `kind:
cloud` nodes, so a daemon node is never swept in by it. As with
`start`/`stop`, no node and no `--all` lists the fleet's `kind: cloud` nodes
and deploys nothing, `--all` plus node names is refused as ambiguous, and
several targeted nodes deploy independently — one node's guard or failure is
reported against it alone.

```sh
spinloop fleet deploy --all --dry-run     # print every plan, deploy nothing
spinloop fleet deploy qwen --overwrite    # redeploy over a registered environment
```

`--dry-run`, `--overwrite`, `--reseed`, `--allowed-cidr`, `--region`, and
`--spinloop-version` mean exactly what they mean on [`spinloop cloud
deploy`](cloud.md), applied per node.

## Flags

| Flag | Meaning |
| ---- | ------- |
| `-f`, `--fleet <path>` | The fleet file (default `./fleet.yaml`) — `logs` takes it long-form only, since `-f` is its follow flag |
| `--all` | `start`/`stop`/`deploy`: act on every node (or every `kind: cloud` node, for `deploy`) instead of named ones |
| `--node <name>` | `route` only: report this node rather than choosing one |
| `--prefer` | `route` only: rank by `idle` or `active`, overriding the file |
| `--format` | `metrics`: `gauge` (default), `bar`, `table`, or `json`; `logs`: `text` (default) or `json` |
| `-w`, `--watch` | `metrics` only: redraw on an interval until interrupted |
| `-f`, `--follow` | `logs` only: keep printing new output until interrupted |
| `--limit` | `logs` only: lines of backlog per node (default 200) |
| `-n`, `--dry-run` | `deploy` only: print the plan for each targeted node without deploying |
| `--overwrite` | `deploy` only: proceed against an already-registered or live environment |
| `--reseed` | `deploy` only: re-fetch the weights even if already in S3 |
| `--allowed-cidr` | `deploy` only: who may reach each environment's instance |
| `--region` | `deploy` only: AWS region of the control plane |
| `--spinloop-version` | `deploy` only: spinloop release each environment installs at boot |

## See also

- [The `fleet.yaml` file](../fleet-file.md) — the format reference for the
  file this command reads
- [`spinloop up`](up.md) — the one-word start, from a fleet directory
- [`examples/fleet-local/`](https://github.com/spinloop-ai/spinloop/tree/main/examples/fleet-local)
  — a fleet of one, on your own machine
- [`examples/fleet-docker/`](https://github.com/spinloop-ai/spinloop/tree/main/examples/fleet-docker)
  — a runnable fleet
- [`spinloop daemon`](serve.md) — what runs on each node
- [HTTP Control API](../http-api.md) — the API the fleet client speaks
- [Environment variables](../env-vars.md)
