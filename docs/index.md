# spinloop

`spinloop` points your coding agent at any model — local or hosted — with one
command. Tell it the provider you want and it configures the agent for you,
merging the settings into the config you already have instead of clobbering it.

New here? Start with **[Getting started](getting-started.md)** — install to
launched agent in a couple of minutes.

## The ideas

Four words carry the whole tool:

- **Harness** — the coding agent being configured. opencode is the default;
  Pi and lucinate are also supported. Chosen at runtime, so the same selection
  works for any of them. See [`spinloop harness`](commands/harness.md).
- **Provider** — what `spinloop` can configure, from a built-in
  catalogue: OpenRouter, AWS Bedrock, Ollama, llama.cpp, vLLM, oMLX and MTPLX
  (both Apple Silicon), or any OpenAI-compatible endpoint. See
  [`spinloop provider list`](commands/provider.md).
- **Spinloop file** — a small, declarative file (like a `Dockerfile`, but for
  your agent's model) that captures one selection so you can commit it and
  apply it anywhere — local or fetched straight from a URL. See
  [The `Spinloop` file](spinloop-file.md).
- **Alias** — a short name you register for a Spinloop file or URL, usable
  wherever a path goes. See [`spinloop alias`](commands/alias.md).

## Where next

| | |
| --- | --- |
| [Serve a model locally](guides/local-serving.md) | Run an inference engine on this machine from a `Spinloop`, and point the agent at it |
| [Open your coding agent](guides/harness.md) | Configure the agent for a provider and model, and launch it |
| [From a Hugging Face model](guides/hugging-face.md) | Turn a model page's reference into a `Spinloop` that serves it |
| [Run a daemon node](guides/daemon.md) | Keep an engine supervised over the HTTP control API, so anything can start, stop, and watch it |
| [Deploy to a cloud GPU](guides/cloud.md) | The same `Spinloop`, on a machine that stops when you do |
| [Run a fleet](guides/fleet.md) | One spinloop observing and driving every machine you run |
| [Serve the fleet as a gateway](guides/gateway.md) | The whole fleet under one OpenAI-compatible endpoint |
| [Work a backlog](guides/work-items.md) | A file of work items, worked by one-shot agents at the pace the fleet allows |
| [Command reference](commands/index.md) | A page per command |
| [Troubleshooting](troubleshooting.md) | The things that go wrong, and how to tell which |
| [FAQ](faq.md) | The questions asked most |

## Environment variables

The ones you will meet first — **[Environment variables](env-vars.md) is the
full list**, including the `SPINLOOP_CLOUD_*` overrides:

| Variable | Effect |
| -------- | ------ |
| `SPINLOOP_HARNESS` | Selects the harness (a `--harness`/`-H` flag beats it) |
| `SPINLOOP_ALIAS` | A registered [alias](commands/alias.md) to use when a command names no Spinloop (an argument beats it; it beats `./Spinloop`) |
| `SPINLOOP_API_TOKEN` | Bearer token for the daemon [control API](http-api.md) |
| `DEEPSEEK_API_KEY`, `OPENAI_API_KEY`, … | Provider API keys — `spinloop provider list` shows which each provider reads |
| `AWS_REGION` | Region for AWS Bedrock |

Keys are looked up in a `.env` file **beside the `Spinloop` being applied**
first (or in the current directory, for a command that takes no Spinloop),
then your shell environment — so a project keeps its own key next to the file
that needs it, the same way `PRESET` travels with a Spinloop. They are
**never written into the agent's config** — spinloop writes a reference the
agent resolves when it runs, and `spinloop harness open` passes the keys it can
resolve to the agent it launches. If you start the agent yourself, set the
variable in your own environment.
