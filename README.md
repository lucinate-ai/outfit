<p align="center">
  <img src="assets/logo.png" alt="outfit" width="520">
</p>

<p align="center">
  Point your coding agent at any model — local or hosted — with one command.
</p>

<p align="center">
  <em>// no hand-editing JSON, no model ids to memorise, no clobbering the config you already have.</em>
</p>

---

```sh
# point your coding agent at a model — here, a local Qwen3.6 on Ollama
outfit add -p ollama -m qwen3.6

# prefer a file you can commit? drop an ./Outfit and apply it
outfit apply

# running that model locally too? the same file launches the server
outfit serve
```

That's the whole tool. Your agent is dressed and pointed at the model; the rest
of your config never moved.

---

Your coding agent is only as good as the model behind it, and the model you want
changes by the day — a frontier model on OpenRouter for the hard stuff, a local
Qwen on llama.cpp when you're offline or cost-conscious, Claude on Bedrock for
work. Switching between them should take a second. It usually doesn't.

Every agent keeps its config somewhere different, in a shape of its own. Pointing
one at a new provider means opening that file by hand and getting four things
exactly right: the base URL, the model id, the package it loads, and the name of
the environment variable holding your key. One stray brace and the agent won't
start. **Local models are the worst of it** — each runtime has its own ports,
model refs and quirks, and none of it is written down where you need it.

`outfit` is the wardrobe for your coding agent. Tell it the provider you want and
it dresses the agent for you:

- **One command, any model.** Pick from a built-in catalogue — OpenRouter,
  Bedrock, Ollama, llama.cpp, or any OpenAI-compatible endpoint. No URLs or model
  ids to look up.
- **Your config survives.** Settings are merged *into* what you already have.
  Other providers, your theme, even your comments stay exactly where you left them.
- **Keys stay where they belong.** Secrets are read from a local `.env` and never
  hard-coded somewhere they'll leak — written `0600`, or kept as an env reference.
- **Local models, sorted.** The same file that points your agent at a local model
  can launch the server for it. One source of truth, two jobs.

