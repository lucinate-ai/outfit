# Working a backlog against the fleet

Give the fleet a file of work items, and `spinloop orchestrator` works them
at the pace the fleet allows: each admitted item runs as a one-shot coding
agent — its inference going through the [fleet's gateway](../commands/gateway.md) —
and its outcome is recorded beside the file. The orchestrator takes the
fleet's shape from the gateway; it reads the fleet file, where there is one,
only to find that gateway, and holds no node credentials of its own.

## What it takes

Three things, all described on their own pages:

- A [fleet file](../fleet-file.md) whose nodes carry [tags](../fleet-file.md#tags) —
  the operator's description of what work each node can take on — and which
  declares its [concurrency limits](../fleet-file.md#concurrency), how much
  work the fleet may hold in flight at once
- A [gateway](../commands/gateway.md) in front of that fleet, which is how the
  orchestrator sees the nodes and how each agent's requests reach them —
  named to it by `--gateway`, or by the fleet file's gateway section where no
  flag is given
- The gateway's token, in an environment variable — or the `.env` beside the
  fleet file, where the gateway comes from that file

## The items file

A list of items, in the order they sit in the backlog:

```yaml
- id: fix-parser
  instructions: |
    The parser rejects a trailing newline in .spin files. Fix it and add a
    regression test.
  dir: ./parser

- id: docs-refresh
  instructions: Refresh the README against the current flag set.
  dir: .
  priority: 1

- id: nightly-bench
  instructions: Run the benchmark suite and record the figures.
  dir: ./bench
  tags: [gpu=a100]
```

| Field          | What it is                                                             |
| -------------- | ---------------------------------------------------------------------- |
| `id`           | The item's name: unique in the file, and the key of its record in the state beside it |
| `instructions` | What the item's agent is told to do                                    |
| `dir`          | The directory the agent works in — created where the orchestrator runs with `--create-item-dirs` |
| `tags`         | The nodes the item can run on, named as `key=value`. An item that names none matches any node |
| `priority`     | Higher runs first. A file that states none runs in file order          |

A file the orchestrator cannot parse stops it before it works an item, naming
the fault.

## Starting the orchestrator

```sh
export OPENAI_API_KEY=the-gateway-token
spinloop orchestrator --gateway http://127.0.0.1:4100 --items ./work.yaml
```

Where the fleet file's [gateway section](../fleet-file.md#gateway) names the gateway
— and you run from the directory the file lives in — the flag stands down:

```sh
spinloop orchestrator --items ./work.yaml
```

It reads the fleet's topology, admits as many backlog items as the fleet's
limits allow and the nodes can take, and keeps working the backlog until the
file is empty of runnable work or you stop it. One orchestrator per items
file: a second one for the same file is refused while the first holds its lock.

## Adding work items

Append an item to the file, by hand: a running orchestrator re-reads the file
when it changes, and the new item enters the backlog on the next pass, ranked
by its priority. Or, against a running orchestrator, with
[`spinloop work add`](../commands/work.md) — the item's fields as flags, sent to
the [work list API](../commands/orchestrator.md#the-work-list-api) the run
serves, which applies the file's validation on them:

```sh
spinloop work add --url http://127.0.0.1:4010 --id fix-parser \
  --instructions "fix the failing tests" --dir ./parser
```

An item's `id` is its identity for life: the state beside the file keeps one
record per `id`. To change what a finished item does, change its `instructions`
and `dir` — the record is kept, so the orchestrator will not work it again.
To make it run once more, give it a new `id`.

While a run is working, the run's view of the items is the source of truth,
and `spinloop work add` is that addition over the API — an item the run adds
is worked on its next pass, like any item the file carries. The API is also
how a client watches the backlog move and acts on it: the list with each
item's state, an item's kept output, a removal of an item the operator no
longer wants, and an abort of one that is running —
[`spinloop work`](../commands/work.md) being that client from the shell.

## Managing the backlog

Beside the items file `spinloop` keeps:

- `<file>.state.json` — each item's state: `running` (with the node it runs
  on), `done`, or `failed` (with the reason). An item with no entry is still
  in the backlog.
- `<file>.logs/<id>.log` — what the item's agent wrote, kept per item.
- `<file>.lock` — marks the orchestrator working the file.

A failure is a record, not a retry: the item stays `failed` with its reason in
the state, and the orchestrator moves on to the next. Read the state and the
item's log to see what went wrong; fix the item and give it a new `id` to work
it again.

From the shell, the [`spinloop work`](../commands/work.md) family drives this
backlog through the run's [work list API](../commands/orchestrator.md#the-work-list-api):
`work list` reports every item with its state — one plain line per item, a
dash where a value is absent; `work logs <id>` (`-f` to follow) prints an
item's kept agent output; `work abort <id>` stops a running item and puts
it back in the backlog; `work retry <id>` does the same for a failed item; `work remove <id>` takes an item out of the file, its
state, and its log. `work board` draws the same list as a live kanban — a
column per state, with keys to add, abort, retry, remove, and tail an item's
log without leaving the screen. The commands name the API's address
with `--url` and present its token, and a refusal reads the way the API states it.

## Stopping and restarting

Interrupt the orchestrator (Ctrl-C) and it stops its running agents cleanly:
the items go back to the backlog, and the next run picks them up where the
file left off. An orchestrator that dies uncleanly leaves its items marked
`running`; the next run records them `failed` rather than work them twice.

## What it does not do

It never starts or stops a node — a stopped node is offered to an item only
where the fleet's [wake policy](../fleet-file.md#waking) says a request may
start it, and it is the gateway that does the starting. It does not re-run an
item that has ended, finished or failed, and its run holds no node
credentials: the gateway is its only view of the fleet, the fleet file
giving only the gateway's address.

## Where next

- [`spinloop orchestrator`](../commands/orchestrator.md) — the full command reference, and
  the [work list API](../commands/orchestrator.md#the-work-list-api) a client works the backlog through
- [`spinloop work`](../commands/work.md) — the backlog driven from the shell,
  through the run's work list API: add, list, logs, abort, retry, remove, and the
  live [`board`](../commands/work.md#watching-the-board)
- [The fleet file](../fleet-file.md) — tags, concurrency, and waking
- [The gateway](../commands/gateway.md) — the front door the orchestrator reads and routes through
