# The `Outfit` file

An **Outfit** is a small, declarative file that captures one provider
selection — which provider, and which model — so you can
apply it with a single command instead of remembering flags. Think of it like a
`Dockerfile`, but for pointing your coding agent at a model.

```dockerfile
# Outfit — point your coding agent at one provider
PROVIDER openrouter
MODEL    deepseek/deepseek-v4-pro   # the provider-native model ref
ALIAS    deepseek                   # optional; friendly name for the model
CONTEXT  128k                       # optional; context window
OUTPUT   32k                        # optional; max output tokens
BASEURL  https://gateway/v1         # optional; API base URL override
PRESET   ./preset.ini               # optional; engine preset for `outfit serve`
```

Applying it is the same as running the equivalent
[`outfit add`](commands/add.md), so everything you already have in your coding
agent's config is preserved.

The **harness** (opencode or Pi) is deliberately *not* part of an Outfit — so
the same file applies to either. Choose the harness when you apply it, with
`--harness`/`-H`, the `OUTFIT_HARNESS` env var, or a stored default
(`outfit harness --set`).

## Using an Outfit

One file, several commands:

- [`outfit apply`](commands/apply.md) — apply the selection to your agent
- [`outfit unapply`](commands/unapply.md) — take it back out
- [`outfit harness -O`](commands/harness.md) — apply it, then launch the agent
- [`outfit serve`](commands/serve.md) — run `llama-server` for the model it
  names
- [`outfit alias`](commands/alias.md) — register it under a short name
- [`outfit export`](commands/export.md) — write one from your current setup

Every command that takes an Outfit path defaults to `./Outfit` in the current
directory, accepts a directory that holds one, and takes a
[registered alias](commands/alias.md) in place of a path.

## Running the model on a cloud GPU

For a model too big for your machine, `REMOTE` names the config of a
scale-to-zero GPU endpoint — one that runs only while you're using it:

```dockerfile
# Outfit
PROVIDER llamacpp        # the engine to run there, as it would run here
ALIAS    qwen3.6-27b
CONTEXT  131072
PRESET   ./preset.ini
REMOTE   ./remote.json
```

`REMOTE` takes either a path or a bare name. A path (`./remote.json`, or an
absolute one) is resolved relative to the Outfit, like `PRESET`. A bare name
(`REMOTE qwen3.6-27b-prod`) selects a named environment from the per-user
registry at `${XDG_CONFIG_HOME:-~/.config}/outfit/remotes/<name>/remote.json`,
so deployment state stays per-user and per-instance while only the name lives in
the committed Outfit. [`outfit remote`](commands/remote.md) reads whichever it
resolves to. With no path argument the commands consult `./Outfit` when it
exists and otherwise fall back to the `default` environment; an explicit path
(`outfit remote status path/to/Outfit`) requires the Outfit to carry a `REMOTE`
instruction.

Note the missing `BASEURL`: the endpoint's address belongs to the deployment,
which records it in the named file as `base_url`, and
[`outfit apply`](commands/apply.md) reads it from there. Write a `BASEURL` only
to override that.

Applying a `REMOTE` Outfit also names the harness provider after the environment
rather than the engine: the example above is configured under `qwen3.6-27b-prod`,
with the model reading as `qwen3.6-27b-prod/qwen3.6-27b`. `PROVIDER` still
supplies the engine's settings; only the name changes, so several environments
built from the same engine each keep their own entry instead of overwriting one.
The name is the bare `REMOTE` value, or the `environment` field of the file a
path-form `REMOTE` names (falling back to `PROVIDER` when that field is absent).

Because `PROVIDER` names the engine, this is the same file that would run the
model locally with [`outfit serve`](commands/serve.md) — pointed at a bigger
machine.

## Syntax

One instruction per line: a keyword followed by a single value.

| Keyword    | Required?                  | Maps to        | Example                        |
| ---------- | -------------------------- | -------------- | ------------------------------ |
| `PROVIDER` | yes                              | `--provider`   | `PROVIDER openrouter`          |
| `MODEL`    | one of `MODEL`/`ALIAS`           | `--model`      | `MODEL deepseek/deepseek-v4-pro` |
| `ALIAS`    | one of `MODEL`/`ALIAS`           | `--alias`      | `ALIAS deepseek`               |
| `CONTEXT`  | no                               | `--context`    | `CONTEXT 128k`                 |
| `OUTPUT`   | no                               | `--output`     | `OUTPUT 32k`                   |
| `BASEURL`  | no                               | `--base-url`   | `BASEURL https://gateway/v1`   |
| `PRESET`   | no                               | `outfit serve` | `PRESET ./preset.ini`          |
| `REMOTE`   | no                               | `outfit remote` | `REMOTE ./remote.json`        |
| `ENV`      | no (repeatable)                  | `outfit remote` | `ENV AWS_PROFILE=prod`        |

Rules:

- An Outfit describes **exactly one provider**. `PROVIDER` is required and may
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
- `CONTEXT` sets the context window for the model(s). It accepts human suffixes
  (`128k`, `1m`) or an absolute count (`200000`).
- `OUTPUT` caps the max output tokens, in the same format as `CONTEXT`. Left
  out, `outfit` records a quarter of the context. It cannot exceed the context
  window.
- `BASEURL` overrides the provider's API base URL — handy for a gateway or a
  llama.cpp server on a non-default port. `URL`, `BASE-URL`, and `BASE_URL` are
  accepted as aliases.
- `PRESET` points at a preset `.ini`, used only by
  [`outfit serve`](commands/serve.md); `apply` ignores it. A relative path is
  resolved against the Outfit's own directory. The file is read in the flag
  vocabulary of the engine `PROVIDER` names, so a preset written for llama.cpp
  is not portable to oMLX and vice versa.
- `ENV` sets an environment variable for the local `outfit` process and is the
  one keyword that **may repeat**. Its value is a single `KEY=VALUE` token (no
  spaces). The `outfit remote` commands read it — along with a `.env` beside the
  Outfit — before they sign their AWS calls, so credentials, region and
  `OUTFIT_REMOTE_*` overrides can travel with the Outfit. Precedence, highest to
  lowest: an `ENV` line, then a variable already set in your shell, then the
  `.env`. `ENV` applies only on the machine running `outfit`; it is never sent to
  a deployed instance.
- Keywords are **case-insensitive** — `provider`, `Provider`, and `PROVIDER` are
  all accepted — but **UPPERCASE is canonical** and is what `outfit export`
  writes.
- **Comments** start with `#`, either on their own line or at the end of a line.
  Blank lines are ignored.

To see the available providers, run `outfit list`. To find a `MODEL` id for one,
run `outfit list --models <provider>`, which asks the provider's own endpoint
what it currently serves.

## Examples

A local model served by llama.cpp (no API key needed). `ALIAS` is the name
opencode shows; add a `MODEL` (an HF repo or `.gguf` path) or a `PRESET` if you
also want `outfit serve` to launch it:

```dockerfile
PROVIDER llamacpp
ALIAS    qwen3.6-35b-a3b
```

A single model from OpenRouter (its key comes from your `.env` or
environment, exactly as with `outfit add`):

```dockerfile
PROVIDER openrouter
MODEL    deepseek/deepseek-v4-pro
```

Any OpenAI-compatible endpoint, with a single pinned model:

```dockerfile
PROVIDER openai-compatible
MODEL    my-model
```

Ready-to-use Outfits live under [`examples/`](../examples/).
