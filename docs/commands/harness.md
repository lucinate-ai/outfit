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
outfit harness -O                        # apply ./Outfit, then launch
outfit harness --outfit=path/to/Outfit   # ...or a specific one
outfit harness --outfit=path/to/dir      # ...or a directory holding an Outfit
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

## Notes

- Trailing arguments and stdio go to the agent untouched, and its exit code is
  yours.
- Not every provider maps to every harness — [`outfit list`](list.md) shows
  which harnesses each supports.

## See also

- [`outfit show`](show.md) — what the active harness has configured
- [`outfit apply`](apply.md) — dress without launching
