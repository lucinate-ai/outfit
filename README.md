# oc-config

Point [opencode](https://opencode.ai) at the model providers you actually use —
OpenRouter, AWS Bedrock, Ollama, llama.cpp, or any OpenAI-compatible endpoint —
with a single command. No hand-editing JSON, no clobbering the config you
already have.

## What it does

- Merges provider settings into your **global** opencode config
  (`~/.config/opencode`), keeping everything else — other providers, your theme,
  even your comments — exactly where you left it.
- Reads provider definitions from a catalogue (`providers.yaml`) baked into the
  binary, so there are no URLs or model ids to memorise.
- Pulls API keys from a local `.env` (or your environment) and writes the config
  `0600`, because secrets.

## Install

With [Homebrew](https://brew.sh):

```sh
brew install outofcoffee/tap/oc-config
```

To upgrade later, run `brew upgrade oc-config`.

### From source

```sh
go build -o oc-config .
```

Drop the resulting `oc-config` binary anywhere on your `PATH`.

## Quickstart

See what's on the menu:

```sh
oc-config list
```

Add a provider and a model family:

```sh
# OpenRouter needs a key — put it in .env first:
echo 'DEEPSEEK_API_KEY=sk-or-v1-...' > .env

oc-config add --provider openrouter --model-family deepseek-v4
```

Then just run `opencode`.

## Usage

```sh
oc-config list
oc-config add    --provider <name> [--model-family <family>] [--model <id>] [--base-url <url>]
oc-config remove --provider <name> [--model-family <family>] [--model <id>]
```

Short flags: `-p` (provider), `-f` (model-family), `-m` (model), `-b` (base-url).

### Examples

```sh
# A local Ollama model (no key required)
oc-config add -p ollama -f llama

# Claude on AWS Bedrock (uses your AWS credentials)
oc-config add -p amazon-bedrock -f claude

# Any OpenAI-compatible endpoint, base URL via flag
OPENAI_API_KEY=sk-... \
  oc-config add -p openai-compatible -m my-model --base-url https://my-endpoint/v1

# Pin a specific default model
oc-config add -p openrouter -f deepseek-v4 -m deepseek/deepseek-v4-pro

# Take a provider back out
oc-config remove -p ollama

# Or just drop one family's models
oc-config remove -p openrouter -f deepseek-v4
```

`add` sets the chosen model as opencode's default. `remove` clears the default
if it pointed at something you removed.

## Keys and endpoints

Each provider declares which environment variable holds its key (`oc-config
list` shows them). Values are looked up in `.env` next to the tool first, then
your shell environment. Local providers like Ollama and llama.cpp need no key;
Bedrock authenticates through your AWS credentials.

Base URLs default to the usual local ports. Override the endpoint for **any**
provider with `--base-url`/`-b` or the `OC_CONFIG_BASE_URL` env var — handy for
proxies, gateways, or a server on a non-default host:

```sh
oc-config add -p openai-compatible -m my-model --base-url https://gateway/v1
OC_CONFIG_BASE_URL=https://gateway/v1 oc-config add -p openai-compatible -m my-model
```

The flag wins over the env var, and either wins over the catalogue's defaults
and the per-provider variables (`OLLAMA_BASE_URL`, `LLAMACPP_BASE_URL`,
`OPENAI_BASE_URL`).

## Guides

Provider- and model-specific walkthroughs live in [`docs/`](docs/):

- [Qwen3.6-35B-A3B on llama.cpp](docs/llamacpp/qwen3.6.md)
- [Gemma-4-12B-IT on llama.cpp](docs/llamacpp/gemma4.md)

## Adding providers and models

Everything `oc-config` knows lives in `providers.yaml`. Add a provider, a model
family, or a new model there and rebuild — no Go required. The file is commented
with the schema.

Don't want to rebuild? Point `oc-config` at your own catalogue at runtime — the
flag wins, then the env var, then the built-in default:

```sh
oc-config list --providers ./my-providers.yaml   # or: -c
OC_CONFIG_PROVIDERS=./my-providers.yaml oc-config list
```

The `.env` file and the built binary are git-ignored.
