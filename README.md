<p align="center">
  <img src="assets/logo.png" alt="outfit" width="520">
</p>

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
brew install lucinate-ai/tap/outfit
```

To upgrade later, run `brew upgrade outfit`.

### From source

```sh
go build -o outfit .
```

Drop the resulting `outfit` binary anywhere on your `PATH`.

## Quickstart

See what's on the menu:

```sh
outfit list
```

Add a provider and a model family:

```sh
# OpenRouter needs a key — put it in .env first:
echo 'DEEPSEEK_API_KEY=sk-or-v1-...' > .env

outfit add --provider openrouter --model-family deepseek-v4
```

Then just run `opencode`.

## Usage

```sh
outfit list
outfit add    --provider <name> [--model-family <family>] [--model <id>] [--context <size>] [--base-url <url>]
outfit remove --provider <name> [--model-family <family>] [--model <id>]
outfit apply  [path]                 # apply an Outfit file (default ./Outfit)
outfit export [--provider <name>]    # print the current config as an Outfit
```

Short flags: `-p` (provider), `-f` (model-family), `-m` (model), `-c` (context), `-b` (base-url).

### Examples

```sh
# A local Ollama model (no key required)
outfit add -p ollama -f llama

# Claude on AWS Bedrock (uses your AWS credentials)
outfit add -p amazon-bedrock -f claude

# Any OpenAI-compatible endpoint, base URL via flag
OPENAI_API_KEY=sk-... \
  outfit add -p openai-compatible -m my-model --base-url https://my-endpoint/v1

# Pin a specific default model
outfit add -p openrouter -f deepseek-v4 -m deepseek/deepseek-v4-pro

# Set the context window — human suffixes or an absolute count, both fine
outfit add -p llamacpp -m my-model -c 128k
outfit add -p llamacpp -m my-model --context 200000

# Take a provider back out
outfit remove -p ollama

# Or just drop one family's models
outfit remove -p openrouter -f deepseek-v4
```

`add` sets the chosen model as opencode's default. `remove` clears the default
if it pointed at something you removed.

`--context`/`-c` records each added model's context window. Parsing is
forgiving: `128k`, `1m`, `1.5m`, `200000`, `128,000`, even `128 K tokens` all
land where you'd expect (`k`/`m`/`g` are decimal — `128k` is 128,000 tokens).

## Outfit files

Prefer to keep a provider selection in a file — like a `Dockerfile`, but for
opencode? Drop an **Outfit** in your project:

```dockerfile
# Outfit
PROVIDER openrouter
FAMILY   deepseek-v4
MODEL    deepseek/deepseek-v4-pro   # optional; becomes the default
CONTEXT  128k                       # optional; context window
BASEURL  https://gateway/v1         # optional; API base URL override
```

```sh
outfit apply              # reads ./Outfit and applies it
outfit apply path/to/Outfit
outfit export > Outfit    # capture your current setup as an Outfit
```

An Outfit describes one provider selection and applies exactly like the
equivalent `add`. Full syntax is in [`docs/outfit-file.md`](docs/outfit-file.md),
and ready-to-use examples live under [`examples/`](examples/).

## Keys and endpoints

Each provider declares which environment variable holds its key (`outfit
list` shows them). Values are looked up in `.env` next to the tool first, then
your shell environment. Local providers like Ollama and llama.cpp need no key;
Bedrock authenticates through your AWS credentials.

Base URLs default to the usual local ports. Override the endpoint for **any**
provider with `--base-url`/`-b` or the `OUTFIT_BASE_URL` env var — handy for
proxies, gateways, or a server on a non-default host:

```sh
outfit add -p openai-compatible -m my-model --base-url https://gateway/v1
OUTFIT_BASE_URL=https://gateway/v1 outfit add -p openai-compatible -m my-model
```

The flag wins over the env var, and either wins over the catalogue's defaults
and the per-provider variables (`OLLAMA_BASE_URL`, `LLAMACPP_BASE_URL`,
`OPENAI_BASE_URL`).

## Guides

Provider- and model-specific walkthroughs live in [`examples/`](examples/), each
with a ready-to-apply `Outfit`:

- [Qwen3.6-35B-A3B on llama.cpp](examples/llamacpp/qwen3.6/README.md)
- [Gemma-4-12B-IT on llama.cpp](examples/llamacpp/gemma4/README.md)

## Adding providers and models

Everything `outfit` knows lives in `providers.yaml`. Add a provider, a model
family, or a new model there and rebuild — no Go required. The file is commented
with the schema.

Don't want to rebuild? Point `outfit` at your own catalogue at runtime — the
flag wins, then the env var, then the built-in default:

```sh
outfit list --providers ./my-providers.yaml
OUTFIT_PROVIDERS=./my-providers.yaml outfit list
```

The `.env` file and the built binary are git-ignored.