Works with [opencode](https://opencode.ai) and
[Pi](https://github.com/earendil-works/pi) today — pick the one you use per
command, or set a default. The same selection works for either.

## Install

With [Homebrew](https://brew.sh):

```sh
brew install lucinate-ai/tap/outfit
```

To upgrade later, run `brew upgrade outfit`.

### From source

```sh
go build -o outfit ./cmd/outfit
```

Drop the resulting `outfit` binary anywhere on your `PATH`.

## Quickstart

See what's in the catalogue:

```sh
outfit list
```

Add a provider and a model family:

```sh
# OpenRouter needs a key — put it in .env first:
echo 'DEEPSEEK_API_KEY=sk-or-v1-...' > .env

outfit add --provider openrouter --model-family deepseek-v4
```

Then just run `opencode`. That's it — your agent is pointed at the new model, and
the rest of your config is untouched.

### More examples

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

# Cap the max output tokens too (defaults to a quarter of the context)
outfit add -p llamacpp -m my-model -c 128k -o 32k

# Take a provider back out
outfit remove -p ollama

# Or just drop one family's models
outfit remove -p openrouter -f deepseek-v4
```

On opencode, `add` sets the chosen model as the default and `remove` clears it
if it pointed at something you removed. Pi has no default-model setting, so
`add` just registers the provider and tells you which model to pick with `/model`.

`--context`/`-c` records each added model's context window. Parsing is
forgiving: `128k`, `1m`, `1.5m`, `200000`, `128,000`, even `128 K tokens` all
land where you'd expect (`k`/`m`/`g` are decimal — `128k` is 128,000 tokens).

`--output`/`-o` caps the max output tokens, in the same format. opencode needs
one whenever a context is set, so when you leave it off `outfit` fills in a
quarter of the context for you. It can't exceed the context window.

## Usage

```sh
outfit list
outfit show   [--harness <name>]         # show what the harness has configured
outfit add    --provider <name> [--model-family <family>] [--model <id>] [--alias <name>] [--context <size>] [--output <size>] [--base-url <url>]
outfit remove --provider <name> [--model-family <family>] [--model <id>]
outfit apply  [path] [--output <size>]   # apply an Outfit file (default ./Outfit)
outfit unapply [path]                    # remove what an Outfit file selects
outfit serve  [path] [--dry-run]         # run llama-server from the Outfit's PRESET
outfit export [--provider <name>]        # print the current config as an Outfit
outfit init-providers [path]             # write the built-in catalogue out to edit
outfit harness [-H <name>] [args...]     # launch the harness (--get shows it; --set stores the default)
```

Short flags: `-p` (provider), `-f` (model-family), `-m` (model), `-a` (alias), `-c` (context), `-o` (output), `-u` (base-url), `-H` (harness).

## Harnesses

A **harness** is the coding agent being configured. opencode is the default; Pi
is also supported. The harness is chosen at runtime — never baked into an Outfit
file — so the same selection works for either.

```sh
outfit add -p ollama -f llama --harness pi   # this command only
OUTFIT_HARNESS=pi outfit add -p ollama -f llama

outfit harness --set pi    # make Pi the default for future commands
outfit harness --get       # show the current default
outfit harness             # launch the active harness (forwards trailing args)
outfit harness -H pi       # launch a specific harness, ignoring the default

outfit show                # what the active harness has configured
outfit show --harness pi   # ...for a specific harness, without changing the default
```

Where `outfit list` shows the catalogue of providers you *could* configure,
`outfit show` reports what a harness *currently has* configured — its providers,
each provider's models with their context/output limits, and the default model.
It takes the same `--harness`/`-H` override (and the same precedence) as every
other command, so you can inspect any harness without touching your stored
default.

Precedence: `--harness`/`-H` flag, then `OUTFIT_HARNESS`, then your stored
default, then opencode. Not every provider maps to every harness — `outfit list`
shows which harnesses each one supports (AWS Bedrock, for instance, is
opencode-only).

## Outfit files

Prefer to keep a provider selection in a file — like a `Dockerfile`, but for
your coding agent? Drop an **Outfit** in your project:

```dockerfile
# Outfit
PROVIDER openrouter
FAMILY   deepseek-v4
MODEL    deepseek/deepseek-v4-pro   # optional; the provider-native model ref
ALIAS    deepseek                   # optional; friendly name for the model
CONTEXT  128k                       # optional; context window
OUTPUT   32k                        # optional; max output tokens
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

## Serving a local model

Running a model with llama.cpp? `outfit serve` reads an Outfit and launches
`llama-server` for it — so the same file that points opencode at a model can
start it too. The simple case needs no preset:

```dockerfile
# Outfit
PROVIDER llamacpp
MODEL    unsloth/Qwen3.6-35B-A3B-GGUF:UD-Q4_K_XL   # HF repo, or a .gguf path
ALIAS    qwen3.6                                    # llama-server --alias
CONTEXT  32768                                      # llama-server --ctx-size
```

```sh
outfit serve              # builds a llama-server command and runs it
outfit serve --dry-run    # just print the command — no server
```

For flags an Outfit doesn't model (`-ngl`, `--jinja`, KV-cache types, draft
models), point at a llama.cpp preset `.ini` with `PRESET` and `serve` flattens
the chosen section into the command instead — with anything the Outfit states
(like `CONTEXT`) overriding the preset. It's the missing piece presets don't
cover: launching a *single* model. Details in
[`docs/outfit-file.md`](docs/outfit-file.md#serving-a-llamacpp-model).

## Keys and endpoints

Each provider declares which environment variable holds its key (`outfit
list` shows them). Values are looked up in `.env` next to the tool first, then
your shell environment. Local providers like Ollama and llama.cpp need no key;
Bedrock authenticates through your AWS credentials.

Base URLs default to the usual local ports. Override the endpoint for **any**
provider with `--base-url`/`-u` or the `OUTFIT_BASE_URL` env var — handy for
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

Everything `outfit` knows lives in `internal/catalog/providers.yaml`. Add a
provider, a model family, or a new model there and rebuild — no Go required. The
file is commented with the schema.

Don't want to rebuild? Point `outfit` at your own catalogue at runtime — the
flag wins, then the env var, then the built-in default:

```sh
outfit list --providers ./my-providers.yaml
OUTFIT_PROVIDERS=./my-providers.yaml outfit list
```

Need a starting point? `init-providers` drops the built-in catalogue into the
current directory (it won't overwrite an existing file — pass a path or
`--force` if you mean to):

```sh
outfit init-providers                 # writes ./providers.yaml
outfit list --providers providers.yaml
```

## Development

`outfit` is a Go CLI with no runtime dependencies. The domain logic is split
into `internal/` packages so each concern is isolated and independently testable;
[`AGENTS.md`](AGENTS.md) is the map of how it all fits together.

```sh
go build -o outfit ./cmd/outfit   # build the binary
go test ./...                     # run the suite
go test ./... -cover              # with coverage (kept >= 80%)
go vet ./...                      # vet
gofmt -w ./...                    # format
```

## Contributing

Issues and pull requests are welcome. A few things that make a change easy to
merge:

- Adding a provider or model? It's a data change in
  `internal/catalog/providers.yaml`, not Go — see
  [Adding providers and models](#adding-providers-and-models).
- Adding a third harness? Start at the `Harness` interface in
  `internal/harness`; [`AGENTS.md`](AGENTS.md) walks through the contract.
- Keep the suite green and formatted (`go test ./...`, `gofmt -w ./...`) before
  opening a PR.

The `.env` file and the built binary are git-ignored.

## License

[MIT](LICENSE).
