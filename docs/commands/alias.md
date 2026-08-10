# outfit alias

Register an [`Outfit` file](../outfit-file.md) under a short name. The name
then works wherever an Outfit path does — `apply`, `unapply`, `serve`,
`harness` — from any directory.

```sh
outfit alias                 # register ./Outfit under its own ALIAS
outfit alias path/to/dir     # ...or the Outfit in another directory
outfit alias -n big .        # ...under a name of your choosing
outfit alias --list          # what is registered, and whether it still exists
```

Then, from anywhere:

```sh
outfit apply big
outfit serve big
outfit harness big -- --agent-arg
```

## Naming one for the whole shell

`OUTFIT_ALIAS` holds a registered name, and any command given no Outfit uses it:

```sh
export OUTFIT_ALIAS=big
outfit apply          # the same as `outfit apply big`
outfit serve
outfit remote status
```

The order is the argument you typed, then `OUTFIT_ALIAS`, then `./Outfit`. Two
details worth knowing:

- It decides **which** Outfit is the default, never **whether** one is applied.
  A bare `outfit harness` still launches without dressing the agent; `outfit
  harness -O` asks for the default Outfit and gets the variable's. And `outfit
  alias` ignores it entirely — with no argument that command means "the Outfit
  in this directory".
- It holds a registry name, never a path, and a file of the same name in the
  working directory does **not** shadow it — the opposite of the rule for an
  argument, below. An argument is usually a path; a variable can only have been
  set to name an alias, so it works the same in every directory.

A value that is not registered, or whose Outfit has gone, fails naming
`OUTFIT_ALIAS`, so a stale `export` in a shell profile is obvious from the
error.

## Flags

| Flag | Meaning |
| ---- | ------- |
| `-n`, `--name` | Register under this name instead of the Outfit's `ALIAS` |
| `-F`, `--force` | Re-point a name that is already registered |
| `-l`, `--list` | List the registered names and where they point |

## Two senses of "alias"

They meet here, and they are not the same thing. The `ALIAS` **instruction**
inside an Outfit names the *model* — it is the key your agent shows, and
`llama-server --alias` under [`serve`](serve.md). A registered **alias** names
the *Outfit file* to `outfit`. Registration defaults the second to the first
because you have usually already written a good name; `--name`/`-n` separates
them, and is required when an Outfit states no `ALIAS` at all.

## Notes

- A real path always beats a registered name, so registering one cannot change
  a command that already worked. When both exist, `outfit` says so and uses the
  path.
- Names are stored, with absolute paths, in `outfit`'s own config
  (`${XDG_CONFIG_HOME:-~/.config}/outfit/config.json`) — they are yours and
  this machine's, never part of an `Outfit`, so a committed `Outfit` stays
  portable.
- Registering parses the Outfit, so a broken file is caught now rather than
  days later.
- Tab completion knows your aliases — see
  [`outfit completion`](completion.md).

## See also

- [`outfit unalias`](unalias.md) — drop a name
- [`outfit show`](show.md) — lists your aliases alongside the configured state
