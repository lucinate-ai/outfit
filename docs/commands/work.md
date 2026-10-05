# spinloop work

Work the [orchestrator](orchestrator.md)'s work list — the backlog it works —
from the shell, as a client of the [work list API](orchestrator.md#the-work-list-api)
the orchestrator serves: add an item, read the work, read an item's kept
output, stop a running item, re-queue a failed one, remove an item — or watch the whole run on a
live board.

```sh
spinloop work add --url http://127.0.0.1:4010 --id fix-parser --instructions "fix the failing tests" --dir ./parser
spinloop work list --url http://127.0.0.1:4010
spinloop work logs --url http://127.0.0.1:4010 fix-parser -f
spinloop work abort --url http://127.0.0.1:4010 fix-parser
spinloop work retry --url http://127.0.0.1:4010 fix-parser
spinloop work remove --url http://127.0.0.1:4010 docs-refresh
spinloop work board --url http://127.0.0.1:4010
```

Each subcommand takes `--url`, the API's base address — the one the
orchestrator prints at its start — and presents the API's token as a bearer on
every call it makes. The token comes from `--api-token`, else
`--api-token-file`, else the `SPINLOOP_API_TOKEN` environment; two of the flags
at once is a refusal naming both, and a loopback API needs none.

The run's view of the items is the source of truth: the commands call the API
and report its answer. The API applies the items file's validation on an
item's fields, and it holds the file, the state, and the records the commands
would otherwise work — so a refusal reads the way the API states it. A command
that names no `--url` fails before it calls the API, naming the flag, and a
command that cannot reach the API fails naming the address — the cue to start
the run, or to fix the reach.

## Adding an item

```sh
spinloop work add --url http://127.0.0.1:4010 --id fix-parser \
  --instructions "fix the failing tests" --dir ./parser \
  --tag gpu=a100 --priority 10
```

The item's fields come from the flags and go to the API's add path, where the
API applies the items file's validation on them — the instructions and working
directory present, the tags well-formed `key=value` pairs, no key named twice —
and the file stays a valid items file after the write. An id the file already
carries is refused, naming it, and an id the state beside the file has recorded
`done` or `failed` is refused too, naming the record: an ended item is not
worked again under its own id. The API answers once the item is in the file,
and the command reports its answer: a refusal reads the way the API states it.

## Listing the work

Piped or redirected:

```sh
spinloop work list --url http://127.0.0.1:4010
# a           backlog   -       -               -
# b           running   n       2026-09-14T10:00:00Z  -
# c           done      n       2026-09-14T09:00:00Z  2026-09-14T09:30:00Z
# d           failed    -       -               2026-09-14T08:00:00Z
```

On a terminal, a heading row and the columns aligned:

```sh
spinloop work list --url http://127.0.0.1:4010
# ID  STATE    NODE  STARTED               ENDED
# a   backlog  -     -                     -
# b   running  n     2026-09-14T10:00:00Z  -
# c   done     n     2026-09-14T09:00:00Z  2026-09-14T09:30:00Z
# d   failed   -     -                     2026-09-14T08:00:00Z
```

Every item in the work list with its record, in the file's order: the id, its
state — `backlog`, `running`, `done` or `failed` — the node a running item is
on, and when it started and ended. Piped or redirected, one plain line per
item, a dash where a value is absent, so a program can split the columns; on a
terminal, the columns line up as a table instead, each as wide as its widest
value — the heading included — and the state in its colour. An item with no
record is `backlog`. The list reads the work list API the orchestrator serves
— the run's view of the items, the source of truth. `spinloop work ls` is the
same list.

## Reading an item's log

```sh
spinloop work logs --url http://127.0.0.1:4010 fix-parser
```

Prints an item's kept agent output through the API's log path. An item with
no output yet — still `backlog`, or `running` with nothing written so far —
prints as empty, not a fault. An id the run does not carry is refused,
naming it.

```sh
spinloop work logs --url http://127.0.0.1:4010 fix-parser -f
```

`-f`/`--follow` polls for new output and prints it as it arrives, the way
`tail -f` does. It keeps polling through `backlog` and `running` — run it
right after `work add` and it waits for the item to start, then streams
its output — and stops once the item is `done` or `failed`, once it drops
out of the work list (a `work remove` elsewhere), or on your own
interrupt.

## Aborting an item

```sh
spinloop work abort --url http://127.0.0.1:4010 fix-parser
```

Stops a running item through the API's abort path: the item's agent stopped the
way a clean interrupt stops it — the polite signal, the grace, then the hard
end — and its record removed. The item is back in the backlog, and the run's
next pass admits it again.

Only a running item can be aborted: an id the file does not carry is refused,
naming it, and an item the run records `backlog`, `done` or `failed` is refused
too, naming the item and its state. The API answers once the item is stopped,
and the command reports its answer: a refusal reads the way the API states it.

## Retrying a failed item

```sh
spinloop work retry --url http://127.0.0.1:4010 fix-parser
```

Puts a failed item back in the backlog through the API's retry path: its record
is removed, and the run's next pass admits it again. The item's fields in the
items file are unchanged, and its kept output from the failed attempt stays
until the new attempt writes over it.

Only a failed item can be retried: an id the file does not carry is refused,
naming it, and an item the run records `backlog`, `running` or `done` is refused
too, naming the item and its state. The API answers once the item is back in
the backlog, and the command reports its answer: a refusal reads the way the
API states it.

## Removing an item

```sh
spinloop work remove --url http://127.0.0.1:4010 docs-refresh
```

Takes an item out of the work list through the API's remove path: the item out
of the items file, its record out of the state beside it, and its kept output
from the logs beside it — the file a valid items file after the removal.

A running item cannot be removed: the refusal names it and the abort that goes
first. An id the file does not carry is refused, naming it. The API answers
once the item is out, and the command reports its answer.

## Watching the board

```sh
spinloop work board --url http://127.0.0.1:4010
```

The board: the same work list as a live kanban — four columns,
`Backlog`, `Running`, `Done`, `Failed`, a card per item — re-read from
the API on a cadence, so a card moves as the run works it. It is a
client of the API like every other command here: it reads no file and
writes no file, and every action it offers goes through the same paths
the one-shot commands call.

Each card carries the item's id, its instructions clipped to the card's
width, and its priority where it has one; a running card adds the node
it is on and how long it has been up, counting up as you watch. States
wear the colours `work list` gives them.

Keys are offered in the footer only where they would do something for
what the cursor stands on:

- `↑`/`↓`/`←`/`→` move the selection — sideways to the next column
  holding a card.
- `enter` opens the item's detail: its full instructions, dir, tags,
  timings and failure reason, with its kept output tailed beneath as
  `work logs -f` tails it, ending when the item ends or drops out.
  `esc` returns; the board cannot be quit from inside the detail.
- `a` aborts a running item, `t` retries a failed one, `x` removes one that
  is not running — the removal asks first, and declining sends nothing. A refusal from the API
  reads on the status line the way the API states it.
- `n` opens the add form — the same add `work add` sends, through the
  API's add path. Its five fields stand before you at once (id,
  instructions and dir marked required); `up`/`down` step the field
  cursor, `enter` advances, and on the last field sends. The form
  keeps the API's refusal visible for a corrected send; `esc` closes
  an empty form and, with anything typed, asks before discarding.
  The board keeps moving behind it.
