# Open your coding agent

`spinloop` configures your coding agent — the **harness** — for a provider and
model, and can launch it. Configuration is a deep merge into the agent's own
config file: other providers, your theme, even your comments stay exactly where
you left them, and applying the same selection twice changes nothing the second
time.

## Which harness

Three are supported, each read from its own config file:

| Harness | Config file spinloop merges into |
| ------- | -------------------------------- |
| opencode (the default) | `${XDG_CONFIG_HOME:-~/.config}/opencode/opencode.json` |
| Pi | `~/.pi/agent/models.json` |
| lucinate | `~/.lucinate/connections.json` |

The harness is chosen at runtime, never baked into a `Spinloop` file, so the
same selection works for any of them. Every command resolves it in this order:

1. `--harness`/`-H` flag
2. `SPINLOOP_HARNESS`
3. your stored default — `spinloop harness config --set pi`
4. opencode

`spinloop harness config` reports the active harness and where that choice came
from.

## Configure

```sh
# A hosted model: the key from a .env in the current directory, or the environment
echo 'DEEPSEEK_API_KEY=sk-or-v1-...' > .env
spinloop harness add -p openrouter -m deepseek/deepseek-v4-flash

# A local model: no key at all
spinloop harness add -p ollama -m llama3.2
```

`spinloop provider list` shows every provider, the key each needs, and which
harnesses support it. Or capture a selection you already have:

```sh
spinloop harness export > Spinloop     # the config, as a Spinloop file
```

Check what the harness now carries with `spinloop harness show`, and take
something back out with `spinloop harness remove -p <provider>`.

## Make it declarative

Flags are for trying things; a project wants a file. Drop a
[`Spinloop`](../spinloop-file.md) in it:

```dockerfile
# Spinloop
PROVIDER openrouter
MODEL    deepseek/deepseek-v4-pro
```

```sh
spinloop harness apply        # apply ./Spinloop — or a path, an alias, a URL
spinloop harness unapply      # remove what it selects
```

Register the file under a short name and the name works anywhere a path does:

```sh
spinloop alias                # registers ./Spinloop under its ALIAS
spinloop harness apply qwen3.6
```

## Launch

```sh
spinloop harness open         # launch the active harness
spinloop harness open -O      # apply ./Spinloop on the way in, then launch
spinloop code                 # the one-word form of the same launch
```

`open` and `code` forward stdio and any trailing arguments to the agent, and
the agent's exit code is yours:

```sh
spinloop code --env prod      # spinloop's own flags, in any order
spinloop code -- agent-args   # -- stops the parsing: everything after is the agent's
```

Where the model is served is a launch concern, learned one of three ways: a
Spinloop you apply, `--env` for a [registered cloud environment](cloud.md),
or a [fleet file](fleet.md) that routes the launch to a node that serves — or
is woken to serve — the wanted model.

## Keys

A key is read from a `.env` beside the `Spinloop` being applied (or in the
current directory for a command that takes no Spinloop), then your environment.
spinloop writes a *reference* to the variable into the agent's config, never
the value — a resolved secret is not written to disk by any harness. When you
launch with `spinloop harness open` or `code`, the keys that resolve are passed
to the agent; when you start the agent yourself, set the variables in your own
environment.

## Where next

- [`spinloop harness`](../commands/harness.md) — every subcommand, in full
- [`spinloop code`](../commands/code.md) — the launch, in depth
- [`spinloop alias`](../commands/alias.md) — naming selections for the whole shell
- [Serve a model locally](local-serving.md) — if the model runs on this machine
