# A fleet of one: this machine

Gemma-4-12B-IT on your own machine, but reached through
[`spinloop fleet`](../../docs/commands/fleet.md) rather than by starting
`llama-server` yourself.

```sh
spinloop daemon              # once, in this directory (or under launchd/systemd)
spinloop harness open -O     # from this directory: wears ./Spinloop, routes, launches
```

That second command finds the node, starts the engine if it isn't running,
waits for it to load, and launches your agent pointed at it. The next time you
run it the engine is already up, so it just launches.

## Why bother, for one machine?

A fleet of one sounds like ceremony, and it would be if it were only about
picking a node. What it actually removes is the engine's lifecycle from your
day. Compare with [`examples/llamacpp/gemma4`](../llamacpp/gemma4/), which is
the same model and the same preset:

| | `spinloop serve` | this |
| --- | --- | --- |
| Starting the engine | you, in a terminal you keep open | on demand, when you launch an agent |
| After a reboot | you again | `spinloop daemon` under launchd/systemd |
| Wrong model loaded | stop it, edit, start it | the launch pushes what its Spinloop says |
| Second machine later | a new setup | add a line to `fleet.yaml` |

The last row is the real argument. Everything here works unchanged when you add
a second machine — the file grows a node, and `prefer` starts deciding between
them. Nothing about the Spinloop or the command changes.

## The files

[`fleet.yaml`](fleet.yaml) is as short as a fleet file gets:

```yaml
nodes:
  - name: local
    host: 127.0.0.1
    file: ./Spinloop
```

`file` is the one line worth pausing on: it is what lets `fleet start local`
resolve what to run without a prior launch (more on that below). Everything
else is a default worth knowing about, because each becomes a decision on a
real network:

- **No token.** A daemon on loopback needs none. Any node reachable across a
  network does — the daemon refuses to listen on a non-loopback address without
  one, and takes it from `--api-token-file`, `SPINLOOP_API_TOKEN`, or
  `--api-token`.
- **No engine key.** An engine on loopback needs no gating either. On a shared
  network, name the variable holding one with `engineTokenEnv` and the client
  gates the engine with it when it starts it — the node never holds a key of
  its own.
- **No `engine:` block.** The daemon reports the port its engine serves on, and
  llama.cpp's default is what the preset uses. You need a block when the daemon
  cannot know: a container publishing the engine elsewhere, or a proxy.
- **Loopback engine is fine here.** llama.cpp binds `127.0.0.1` unless told
  otherwise, and this node *is* loopback. On a cloud node that same engine
  would be unreachable, and routing says so rather than handing you an address
  that refuses connections.

[`Spinloop`](Spinloop) is `examples/llamacpp/gemma4`'s, beside a
[`fleet.yaml`](fleet.yaml). The fleet file is what turns the launch into a
routed one: a valueless `-O` wears the default `./Spinloop`, which — being
unnamed — takes the `fleet.yaml` in the working directory. One line is
deliberately absent — there is no `BASEURL`. The address is whichever node gets
chosen; pinning one turns routing off, and spinloop says so rather than choosing
a node it would then ignore.

[`preset.ini`](preset.ini) is unchanged from the non-fleet example. It matters
more here than it looks: when routing wakes a node it sends the preset's flags
along, so the daemon starts `llama-server` with exactly the command you would
have typed. The model comes from the preset's `hf` too, which is why this Spinloop
needs no `MODEL` line.

## Running it

Start the daemon once. It takes no arguments and reads no files — it runs what
a start request tells it to, and until then it sits idle:

```sh
spinloop daemon
```

Leave it running — under `launchd` or `systemd` for real use, or just in a
terminal to try it. Then, from this directory:

```sh
spinloop status        # local: idle
spinloop fleet route         # which node a launch would pick, changing nothing
spinloop harness open -O     # wear ./Spinloop, route, wake if needed, launch
```

`fleet.yaml`'s `file: ./Spinloop` means `spinloop fleet start local` works from
cold, before any launch — it resolves the same Spinloop a routed launch would,
and pushes it. A launch still does more (waits for the engine to load, then
launches your agent against it), but starting the node no longer needs one to
have happened first.

`spinloop fleet route` before your first launch:

```
Spinloop: ./Spinloop
Fleet:  ./fleet.yaml
Prefer: idle

no node in ./fleet.yaml is serving gemma-4-12b-it:
  local            idle

A launch would wake local and wait for its engine. Nothing has been started.
```

and the launch itself:

```
Routing through ./fleet.yaml...
Waking local to serve gemma-4-12b-it...
local is up; waiting for its engine to load...
Using local at http://127.0.0.1:8080/v1 — woken to serve gemma-4-12b-it
```

The wait is the model loading — minutes for a cold 12B, then seconds forever
after, because the engine stays up between sessions.

**`-O` is not optional.** A bare `spinloop harness open` launches unconfigured: it
applies no Spinloop, so there is no model to route and no fleet is picked up.
Wear the Spinloop (`-O` for `./Spinloop`, a path, or a
[registered alias](../../docs/commands/alias.md)) and routing follows from it.

## Prerequisites

The same as [`examples/llamacpp/gemma4`](../llamacpp/gemma4/README.md) — a
recent `llama-server` with MTP support, a GPU with ~16 GB of VRAM, and the
weights it fetches on first run. Read that one for what the preset's flags do
and for the long-context and reasoning variants.

## See also

- [`examples/fleet-docker/`](../fleet-docker/) — a three-node fleet in
  containers, with a fake engine, if you want to see routing across machines
  without owning any.
- [`examples/fleet/`](../fleet/) — a commented `fleet.yaml` covering tokens,
  engine overrides and `prefer`.
