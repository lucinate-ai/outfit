# The `Spinloop` file

An **Spinloop** is a small, declarative file that captures one provider
selection — which provider, and which model — so you can
apply it with a single command instead of remembering flags. Think of it like a
`Dockerfile`, but for pointing your coding agent at a model.

```dockerfile
# Spinloop — point your coding agent at one provider
PROVIDER openrouter
MODEL    deepseek/deepseek-v4-pro   # the provider-native model ref
ALIAS    deepseek                   # optional; friendly name for the model
CONTEXT  128k                       # optional; context window (per request)
OUTPUT   32k                        # optional; max output tokens
PARALLEL 2                          # optional; concurrent request slots for `serve`
BASEURL  https://gateway/v1         # optional; API base URL override
PRESET   ./preset.ini               # optional; engine preset for `spinloop serve`
```

Applying it is the same as running the equivalent
[`spinloop harness add`](commands/harness.md#spinloop-harness-add), so everything you already have in your coding
agent's config is preserved.

The **harness** (opencode or Pi) is deliberately *not* part of a Spinloop — so
the same file applies to either. Choose the harness when you apply it, with
`--harness`/`-H`, the `SPINLOOP_HARNESS` env var, or a stored default
(`spinloop harness config --set`).

## Using a Spinloop

One file, several commands:

- [`spinloop harness apply`](commands/harness.md#spinloop-harness-apply) — apply the selection to your agent
- [`spinloop harness unapply`](commands/harness.md#spinloop-harness-unapply) — take it back out
- [`spinloop harness open -O`](commands/harness.md#spinloop-harness-open) — apply it, then launch the agent
- [`spinloop serve`](commands/serve.md) — run `llama-server` for the model it
  names
- [`spinloop up`](commands/up.md) — the one-word start: its server, or the
  fleet's nodes where a `fleet.yaml` is beside it
- [`spinloop alias`](commands/alias.md) — register it under a short name
- [`spinloop harness export`](commands/harness.md#spinloop-harness-export) — write one from your current setup

Every command that takes a Spinloop path accepts a directory that holds one and
takes a [registered alias](commands/alias.md) in place of a path. Given no path
at all, it uses the alias `SPINLOOP_ALIAS` names, and failing that `./Spinloop` in
the current directory.

## Fetching a Spinloop from a URL

A Spinloop path can also be an `http://` or `https://` URL, fetched instead of
read from local disk:

```sh
spinloop harness apply https://example.com/team/Spinloop
```

A URL ending in `/` is treated like a directory — `Spinloop` is appended, so
`spinloop harness apply https://example.com/team/` fetches
`https://example.com/team/Spinloop`. [`spinloop alias`](commands/alias.md) can
register a URL too, so a team can hand out a short name for a published Spinloop
instead of a link:

```sh
spinloop alias -n team-default https://example.com/team/Spinloop
spinloop harness apply team-default
```

A relative `PRESET` in a URL-sourced Spinloop resolves against that
URL rather than a local directory — see [Syntax](#syntax) below — and is
fetched only when the command that actually needs it runs, never merely
because the Spinloop itself was read.

## Running the model on a cloud GPU

For a model too big for your machine, `spinloop cloud` runs it on a
scale-to-zero GPU endpoint — one that runs only while you're using it. The
Spinloop says what the endpoint serves:

```dockerfile
# Spinloop — what the environment serves
PROVIDER llamacpp        # the engine to run there, as it would run here
ALIAS    qwen3.6-27b
CONTEXT  131072
PRESET   ./preset.ini
```

The Spinloop says *what*; it no longer says *where*. Where is an
**environment** — a deployed, registered endpoint — and the environment is named
with a `--env <name>` flag on the commands that act on it:

```sh
spinloop cloud deploy --env qwen3.6-27b-prod   # from the directory holding the Spinloop
spinloop harness apply --env qwen3.6-27b-prod           # point opencode at it
spinloop harness open --env qwen3.6-27b-prod    # work
spinloop cloud stop --env qwen3.6-27b-prod     # done
```

Each `--env <name>` reads the environment's registered config at
`${XDG_CONFIG_HOME:-~/.config}/spinloop/clouds/<name>/cloud.json` — the file
[`spinloop cloud deploy`](commands/cloud.md) writes when it creates the
environment — so deployment state stays per-user and per-machine while the
Spinloop itself stays clean enough to commit. A command given no `--env` uses
the `default` environment, and an unregistered name fails, naming the `deploy
--env` that would create it. A name is a plain identifier: `--env ./x.json`
fails, saying so. See [`spinloop cloud`](commands/cloud.md) for the full
lifecycle.

Note the missing `BASEURL`: the endpoint's address belongs to the deployment,
which records it in the environment's `cloud.json` as `base_url`, and
[`spinloop harness apply`](commands/harness.md#spinloop-harness-apply) reads it from there. Write a `BASEURL` only
to override that.

Applying with `--env` also names the harness provider after the environment
rather than the engine: the example above is configured under `qwen3.6-27b-prod`,
with the model reading as `qwen3.6-27b-prod/qwen3.6-27b`. `PROVIDER` still
supplies the engine's settings; only the name changes, so several environments
built from the same engine each keep their own entry instead of overwriting one.
The provider's display name is qualified by the environment too — `llama.cpp
(qwen3.6-27b-prod)` rather than a bare `llama.cpp` — so a cloud environment reads
distinctly from a local engine of the same kind in a harness model picker.

A launch may not state both `--env` and a fleet (the `--fleet` flag, or the
`./fleet.yaml` in force when the Spinloop is not named): each names where the
model is served from, so spinloop fails naming both.

`spinloop harness open --env qwen3.6-27b-prod` also works with **no Spinloop at
all**: the environment already knows what it is serving, so the harness
configures itself from that — the same result as the Spinloop above, without
needing a copy of it on the machine doing the launching. See
[`spinloop harness open`](commands/harness.md#launching-with-no-spinloop-at-all).

Because `PROVIDER` names the engine, this is the same file that would run the
model locally with [`spinloop serve`](commands/serve.md) — pointed at a bigger
machine.

## Running the model on another machine you own

A [fleet file](fleet-file.md) names the machines on your network
running `spinloop daemon`, and `spinloop harness open` can pick one for you.
Which fleet file a launch routes through is a launch concern, not a Spinloop
field:

- `spinloop harness open --fleet <path>` names it explicitly.
- Without the flag, a Spinloop you did not name — the default `./Spinloop`,
  worn by a valueless `--spinloop` — takes the `fleet.yaml` in the working
  directory. A Spinloop you did name — a path, a `--spinloop` value, or the
  alias `SPINLOOP_ALIAS` names — routes only by flag.

```dockerfile
# Spinloop
PROVIDER llamacpp
MODEL    qwen3-27b
```

Launching against a fleet queries it, picks a node already serving that model,
and points the agent at that node's engine. When nothing is serving it, spinloop
starts one and waits for it to load — so the machine you sat down at needs
nothing but the fleet file. `--node <name>` pins one machine, and `--no-wake`
refuses to start anything.

A fleet file may name a [gateway](commands/gateway.md) instead of nodes: a
single endpoint that has already done the choosing. A launch through such a file
is pointed at the gateway's address — with the OpenAI-compatible `/v1` prefix
added when it carries no path — and the agent it launches authenticates with the
gateway's token, resolved the way a key is resolved elsewhere: an `ENV`
instruction, then the process environment, then the `.env` beside the Spinloop.
A variable already set wins. Set nowhere, the launch fails before it writes
anything, naming the variable the section names — `OPENAI_API_KEY` where the
section names none.

A gateway resolves the model per request, so [`spinloop fleet
harness`](commands/fleet.md#launching-the-harness) needs no Spinloop at all
when the fleet file names one: with none given, it configures a generic
OpenAI-compatible provider at the gateway's address itself, its model list
populated from the gateway's own listing, and no `MODEL`/`ALIAS` to pick.
That provider reads the way a cloud environment's does — labelled by the
gateway's own `name`, or its address when the section names none — the same
"llama.cpp (dev-2)" pattern described above.

Note the missing `BASEURL` — the address is whichever node gets chosen. Writing
one pins the address and turns routing off, and spinloop says so rather than
choosing a node and discarding it.

See [`spinloop fleet route`](commands/fleet.md#which-node-would-i-get) to check
which node you would get before launching anything.

## Syntax

One instruction per line: a keyword followed by a single value.

| Keyword    | Required?                  | Maps to        | Example                        |
| ---------- | -------------------------- | -------------- | ------------------------------ |
| `PROVIDER` | yes                              | `--provider`   | `PROVIDER openrouter`          |
| `MODEL`    | one of `MODEL`/`ALIAS`           | `--model`      | `MODEL deepseek/deepseek-v4-pro` |
| `ALIAS`    | one of `MODEL`/`ALIAS`           | `--alias`      | `ALIAS deepseek`               |
| `CONTEXT`  | no                               | `--context`    | `CONTEXT 128k`                 |
| `OUTPUT`   | no                               | `--output`     | `OUTPUT 32k`                   |
| `PARALLEL` | no                               | `spinloop serve`, `spinloop cloud deploy` | `PARALLEL 2` |
| `BASEURL`  | no                               | `--base-url`   | `BASEURL https://gateway/v1`   |
| `PRESET`   | no                               | `spinloop serve` | `PRESET ./preset.ini`          |
| `ENV`      | no (repeatable)                  | `spinloop cloud`, `spinloop harness` | `ENV AWS_PROFILE=prod` |

Rules:

- A Spinloop describes **exactly one provider**. `PROVIDER` is required and may
  appear only once; so may every other keyword, except `ENV`.
- You need **at least one** of `MODEL` or `ALIAS`. Give a `MODEL` to add a
  specific model; give an `ALIAS` to name it.
- `MODEL` is the reference the **provider itself** understands: an
  OpenRouter/Bedrock model id, an Ollama name, or — for llama.cpp — a Hugging
  Face repo (`org/model:quant`) or a path to a `.gguf`.
- `ALIAS` is the friendly name the harness shows for the model (and, under
  `serve`, the name `llama-server` reports and the preset section to run). It
  defaults to `MODEL`. For a llama.cpp server the model key is only a label, so
  an `ALIAS` keeps it readable; an `ALIAS` on its own is enough to select one.
- `CONTEXT` sets the context window for the model(s) — always the context a
  single request gets, whatever serves it. It accepts human suffixes (`128k`,
  `1m`) or an absolute count (`200000`).
- `OUTPUT` caps the max output tokens, in the same format as `CONTEXT`. Left
  out, `spinloop` records a quarter of the context. It cannot exceed the context
  window.
- `PARALLEL` sets the number of concurrent request slots for `spinloop serve`
  and `spinloop cloud deploy` — a plain integer, not a size. It has no meaning
  for a hosted provider selection, only for a served engine, so unlike
  `CONTEXT`/`OUTPUT` it has no `add`/`remove` CLI flag. Since `CONTEXT` always
  means "context per request", and llama.cpp's own `--ctx-size` is a total
  budget it divides across its `--parallel` slots, a `llamacpp` Spinloop with
  both `CONTEXT` and `PARALLEL` set gets a `--ctx-size` scaled by the slot
  count so each slot still gets what `CONTEXT` promised (`CONTEXT 128k` +
   `PARALLEL 2` → `--ctx-size 256000 --parallel 2`). `vllm`, `mtplx`, and `omlx`
   have no such coupling — `PARALLEL` becomes `--max-num-seqs`/
   `--max-active-requests`/`--max-concurrent-requests` respectively, and
   `CONTEXT` is never scaled by it. See
   [`spinloop serve`](commands/serve.md#parallelism) for the full per-engine
   mapping.
- `BASEURL` overrides the provider's API base URL — handy for a gateway or a
  llama.cpp server on a non-default port. `URL`, `BASE-URL`, and `BASE_URL` are
  accepted as aliases.
- `PRESET` points at a preset `.ini`, used only by
  [`spinloop serve`](commands/serve.md); `apply` ignores it. A relative path
  resolves against the Spinloop's own directory, or against its URL when the
  Spinloop itself was fetched from one; `PRESET` may also be an absolute URL of
  its own, fetched only when `serve` (or `spinloop cloud deploy`) builds the
  launch command — never merely because the Spinloop was read. The file is read
  in the flag vocabulary of the engine `PROVIDER` names, so a preset written
  for llama.cpp is not portable to oMLX and vice versa.
- `ENV` sets an environment variable on the machine running `spinloop` and is the
  one keyword that **may repeat**. Its value is a single `KEY=VALUE` token (no
  spaces). The `spinloop cloud` commands read it — along with a `.env` beside the
  Spinloop — before they sign their AWS calls, so credentials, region and
  `SPINLOOP_CLOUD_*` overrides can travel with the Spinloop. `spinloop harness open` reads
  it too, passing the whole `.env` and the `ENV` lines to the agent it launches.
  Precedence, highest to lowest: an `ENV` line, then a variable already set in
  your shell, then the `.env` — the same rule everywhere spinloop resolves local
  variables. `ENV` applies only on the machine running `spinloop`; it is never sent
  to a deployed instance, and on the harness path it shapes only the launched
  agent, never spinloop's own environment.
- The `REMOTE` keyword was removed: a `REMOTE` line fails, naming the line and
  the replacement — `spinloop cloud deploy --env <name>` at deploy time and
  `--env <name>` on `apply`, `unapply`, `harness`, and the `cloud`
  subcommands. Where a `REMOTE` line pointed at a path or a URL, register the
  environment's config with `spinloop cloud deploy --env <name>` instead, and
  name it from the flags.
- Keywords are **case-insensitive** — `provider`, `Provider`, and `PROVIDER` are
  all accepted — but **UPPERCASE is canonical** and is what `spinloop harness export`
  writes.
- **Comments** start with `#`, either on their own line or at the end of a line.
  Blank lines are ignored.

To see the available providers, run `spinloop provider list`. To find a `MODEL` id for one,
run `spinloop provider list --models <provider>`, which asks the provider's own endpoint
what it currently serves.

## Examples

A local model served by llama.cpp (no API key needed). `ALIAS` is the name
opencode shows; add a `MODEL` (an HF repo or `.gguf` path) or a `PRESET` if you
also want `spinloop serve` to launch it:

```dockerfile
PROVIDER llamacpp
ALIAS    qwen3.6-35b-a3b
```

A single model from OpenRouter (its key comes from your `.env` or
environment, exactly as with `spinloop harness add`):

```dockerfile
PROVIDER openrouter
MODEL    deepseek/deepseek-v4-pro
```

Any OpenAI-compatible endpoint, with a single pinned model:

```dockerfile
PROVIDER openai-compatible
MODEL    my-model
```

Ready-to-use Spinloops live under
[`examples/`](https://github.com/spinloop-ai/spinloop/tree/main/examples),
including [`examples/remote-spinloop/`](https://github.com/spinloop-ai/spinloop/tree/main/examples/remote-spinloop)
for fetching one from a URL.
