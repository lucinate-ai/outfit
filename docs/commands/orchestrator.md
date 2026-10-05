# spinloop orchestrator

Work a backlog of items against a [fleet](fleet.md) at a pace the fleet can
absorb. The orchestrator holds the backlog, reads the fleet's topology from
the fleet's [gateway](gateway.md), admits an item while the fleet's declared
[concurrency](../fleet-file.md#concurrency) limits allow, and runs each admitted item
as a one-shot agent of the [active harness](harness.md) in the item's own
workspace directory — the agent's inference going through that same gateway.

```sh
spinloop orchestrator
spinloop orchestrator --gateway http://gateway.internal:4000
spinloop orchestrator --gateway http://gateway.internal:4000 --items ./work.yaml
spinloop orchestrator --fleet ./fleet.yaml -H pi
```

It runs in the foreground, the way [`spinloop gateway`](gateway.md) does: the
signal is the only exit, and a clean interrupt stops its agents and puts their
items back in the backlog — none lost, none run twice. An item that has ended
is not run again on a restart.

While it works, it serves the [work list API](#the-work-list-api) on its own
listener, the gateway's server pattern: an address and a token, and loopback
the bind that needs neither beyond the machine.

Where no `--gateway` is given, it reads the [fleet file](fleet.md) — the one
`--fleet` names, or `./fleet.yaml` — for the gateway's address and the
section's token variable, and nothing else: the gateway is the run's only view
of the fleet, and it holds no node token and no engine key. What it holds is
one credential — the gateway's bearer token, from the environment variable
`--token-env` names, or the section's `tokenEnv` where the gateway comes from
the file and no flag is given (`OPENAI_API_KEY` by default); where the
gateway comes from the file, the value is read the way the file reads its
secrets — the environment first, then the `.env` beside it — and that token
reaches each agent as its key.

## The items file

A list of items, each with an id, the instructions its agent is given, and its
own directory:

```yaml
- id: fix-the-parser
  instructions: fix the failing tests
  dir: ./parser

- id: bump-the-deps
  instructions: run the dependency upgrade and resolve the breakages
  dir: ./
  priority: 10          # optional; higher first
  tags:                 # optional; every one must be a tag of the node it runs on
    - gpu=a100
```

- **`id`** — unique in the file; the state and the per-item log are keyed by it.
- **`instructions`** — the task the agent is given, whole.
- **`dir`** — the item's own directory: everything the orchestrator keeps
  about that item's launch lives under it (see [What it
  keeps](#what-it-keeps)). A missing directory fails that item and only that
  item; the rest of the backlog goes on. With `--create-item-dirs`, the
  orchestrator creates a missing directory instead.
- **`priority`** — an integer, higher first; items of one rank go in file
  order.
- **`tags`** — `key=value` pairs naming the [tags](../fleet-file.md#tags) of the nodes
  the item may run on. Every pair must be a tag the node carries; an item with
  no tags may run anywhere. An item nothing matches waits — the fleet may
  change — rather than failing.

The file is re-read while the orchestrator runs: a new item enters the
backlog, and an item dropped from the file keeps its recorded end.

## What it keeps

Beside the items file:

```
work.yaml            the items
work.yaml.state.json the record: one entry per item that is running or has ended
work.yaml.lock       what keeps a second orchestrator off the file
work.yaml.logs/      one log per item, its agent's output
work.yaml.aborts/    the abort markers left beside the file, while they stand
```

And inside each item's own directory (`dir` in the items file):

```
<dir>/workspace/      the harness's own working directory
<dir>/config/         under --dispatch docker: one item's scoped config, while it runs
<dir>/log             a second copy of the item's kept output, written once the agent ends
```

`workspace/` is created on every launch, under either backend, if it is not
already there; the operator's own files elsewhere in `dir` are left alone.
`config/` only appears under the docker backend, and only while the item
runs. `log` is a static copy, not a live one: `work logs`/the work list API
read the canonical copy beside the items file, which streams while the
agent runs; `<dir>/log` is written once the agent ends, so the item's own
directory carries everything about its launch on its own. `work remove`
takes both `config/` and `log` out with the item's kept output —
`workspace/` is left alone, since it can hold real output an operator
wants to keep.

The state holds each item's `running`/`done`/`failed` record — the node a
running one is on, the reason a failed one failed — and an item with no entry
is in the backlog. A run that ends in a crash leaves its in-flight items
recorded `running`; the next start for the same file records them `failed`,
naming the interruption, and does not run them again. A clean interrupt does
the re-queueing itself: its items simply lose their records.

One orchestrator per items file: a second one for the same file is refused,
naming the holder.

## The work list API

The work list — every item the items file carries, joined with the run's
record for it — is queryable and mutable over HTTP while the run works, so a
client can watch the backlog move and act on it without touching the file. The
run and the API are one pair of hands on the same state: a change the API
accepts reaches the run on its next pass, and a pass the run takes shows in the
list before it returns.

```
Working work.yaml against http://gateway.internal:4000: 3 items in the backlog
Work list on 127.0.0.1:4010

a   backlog  -  -                     -
b   running  n  2026-09-14T10:00:00Z  -
c   done     n  2026-09-14T09:00:00Z  2026-09-14T09:30:00Z
```

After the banner, the startup output is the work list itself, the run's own
view of the items at that moment — the same list
[`spinloop work list`](work.md#listing-the-work) reads from the API, in the
same form: a table on a terminal, plain tab-separated lines otherwise. An
item a prior run recorded `done`, `failed` or `running` before this restart
shows in that state, not `backlog`.

| Path | Meaning |
| ---- | ------- |
| `GET /health` | That the orchestrator is up. It touches no file and no work on purpose — it is how you tell the orchestrator down from the fleet down. |
| `GET /v1/items` | The work list: every item the file carries, in the file's order, each with its record — the item's fields, its `backlog`/`running`/`done`/`failed` state, the node a running one is on, when it started and ended, and the reason a failed one failed. An item with no record is `backlog`. |
| `GET /v1/items/{id}/log` | The item's kept agent output, as it was kept. An item that wrote none is answered as having none (`"log": null`), not as a fault. |
| `POST /v1/items` | Add an item to the file and the backlog: the file's validation on its fields, and the file stays a valid items file after the write. |
| `DELETE /v1/items/{id}` | Take an item out of the work list: the items file, its record, and its kept output, all of it. |
| `POST /v1/items/{id}/abort` | Stop a running item's agent the way a clean interrupt stops it — the polite signal, the grace, then the hard end — and put the item back in the backlog, where the run admits it again on a later pass. |
| `POST /v1/items/{id}/retry` | Put a failed item back in the backlog: its record is removed, so the run admits it again on a later pass. The items file is unchanged and the failed attempt's output stays until the new attempt writes over it. |
| Any other path or method | A `404` naming the paths the API serves. |

The mutations refuse rather than force:

- An **add** is refused a `400` where the item's fields fail the file's
  validation; a `409` where the file already carries the id — naming it — or
  the state records it ended, `done` or `failed` — naming the record.
- A **remove** is refused a `409` where the item is running — naming it and
  the abort that goes first — and a `404` where the file does not carry the id,
  naming it.
- An **abort** is refused a `409` where the item is not running — naming the
  item and its state — and a `404` where the file does not carry the id,
  naming it.
- A **retry** is refused a `409` where the item is not failed — naming the
  item and its state — and a `404` where the file does not carry the id,
  naming it.

### The API's token

Callers present the API's token as a bearer token on every request,
`/health` included — a wrong or missing one is a `401`. The token comes from
one of three places, the same rules the
[daemon's](serve.md#the-control-api-api-and-spinloop-daemon) token follows,
and giving two at once is an error rather than a silent precedence:

| Source | Notes |
| ------ | ----- |
| `--api-token-file <path>` | The file's contents, trimmed. |
| `SPINLOOP_API_TOKEN` | The environment. |
| `--api-token <value>` | The token itself — readable by every local user through `ps`, like the daemon's. |

A non-loopback listen with no token refuses to start, naming the address and
the three ways to supply one; a loopback listen (`-l`, or `--listen
127.0.0.1:4010`) needs none. The default, `:4010`, binds every interface.

The API's token is a separate credential from the
[gateway's](gateway.md#the-gateways-token): the gateway's token — the one
`--token-env` names — is what the orchestrator presents *to* the gateway, and
what each agent holds as its key; the API's token is what a caller presents *to
the orchestrator*. One machine can run the two with different values, and the
API's token never reaches a node or an agent.

## How an item runs

An admitted item is launched as the harness's non-interactive single-task
form — `opencode run` or `pi --print` — in the item's workspace directory
(`<dir>/workspace/`, see [What it keeps](#what-it-keeps)). The model it
runs against is the gateway and the chosen node: the node's model, under a
provider the orchestrator writes into the harness config for the run, with the
gateway's OpenAI-compatible address as the base URL — its address plus `/v1`
where it names none — and the gateway's token as the key. A
harness with no single-task form — lucinate — is refused at startup, naming it.

opencode's launch carries `--auto`: with no terminal for a permission prompt
to reach, one otherwise blocks the item forever, or is auto-rejected off a
terminal, silently stopping the agent from doing the item's own work. Every
one-shot launch trusts every tool the item's instructions call for — there is
no way, at dispatch time, to know which ones it will need.

The node an item takes is the fleet's own routing, read through the gateway:
an item matches a node only where every tag it names is one the node carries;
a node already running and answering is offered before a node the run would
have to start, and among a tier the fleet file's `prefer` ranks them. A
stopped node is an option only where the fleet file
[wakes](../fleet-file.md#waking).

### Where an item's agent runs: `--dispatch`

`--dispatch` chooses how an admitted item's agent actually runs, for the
whole run — not per node, not per item:

- **`bare`** (the default) — a process on the orchestrator's own host, in
  its own process group, the harness's config the host's own — exactly
  what running spinloop directly has always done.
- **`docker`** — a container from the official spinloop agent image
  (opencode, Pi and `gh`), on its own network. A gateway bound to the
  orchestrator host's own loopback (`localhost`, `127.0.0.1` — what a
  gateway run on the same machine usually is) reaches the container by
  `host.docker.internal` instead: nothing to change in the fleet file or
  `--gateway`, and no reliance on a Docker Desktop setting most operators
  do not have on for `--network host` to reach the real host on macOS or
  Windows. A gateway already on a routable address is unaffected. The
  container mounts the item's own `workspace/` and `config/` directories
  (see [What it keeps](#what-it-keeps)) — the config scoped to that one
  launch alone, never the host's own harness config, carrying just the
  provider this launch needs. `docker version` is checked once, at
  startup: an unreachable daemon stops the command before it works an
  item, naming the fix. `--dispatch-image` names the image to run,
  defaulting to `ghcr.io/spinloop-ai/agent:<spinloop's own version>` — the
  image built alongside that release.

[`harness.yaml`](#harnessyaml-environment-and-lifecycle-scripts) can also
name the backend, with `dispatch:` — useful for keeping the choice with
the rest of an item's environment rather than in a wrapper script that
adds the flag. An explicit `--dispatch` on the command line wins over it.

Stopping an item — an abort, or the run's own clean interrupt — stops
either the same way: the polite signal first, then, where the grace runs
out, the hard end. See [`images/agent/README.md`](https://github.com/spinloop-ai/spinloop/blob/main/images/agent/README.md)
for what the official image carries.

## `harness.yaml`: environment and lifecycle scripts

Beyond the fleet file and the flags, an operator can shape the environment
an item's agent runs in without building a new image: `harness.yaml`,
found beside the items file by default, or named with
`--harness-config <path>`. Where neither is present, nothing changes.

```yaml
dispatch: docker
harness: opencode
baseDir: ../work
env:
  GH_TOKEN: ghp_...
  SOME_TOOL_FLAG: "1"
startup: |
  git config --global user.email "agent@example.com"
  git config --global user.name "Agent"
shutdown: |
  echo "item finished" >> /tmp/agent-activity.log
```

- **`dispatch`** — the backend the run uses, `bare` or `docker`, the way
  `--dispatch` does. An explicit `--dispatch` wins over it; where the flag
  is not given, this is the run's choice.
- **`harness`** — the harness the run uses, the way `--harness`/`-H` does.
  An explicit `--harness` wins over it; where the flag is not given, this
  is tried before the `HARNESS` environment variable and the stored
  preference (`spinloop harness use`).
- **`baseDir`** — the directory an item's own relative `dir` (see [The
  items file](#the-items-file)) resolves against, in place of the
  directory the orchestrator command happens to be started from. An
  item's `dir` that is already absolute is unaffected. A relative
  `baseDir` resolves against `harness.yaml`'s own directory, so the file
  stays portable together with the items it describes.
- **`env`** — a map added to every launch's environment, under either
  backend. An entry naming the same variable the gateway's token is
  presented under is refused, naming it, before the command works an item.
- **`startup`** — a shell script run before the harness, in the item's
  workspace directory, under the launch's full environment. A startup
  script that exits non-zero fails the item, naming the script, before the
  harness ever runs.
- **`shutdown`** — a shell script run once the harness has ended —
  cleanly, failed, or aborted alike — provided a startup script ran at
  all. An abort's own answer waits for it, bound by the same grace the
  harness's own stop already has.

Both scripts' output joins the harness's own in the item's kept log, in
the order they ran: startup's, then the harness's, then shutdown's.

## What it does not do

- It does not start or stop anything on a node. A stopped node appears in the
  topology with the model the fleet would start it with, and an item may be
  admitted against it — the engine is started by the fleet's own machinery,
  the same as a gateway request would.
- The run holds no fleet file and no node credential: the file, where read,
  gives the gateway's address and nothing else. The topology is its whole
  view, and a gateway that stops answering ends the run, naming the gateway —
  the items are safe in the file and the state, and a restarted run picks
  them up.
- It never re-runs an ended item. `done` stands, and a `failed` one —
  including the ones a crash left in flight — stands.
- It is not the fleet's scheduler. The limits it works to are the fleet
  file's declared [concurrency](../fleet-file.md#concurrency); it measures nothing and
  estimates no load.

## Flags

| Flag | Meaning |
| ---- | ------- |
| `--gateway <address>` | The fleet's gateway; where not given, the fleet file's gateway section supplies it. The topology, and the agents' inference, both go through it |
| `-f`, `--fleet <path>` | The fleet file to find the gateway in, where `--gateway` is not given (default `./fleet.yaml`) |
| `--items <path>` | The work items file (default `./work.yaml`) |
| `--create-item-dirs` | Create an item's working directory if it does not exist (default off) |
| `--token-env <variable>` | The environment variable holding the gateway's bearer token (default `OPENAI_API_KEY`, or the fleet file's section where the gateway comes from it and no flag is given; the value, on that path, also from the `.env` beside the file) |
| `--listen <address>` | The address to serve the work list API on (default `:4010`) |
| `-l`, `--loopback` | Serve the work list API on loopback on the default port (`127.0.0.1:4010`); needs no token |
| `--api-token-file <path>` | Read the work list API's bearer token from this file |
| `--api-token <value>` | The work list API's bearer token |
| `-H`, `--harness <name>` | Which harness to run the agents with (default the resolved one) |
| `--log-level` | `debug`, `info`, `warn`, or `error` — overrides `SPINLOOP_LOG_LEVEL` (default `info`) |
| `--dispatch <backend>` | How an admitted item's agent runs: `bare` (default) or `docker` |
| `--dispatch-image <ref>` | The agent image the docker backend runs (default `ghcr.io/spinloop-ai/agent:<spinloop's version>`); only meaningful with `--dispatch docker` |
| `--harness-config <path>` | The `harness.yaml` to read (default: one beside the items file, where it exists) |

## See also

- [Work a backlog](../guides/work-items.md) — the feature this command drives, and how to work the items file
- [`spinloop gateway`](gateway.md) — the endpoint the orchestrator reads, and the one its agents call
- [`spinloop fleet`](fleet.md) — the file the fleet's tags and concurrency limits live in
- [`spinloop harness`](harness.md) — the single-task form each item runs as
