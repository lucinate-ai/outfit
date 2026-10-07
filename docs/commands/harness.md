# spinloop harness

Manage the **harness** — your coding agent — through its subcommands: six
configure which providers and models the agent uses, `open` launches it, and
`config` reports or stores which harness is the default. A bare `spinloop
harness` shows this list. opencode is the default; Pi and lucinate are also
supported. The harness is chosen at runtime, never baked into a `Spinloop`
file, so the same selection works for any of them.

```sh
spinloop harness                # show the subcommands
spinloop harness open           # launch the active harness (forwards trailing args)
spinloop harness add            # configure a provider and a model
spinloop harness remove         # take a provider, or some of its models, out
spinloop harness apply          # apply a Spinloop file
spinloop harness unapply        # remove what a Spinloop selects
spinloop harness show           # what the harness has configured
spinloop harness export         # print the config as a Spinloop file
spinloop harness config         # report or store the default harness
```

## Which harness wins

Every `spinloop` command resolves the harness the same way:

1. `--harness`/`-H` flag
2. `SPINLOOP_HARNESS` environment variable
3. Your stored default (`spinloop harness config --set`)
4. opencode

## spinloop harness open

Launch the active harness's executable, forwarding stdio and any trailing
arguments to it. The harness's exit code is yours.

`spinloop code` is a one-word shortcut for this same launch — the same flags,
Spinloop application, and forwarding. See [`spinloop code`](code.md).

### Configure, then launch

`--spinloop`/`-O` applies an [`Spinloop`](../spinloop-file.md) on the way in —
the same work [`spinloop harness apply`](#spinloop-harness-apply) does — so one
command configures the agent and launches it:

```sh
spinloop harness open -O                                  # apply ./Spinloop, then launch
spinloop harness open --spinloop=path/to/Spinloop          # ...or a specific one
spinloop harness open --spinloop=path/to/dir               # ...or a directory holding a Spinloop
spinloop harness open --spinloop=https://example.com/Spinloop # ...or a URL, fetched instead of read
```

Given bare, `--spinloop` defaults to `./Spinloop` like `apply` does; when you name
a path, attach it to the flag, because anything positional is forwarded to the
agent (`spinloop harness open -O run --model x` passes `run --model x` on). The one
exception is an argument that names a Spinloop — a path, a directory holding
one, or a [registered alias](alias.md) — which is applied rather than
forwarded. It can sit anywhere among the command's own flags (`--env`, `-H`,
`--fleet`, ...), in any order; the first argument that is neither one of those
flags nor a Spinloop name starts the agent's own arguments:

```sh
spinloop harness open qwen3.6-27b                     # apply the aliased Spinloop, launch
spinloop harness open qwen3.6-27b --env prod          # the alias and --env in either order
spinloop harness open --env prod qwen3.6-27b          # ...parse the same way
spinloop harness open qwen3.6-27b -- --agent-arg      # ...forwarding --agent-arg
spinloop harness open -- qwen3.6-27b                  # leading -- opts out: forward it
```

Put `--` before the agent's own arguments if one of them would otherwise be
mistaken for a Spinloop name.