- `r` reads again at once; `q` or `Ctrl+C` leaves.

When the API goes quiet the board does not go with it: it keeps
drawing its last reading and marks the title bar with its age until a
good read returns.

The board needs an interactive terminal. Piped or redirected, it
refuses and names `spinloop work list` as the command for the same
work into a pipe.

## What it does not do

- It reads no file and writes no file: the commands never touch the items file,
  the state, or the logs directly, and every mutation goes through the API the
  run owns.
- It never runs an agent, and never starts or stops one: an abort asks the run
  to stop its agent, and the run's own grace bounds the stop.
- It does not re-run a `done` item, and a `failed` one only when asked with
  `work retry`. A `done` or `failed` record stands against the id's re-add,
  the way the orchestrator's does.
- An abort is a stop, not a cancel of the work: the item goes back to the
  backlog and is worked again on a later pass. To keep it out, remove it.

## Flags

| Flag | Meaning |
| ---- | ------- |
| `--url <address>` | The work list API's base address — `add`, `list`, `logs`, `abort`, `retry`, `remove`, `board` |
| `--api-token <value>` | The work list API's bearer token — every subcommand |
| `--api-token-file <path>` | The file the work list API's bearer token stands in — every subcommand |
| `--id <id>` | The item's id — `add` |
| `--instructions <text>` | The instructions the item's agent is given — `add` |
| `--dir <path>` | The directory the agent works in — `add` |
| `--tag <key=value>` | A tag the item carries; repeatable — `add` |
| `--priority <n>` | The item's priority, higher first — `add` |
| `-f`, `--follow` | Keep printing new output as it arrives — `logs` |

Where neither token flag is given, the token comes from the
`SPINLOOP_API_TOKEN` environment, and a loopback API needs none at all.

## See also

- [Working a backlog against the fleet](../guides/work-items.md) — the feature this
  command family drives
- [`spinloop orchestrator`](orchestrator.md) — the run the commands talk to,
  and the [work list API](orchestrator.md#the-work-list-api) they call
