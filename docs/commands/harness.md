# outfit harness

Launch your coding agent — the **harness** — and manage which one is active.
opencode is the default; Pi is also supported. The harness is chosen at
runtime, never baked into an `Outfit` file, so the same selection works for
either.

```sh
outfit harness             # launch the active harness (forwards trailing args)
outfit harness -H pi       # launch a specific harness, ignoring the default
outfit harness --set pi    # make Pi the default for future commands
outfit harness --get       # show the current default
```

## Which harness wins

Every `outfit` command resolves the harness the same way:

1. `--harness`/`-H` flag
2. `OUTFIT_HARNESS` environment variable
3. Your stored default (`outfit harness --set`)
4. opencode

## Dress, then launch

`--outfit`/`-O` applies an [`Outfit`](../outfit-file.md) on the way in — the
same work [`outfit apply`](apply.md) does — so one command dresses the agent
and launches it:

```sh
outfit harness -O                                  # apply ./Outfit, then launch
outfit harness --outfit=path/to/Outfit             # ...or a specific one
outfit harness --outfit=path/to/dir                # ...or a directory holding an Outfit
outfit harness --outfit=https://example.com/Outfit # ...or a URL, fetched instead of read
```

Given bare, `--outfit` defaults to `./Outfit` like `apply` does; when you name
a path, attach it to the flag, because anything positional is forwarded to the
agent (`outfit harness -O run --model x` passes `run --model x` on). The one
exception is a *leading* argument that names an Outfit — a path, a directory
holding one, or a [registered alias](alias.md) — which is applied rather than
forwarded:

```sh
outfit harness qwen3.6-27b                 # apply the aliased Outfit, launch
outfit harness qwen3.6-27b -- --agent-arg  # ...forwarding --agent-arg
outfit harness -- qwen3.6-27b              # leading -- opts out: forward it
```

`OUTFIT_ALIAS` decides what "the default Outfit" means, so `outfit harness -O`
applies the alias it names. A bare `outfit harness` still applies nothing: the
variable chooses which Outfit, never whether you are dressed. See
[`outfit alias`](alias.md#naming-one-for-the-whole-shell).

## Flags

| Flag | Meaning |
| ---- | ------- |
| `-H`, `--harness` | Which harness to launch (or set `OUTFIT_HARNESS`) |
| `-O`, `--outfit` | Apply this Outfit before launching (bare: `./Outfit`) |
| `--set` | Store the default harness and exit |
| `--get` | Print the active harness instead of launching |
| `--providers` | Path to a custom catalogue, for the applied Outfit |
| `--fleet` | Route through this fleet file (overrides the Outfit's `FLEET`) |
| `--node` | Pin the launch to one fleet node |
| `--prefer` | Rank fleet nodes by `idle` or `active` (overrides the fleet file) |
| `--no-wake` | Fail rather than starting an engine on an idle fleet node |
| `--wake-timeout` | How long to wait for a woken node's engine (default 5m) |

## Launching against your fleet

An Outfit with a [`FLEET`](../outfit-file.md#running-the-model-on-another-machine-you-own)
instruction sends the agent to a machine on your network instead of a local
engine:

```sh
outfit harness my-outfit          # picks a node, launches the agent at it
outfit harness --node gpu-box my-outfit
outfit harness --prefer active my-outfit
```

outfit queries the fleet, prefers a node already serving the Outfit's model,
and points the launched agent at that node's engine — the same injection that
carries a [`REMOTE`](remote.md) endpoint's address and key, with a selection
step in front. It reports which node it chose, and why, before the agent
starts.

When nothing is serving that model, outfit picks a node that is not running,
tells it what to serve, starts it, and waits for its engine to answer. A node
that is already running is never stopped to make room — someone else may be
using it — so a fleet with every machine busy on other models fails rather than
displacing anyone. `--no-wake` turns starting off entirely.

Which node wins among several that could all serve you is a
[`prefer` setting](fleet.md#spreading-or-consolidating): `idle` (the default)
takes the machine that has been quiet longest, keeping a second agent off an
engine that is mid-request; `active` consolidates onto the busy one instead.

## Notes

- Trailing arguments and stdio go to the agent untouched, and its exit code is
  yours.
- Not every provider maps to every harness — [`outfit list`](list.md) shows
  which harnesses each supports.

## See also

- [`outfit show`](show.md) — what the active harness has configured
- [`outfit apply`](apply.md) — dress without launching
- [`outfit fleet route`](fleet.md#which-node-would-i-get) — which node a launch would pick
- [`examples/fleet-local/`](../../examples/fleet-local/) — routing at a single local node, end to end
