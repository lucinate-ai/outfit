# outfit documentation

`outfit` points your coding agent at any model — local or hosted — with one
command. Tell it the provider you want and it dresses the agent for you,
merging the settings into the config you already have instead of clobbering it.

New here? Start with **[Getting started](getting-started.md)** — install to
launched agent in a couple of minutes.

## The ideas

Four words carry the whole tool:

- **Harness** — the coding agent being configured. opencode is the default;
  Pi is also supported. Chosen at runtime, so the same selection works for
  either. See [`outfit harness`](commands/harness.md).
- **Provider and family** — what `outfit` can configure, from a built-in
  catalogue: OpenRouter, AWS Bedrock, Ollama, llama.cpp, vLLM, or any
  OpenAI-compatible endpoint, each with named model families. See
  [`outfit list`](commands/list.md).
- **Outfit file** — a small, declarative file (like a `Dockerfile`, but for
  your agent's model) that captures one selection so you can commit it and
  apply it anywhere. See [The `Outfit` file](outfit-file.md).
- **Alias** — a short name you register for an Outfit file, usable wherever a
  path goes. See [`outfit alias`](commands/alias.md).

## Guides

- [Getting started](getting-started.md) — the end-to-end flow
- [The `Outfit` file](outfit-file.md) — syntax and examples
- [Running on a cloud GPU](commands/remote.md) — the same Outfit, on a
  machine that stops when you do
- [Runnable examples](../examples/) — ready-to-apply Outfits with walkthroughs

## Commands

| Command | What it does |
| ------- | ------------ |
| [`outfit add`](commands/add.md) | Point the agent at a provider and model |
| [`outfit remove`](commands/remove.md) | Take a provider or its models back out |
| [`outfit list`](commands/list.md) | Show the catalogue of providers you could configure |
| [`outfit show`](commands/show.md) | Show what the agent currently has configured |
| [`outfit apply`](commands/apply.md) | Apply an `Outfit` file |
| [`outfit unapply`](commands/unapply.md) | Remove what an `Outfit` file selects |
| [`outfit alias`](commands/alias.md) | Name an `Outfit` so the name works anywhere a path does |
| [`outfit unalias`](commands/unalias.md) | Drop a registered name |
| [`outfit serve`](commands/serve.md) | Run `llama-server` for the model an `Outfit` names |
| [`outfit remote`](commands/remote.md) | Run the model on a cloud GPU that stops when you do |
| [`outfit export`](commands/export.md) | Capture the current setup as an `Outfit` |
| [`outfit harness`](commands/harness.md) | Launch the agent, optionally dressing it first |
| [`outfit init-providers`](commands/init-providers.md) | Write the catalogue out to customise |
| [`outfit completion`](commands/completion.md) | Tab completion for your shell |

`outfit version` prints the version, and `outfit help` the usage summary.

## Environment variables

| Variable | Effect |
| -------- | ------ |
| `OUTFIT_HARNESS` | Selects the harness (a `--harness`/`-H` flag beats it) |
| `OUTFIT_PROVIDERS` | Path to a custom provider catalogue (`--providers` beats it) |
| `OUTFIT_BASE_URL` | Overrides any provider's API base URL (`--base-url`/`-u` beats it) |
| `DEEPSEEK_API_KEY`, `OPENAI_API_KEY`, … | Provider API keys — `outfit list` shows which each provider reads |
| `OLLAMA_BASE_URL`, `LLAMACPP_BASE_URL`, `OPENAI_BASE_URL` | Per-provider endpoint overrides |
| `AWS_REGION` | Region for AWS Bedrock |

Keys are looked up in a `.env` file **beside the `Outfit` being applied** first
(or in the current directory, for a command that takes no Outfit), then your
shell environment — so a project keeps its own key next to the file that needs
it, the same way `PRESET` and `REMOTE` travel with an Outfit. They are **never written into the agent's config** — outfit writes
a reference the agent resolves when it runs, and `outfit harness` passes the
keys it can resolve to the agent it launches. If you start the agent yourself,
set the variable in your own environment. Local providers (Ollama, llama.cpp on
localhost) need no key; Bedrock uses your AWS credentials.