`SPINLOOP_ALIAS` decides what "the default Spinloop" means, so `spinloop harness open -O`
applies the alias it names. A bare `spinloop harness open` still applies nothing: the
variable chooses which Spinloop, never whether you are configured. See
[`spinloop alias`](alias.md#naming-one-for-the-whole-shell).

### Launching with no Spinloop at all

`--env <name>` on its own — no leading alias or path, no `--spinloop`/`-O` —
configures the harness from what is actually deployed to that
[environment](cloud.md), rather than doing nothing with the flag:

```sh
spinloop harness open --env dev-3 --prompt "..."   # configured from dev-3's deployment, then launched
```

The environment's runner becomes the provider, its served model name becomes
the model, and its context size (when set) becomes the context window — the
same result a Spinloop stating the matching `PROVIDER`/`ALIAS`/`CONTEXT` with
`--env dev-3` would produce, without writing one. This is what makes the
two-machine flow work: deploy from one machine
(`spinloop cloud deploy <spinloop> --env dev-3`), then on any machine that
can reach the same environment — one that has its `cloud.json` in the
registry, however it got there — run `spinloop harness open --env dev-3` with no
Spinloop and get the same configuration, live, so a later redeploy is picked
up automatically rather than requiring anyone to re-copy anything.

Applying a Spinloop alongside `--env` (a leading alias/path, or `-O`) is
unaffected: the Spinloop's own `PROVIDER`, `ALIAS`, `MODEL` and `CONTEXT` win,
exactly as a hand-written `BASEURL` already wins over the environment's
registered address.

A bare `--env` against an environment with nothing deployed — or a control
plane too old to report what is deployed — fails before launching, naming the
environment and how to fix it: `spinloop cloud deploy <spinloop> --env
<name>` to deploy something, or `spinloop cloud bootstrap` to update the
control plane.

### Flags

| Flag | Meaning |
| ---- | ------- |
| `-H`, `--harness` | Which harness to launch (or set `SPINLOOP_HARNESS`) |
| `-O`, `--spinloop` | Apply this Spinloop before launching (bare: `./Spinloop`) |
| `-e`, `--env` | The registered [environment](cloud.md) to launch against; with no Spinloop applied, configures the harness from what is deployed there — mutually exclusive with fleet routing, since each names where the model is served from |
| `--providers` | Path to a custom catalogue, for the applied Spinloop |
| `-f`, `--fleet` | Route through this fleet file (default: `./fleet.yaml`, when the Spinloop is not named) |
| `--node` | Pin the launch to one fleet node |
| `--prefer` | Rank fleet nodes by `idle` or `active` (overrides the fleet file) |
| `--no-wake` | Fail rather than starting an engine on an idle fleet node |
| `--wake-timeout` | How long to wait for a woken node's engine (default 5m) |

### Launching against your fleet

A fleet file sends the agent to a machine on your network instead of a local
engine. Which one a launch routes through is a launch concern, not a Spinloop
field: `--fleet <path>` names it explicitly, and without the flag a Spinloop you
did not name — the default `./Spinloop`, worn by a valueless `-O` — takes the
`fleet.yaml` in the working directory. A Spinloop you did name — a path, a `-O`
value, or the alias `SPINLOOP_ALIAS` names — routes only by flag. See
[fleet files](../spinloop-file.md#running-the-model-on-another-machine-you-own).

```sh
spinloop harness open -O -f fleet.yaml     # valueless -O wears ./Spinloop; routes through fleet.yaml
spinloop harness open --node gpu-box -f fleet.yaml
spinloop harness open --prefer active -f fleet.yaml
```

spinloop queries the fleet, prefers a node already serving the Spinloop's model,
and points the launched agent at that node's engine — the same injection that
carries a [cloud environment](cloud.md)'s endpoint address and key, with a
selection step in front. It reports which node it chose, and why, before the
agent starts.

When nothing is serving that model, spinloop picks a node that is not running,
tells it what to serve, starts it, and waits for its engine to answer. A node
that is already running is never stopped to make room — someone else may be
using it — so a fleet with every machine busy on other models fails rather than
displacing anyone. `--no-wake` turns starting off entirely.

Which node wins among several that could all serve you is a
[`prefer` setting](../fleet-file.md#spreading-or-consolidating): `idle` (the default)
takes the machine that has been quiet longest, keeping a second agent off an
engine that is mid-request; `active` consolidates onto the busy one instead.

A fleet file that names a [gateway](../fleet-file.md#gateway) points the agent there,
so the address lives in the file rather than in every Spinloop — and because a
gateway resolves the model per request, a launch through one needs no Spinloop
at all: `spinloop code --fleet ./fleet.yaml` is enough.

## spinloop harness config

Report or store which harness is the default. With no flag — or `--get` — it
prints the active harness and where that choice came from; `--set <name>`
stores the default and exits.

```sh
spinloop harness config            # report the active harness
spinloop harness config --get      # ...explicitly
spinloop harness config --set pi   # store pi as the default and exit
```

| Flag | Meaning |
| ---- | ------- |
| `--get` | Print the active harness and where that choice came from (the default) |
| `--set` | Store this harness as the default and exit |
| `-H`, `--harness` | Which harness to report with `--get` |

The stored default is what [Which harness wins](#which-harness-wins) resolves to
when no flag or environment is set.

## spinloop harness add

Point your coding agent at a provider and model. Settings are deep-merged into
the agent's config — other providers, your theme, even your comments stay
exactly where you left them.

```sh
spinloop harness add --provider <name> [--model <id>] [--alias <name>]
                        [--context <size>] [--output <size>] [--base-url <url>]
```

```sh
# A model from OpenRouter (key from .env or the environment)
spinloop harness add -p openrouter -m deepseek/deepseek-v4-flash

# A local Ollama model (no key required)
spinloop harness add -p ollama -m llama3.2

# Claude on AWS Bedrock (uses your AWS credentials)
spinloop harness add -p amazon-bedrock -m anthropic.claude-3-5-sonnet

# Claude on GCP Vertex AI (uses your Google credentials; set the project)
GOOGLE_VERTEX_PROJECT=my-gcp-project \
  spinloop harness add -p google-vertex-anthropic -m claude-3-5-sonnet-v2@20241022

# Gemini on GCP Vertex AI
GOOGLE_VERTEX_PROJECT=my-gcp-project \
  spinloop harness add -p google-vertex -m gemini-2.0-flash

# Any OpenAI-compatible endpoint
OPENAI_API_KEY=sk-... \
  spinloop harness add -p openai-compatible -m my-model --base-url https://my-endpoint/v1

# Pin a specific default model
spinloop harness add -p openrouter -m deepseek/deepseek-v4-pro

# Record the context window and cap the output tokens
spinloop harness add -p llamacpp -m my-model -c 128k -o 32k
```

| Flag | Meaning |
| ---- | ------- |
| `-p`, `--provider` | Provider name — see [`spinloop provider list`](provider.md#spinloop-provider-list). Required. |
| `-m`, `--model` | The provider-native model id to add or pin as the default |
| `-a`, `--alias` | Friendly name for the model — the key your agent shows |
| `-c`, `--context` | Context window; `128k`, `1m`, `200000`, even `128 K tokens` all work |
| `-o`, `--output` | Max output tokens, same format; defaults to a quarter of the context |
| `-u`, `--base-url` | Override the provider's API base URL (or set `SPINLOOP_BASE_URL`) |
| `-H`, `--harness` | Which harness to configure (or set `SPINLOOP_HARNESS`) |
| `--providers` | Path to a custom catalogue — see [`spinloop provider init`](provider.md#spinloop-provider-init) |

Notes:

- You need at least one of `--model` or `--alias` alongside the provider.
- API keys are read from a `.env` beside the `Spinloop` — or, for
  `spinloop harness add`, which has no Spinloop, from a `.env` in the current
  directory — then your environment, and never written anywhere they'll leak. A
  provider that requires a key tells you which variable to set.
- Some cloud providers authenticate with ambient credentials instead of an API
  key: `amazon-bedrock` via your AWS credentials, and `google-vertex` /
  `google-vertex-anthropic` via Google Application Default Credentials (run
  `gcloud auth application-default login`, or set `GOOGLE_APPLICATION_CREDENTIALS`
  to a service-account key file). The Vertex providers need a project — set
  `GOOGLE_VERTEX_PROJECT` (and optionally `GOOGLE_VERTEX_LOCATION`, which
  defaults to `global`). These providers are opencode-only.
- On opencode, `add` sets the chosen model as the default. Pi has no
  default-model setting, so `add` tells you which model to pick with `/model`.
- `--output` needs `--context`, and cannot exceed it.

## spinloop harness remove

Take a provider back out of your agent's config — or just some of its models.
The inverse of [`add`](#spinloop-harness-add); everything else in the config
stays put.

```sh
spinloop harness remove --provider <name> [--model <id>] [--alias <name>]
```

```sh
# Remove a provider entirely
spinloop harness remove -p ollama

# Drop one model, keep the provider's others
spinloop harness remove -p openrouter -m deepseek/deepseek-v4-flash
```

| Flag | Meaning |
| ---- | ------- |
| `-p`, `--provider` | Provider to remove from. Required. |
| `-m`, `--model` | Remove this model |
| `-a`, `--alias` | Remove the model stored under this alias |
| `-H`, `--harness` | Which harness to configure (or set `SPINLOOP_HARNESS`) |
| `--providers` | Path to a custom catalogue |

Notes:

- With no model or alias, the whole provider goes.
- If the agent's default model pointed at something you removed, it is cleared
  too.
- Removing something that isn't there is not an error — `spinloop` just tells
  you there was nothing to remove.

## spinloop harness apply

Apply an [`Spinloop` file](../spinloop-file.md) — a declarative description of
one provider selection — exactly as if you had run the equivalent
[`spinloop harness add`](#spinloop-harness-add). Everything else in your
agent's config is preserved.

```sh
spinloop harness apply                              # reads ./Spinloop in the current directory
spinloop harness apply path/to/Spinloop               # a full path to the file
spinloop harness apply path/to/dir                  # a directory holding a Spinloop
spinloop harness apply qwen3.6-27b                  # a name registered with `spinloop alias`
spinloop harness apply https://example.com/Spinloop   # a URL, fetched instead of read from disk
```

Add `--harness pi` (or set `SPINLOOP_HARNESS`) to apply it to Pi instead of
opencode. After applying, just launch your agent — or do both at once with
[`spinloop harness open -O`](#configure-then-launch).

| Flag | Meaning |
| ---- | ------- |
| `-e`, `--env` | The registered [environment](cloud.md) the Spinloop points at: names the harness provider and, with no `BASEURL`, supplies the endpoint's address |
| `-o`, `--output` | Max output tokens — overrides the Spinloop's `OUTPUT` |
| `-H`, `--harness` | Which harness to configure (or set `SPINLOOP_HARNESS`) |
| `--providers` | Path to a custom catalogue (a Spinloop never names one) |

Notes:

- With no argument, `apply` uses the alias `SPINLOOP_ALIAS` names, and failing
  that a file named `Spinloop` in the current directory — see
  [`spinloop alias`](alias.md#naming-one-for-the-whole-shell).
- A URL ending in `/` is treated like a directory — `Spinloop` is appended. See
  [Fetching a Spinloop from a URL](../spinloop-file.md#fetching-a-spinloop-from-a-url).
- A Spinloop's `PRESET` line is for [`spinloop serve`](serve.md); `apply`
  ignores it — never fetched, even when it's a URL.
- With `--env <name>`, the Spinloop points at a registered
  [environment](cloud.md): the harness provider is keyed on the environment
  name (so several environments built from the same engine keep their own
  entries), and a Spinloop with no `BASEURL` takes the endpoint's address from
  the environment's `cloud.json` `base_url`, which its deployment writes. A
  `BASEURL` in the Spinloop wins over it. An unregistered name fails, naming
  the `spinloop cloud deploy --env <name>` that would create it. Without
  `--env`, apply reads no cloud config at all.

## spinloop harness unapply

Remove what an [`Spinloop` file](../spinloop-file.md) selects from your agent's
config — the inverse of [`apply`](#spinloop-harness-apply), just as
[`remove`](#spinloop-harness-remove) is to [`add`](#spinloop-harness-add).

```sh
spinloop harness unapply                              # reads ./Spinloop in the current directory
spinloop harness unapply path/to/Spinloop               # a full path to the file
spinloop harness unapply path/to/dir                  # a directory holding a Spinloop
spinloop harness unapply qwen3.6-27b                  # a name registered with `spinloop alias`
spinloop harness unapply https://example.com/Spinloop   # a URL, fetched instead of read from disk
```

| Flag | Meaning |
| ---- | ------- |
| `-H`, `--harness` | Which harness to configure (or set `SPINLOOP_HARNESS`) |
| `--providers` | Path to a custom catalogue |

Notes:

- It honours `--harness`/`-H` and `SPINLOOP_HARNESS` like everything else, so
  unapply from whichever harness you applied to.
- With no argument it resolves the same way `apply` does: `SPINLOOP_ALIAS`,
  then `./Spinloop` — see
  [`spinloop alias`](alias.md#naming-one-for-the-whole-shell).
- If the agent's default model pointed at something the Spinloop selected, it is
  cleared too.

## spinloop harness show

Show what a harness currently has configured: its providers, each provider's
models with their context/output limits, the default model, and your registered
aliases.

```sh
spinloop harness show                # the active harness
spinloop harness show --harness pi   # a specific one, without changing your default
```

Where [`spinloop provider list`](provider.md#spinloop-provider-list) shows the catalogue of
providers you *could* configure, `show` reports what the harness *actually has*
right now — and which config file that lives in.

| Flag | Meaning |
| ---- | ------- |
| `-H`, `--harness` | Which harness to inspect (or set `SPINLOOP_HARNESS`) |

Notes:

- The output names the active harness and where that choice came from (flag,
  environment, stored preference, or the default).
- Inspecting another harness with `--harness` never touches your stored
  default.

## spinloop harness export

Print the active harness's configuration as an [`Spinloop`
file](../spinloop-file.md), so you can save a setup you built by hand:

```sh
spinloop harness export > Spinloop
spinloop harness export --harness pi > Spinloop   # read Pi's config instead
```

By default it exports the provider behind your default model (or the only
configured provider). If you have several, choose one with `-p`:

```sh
spinloop harness export -p openrouter > Spinloop
```

| Flag | Meaning |
| ---- | ------- |
| `-p`, `--provider` | Which configured provider to export |
| `-H`, `--harness` | Which harness to read (or set `SPINLOOP_HARNESS`) |
| `--providers` | Path to a custom catalogue |

Notes:

- Export names the configured `MODEL` directly.
- It writes canonical UPPERCASE keywords, and records `CONTEXT`/`OUTPUT` only
  when the exported models agree on a value — it never guesses.
- Secrets are never exported; keys stay in your `.env` or environment.

## Notes

- `open` passes trailing arguments and stdio to the agent untouched, and its
  exit code is yours.
- Not every provider maps to every harness — [`spinloop provider
  list`](provider.md#spinloop-provider-list) shows which harnesses each supports.

## See also

- [`spinloop code`](code.md) — this launch, as a one-word top-level shortcut
- [`spinloop provider list`](provider.md#spinloop-provider-list) — what the harness could be configured with
- [`spinloop fleet route`](fleet.md#which-node-would-i-get) — which node a launch would pick
- [`examples/fleet-local/`](https://github.com/spinloop-ai/spinloop/tree/main/examples/fleet-local)
  — routing at a single local node, end to end
