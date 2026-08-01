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

# launch that agent, now wearing the model you picked
outfit harness

# prefer it declarative? commit an ./Outfit file and apply it
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
  Bedrock, Ollama, llama.cpp, vLLM, oMLX, or any OpenAI-compatible endpoint. No
  URLs to look up, and `outfit list --models` fetches the model ids straight from
  the provider.
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

Need a model id? Ask the provider itself — no memorising, no guessing:

```sh
outfit list --models openrouter    # the models it currently serves, live
```

Add a provider and a model:

```sh
# OpenRouter needs a key — put it in .env first:
echo 'DEEPSEEK_API_KEY=sk-or-v1-...' > .env

outfit add --provider openrouter --model deepseek/deepseek-v4-flash
```

Then just run `opencode`. That's it — your agent is pointed at the new model, and
the rest of your config is untouched.

### More examples

```sh
# A local Ollama model (no key required)
outfit add -p ollama -m llama3.2

# Claude on AWS Bedrock (uses your AWS credentials)
outfit add -p amazon-bedrock -m anthropic.claude-3-5-sonnet

# Any OpenAI-compatible endpoint, base URL via flag
OPENAI_API_KEY=sk-... \
  outfit add -p openai-compatible -m my-model --base-url https://my-endpoint/v1

# Pin a specific default model
outfit add -p openrouter -m deepseek/deepseek-v4-pro

# Set the context window — human suffixes or an absolute count, both fine
outfit add -p llamacpp -m my-model -c 128k
outfit add -p llamacpp -m my-model --context 200000

# Cap the max output tokens too (defaults to a quarter of the context)
outfit add -p llamacpp -m my-model -c 128k -o 32k

# Take a provider back out
outfit remove -p ollama

# Or just drop one model
outfit remove -p openrouter -m deepseek/deepseek-v4-flash
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
outfit list   [--models [<provider>]]    # the catalogue; --models fetches live model ids
outfit show   [--harness <name>]         # show what the harness has configured
outfit add    --provider <name> [--model <id>] [--alias <name>] [--context <size>] [--output <size>] [--base-url <url>]
outfit remove --provider <name> [--model <id>] [--alias <name>]
outfit apply  [path] [--output <size>]   # apply an Outfit file or directory (default ./Outfit)
outfit unapply [path]                    # remove what an Outfit file selects
outfit alias  [path] [-n <name>] [-l]    # name an Outfit; -l lists them
outfit unalias <name>                    # drop a registered name
outfit serve  [path] [--dry-run]         # run llama-server from the Outfit's PRESET
outfit export [--provider <name>]        # print the current config as an Outfit
outfit init-providers [path]             # write the built-in catalogue out to edit
outfit harness [<outfit>] [-H <name>] [--outfit[=<path>]] [args...]
                                         # launch the harness (a leading Outfit or alias is
                                         #   applied first; --get shows it; --set stores it)
outfit completion <shell>                # tab completion (bash, zsh, powershell)
outfit remote <start|stop|status|deploy> [path]
                                         # control the remote GPU inference instance
                                         #   (deploy sets what it serves, from the Outfit)
```

Short flags: `-p` (provider), `-m` (model), `-a` (alias), `-c` (context), `-o` (output), `-u` (base-url), `-H` (harness), `-O` (outfit), and under `alias`: `-n` (name), `-l` (list), `-F` (force).

Anywhere a `[path]` appears above you can put a name registered with
[`outfit alias`](#aliases) instead.

## Documentation

The [`docs/`](docs/) directory is the user manual:

- [Getting started](docs/getting-started.md) — install to launched agent, end
  to end
- [The `Outfit` file](docs/outfit-file.md) — full syntax and examples
- [Command reference](docs/README.md#commands) — a page per command, under
  [`docs/commands/`](docs/commands/)

## Harnesses

A **harness** is the coding agent being configured. opencode is the default; Pi
is also supported. The harness is chosen at runtime — never baked into an `Outfit`
file — so the same selection works for either.

```sh
outfit add -p ollama -m llama3.2 --harness pi   # this command only
outfit harness --set pi    # make Pi the default for future commands
outfit harness             # launch the active harness (forwards trailing args)
outfit harness -O          # apply ./Outfit, then launch the harness
outfit show                # what the active harness has configured
```

Precedence: `--harness`/`-H` flag, then `OUTFIT_HARNESS`, then your stored
default, then opencode. Not every provider maps to every harness — `outfit list`
shows which harnesses each one supports (AWS Bedrock, for instance, is
opencode-only). The full story — launching, dressing on the way in, inspecting
any harness — is in [`docs/commands/harness.md`](docs/commands/harness.md) and
[`docs/commands/show.md`](docs/commands/show.md).

## Outfit files

Prefer to keep a provider selection in a file — like a `Dockerfile`, but for
your coding agent? Drop an `Outfit` in your project:

```dockerfile
# Outfit
PROVIDER openrouter
MODEL    deepseek/deepseek-v4-pro   # the provider-native model ref
ALIAS    deepseek                   # optional; friendly name for the model
CONTEXT  128k                       # optional; context window
OUTPUT   32k                        # optional; max output tokens
BASEURL  https://gateway/v1         # optional; API base URL override
```

```sh
outfit apply              # reads ./Outfit and applies it
outfit apply path/to/Outfit
outfit apply path/to/dir  # or a directory that holds an Outfit
outfit harness -O         # apply ./Outfit, then launch the agent wearing it
outfit export > Outfit    # capture your current setup as an Outfit
```

An `Outfit` describes one provider selection and applies exactly like the
equivalent `add`. Full syntax is in [`docs/outfit-file.md`](docs/outfit-file.md),
and ready-to-use examples live under [`examples/`](examples/).

## Aliases

Keeping a directory per model soon means typing a path per command. Name one
once with `outfit alias` and the name works wherever a path does:

```sh
$ outfit alias
Added alias "qwen3.6-27b" for /home/me/models/qwen3.6/Outfit …

$ outfit apply   qwen3.6-27b      # from anywhere, no path needed
$ outfit serve   qwen3.6-27b
$ outfit harness qwen3.6-27b -- --some-agent-arg
```

The name defaults to the `Outfit`'s own `ALIAS` (`--name`/`-n` picks another),
a path on disk always beats a registered name — so adding an alias can never
change what an already-working command does — and the registry lives in
`outfit`'s own config, never in an `Outfit`, so your files stay portable and
committable. Listing, re-pointing, and `unalias` are covered in
[`docs/commands/alias.md`](docs/commands/alias.md).

### Tab completion

```sh
source <(outfit completion bash)   # add to ~/.bashrc
source <(outfit completion zsh)    # or ~/.zshrc (needs compinit)
outfit completion powershell | Out-String | Invoke-Expression   # or $PROFILE
```

TAB then completes commands, flags, providers, harnesses, and your registered
aliases — details in
[`docs/commands/completion.md`](docs/commands/completion.md). Homebrew installs
the bash and zsh completions for you.

## Serving a local model

Running a model locally? `outfit serve` reads an `Outfit` and launches the
inference server its `PROVIDER` names — `llamacpp` runs `llama-server`, `omlx`
runs [oMLX](https://omlx.ai) on Apple Silicon — so the same file that points
opencode at a model can start it too. The simple case needs no preset:

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

For flags an `Outfit` doesn't model (`-ngl`, `--jinja`, KV-cache types, draft
models), point at a llama.cpp preset `.ini` with `PRESET` and `serve` flattens
the chosen section into the command instead — with anything the `Outfit` states
(like `CONTEXT`) overriding the preset. It's the missing piece presets don't
cover: launching a *single* model. Details in
[`docs/commands/serve.md`](docs/commands/serve.md).

## Remote inference instance

Running a model on your own cloud GPU box? [`remote/`](remote/) deploys one.
`outfit remote` drives its scale-to-zero lifecycle: the instance only exists
while you are using it, and stops itself after a period of idleness.

```sh
outfit remote start    # boot the instance, wait for the model to load,
                       # then print OPENAI_BASE_URL / OPENAI_API_KEY exports
outfit remote status   # instance state and endpoint health
outfit remote stop     # stop now instead of waiting for the idle timer
```

Configuration is found in one of two places. A project's `Outfit` file can
name it with a `REMOTE` instruction (`REMOTE remote.json`, resolved relative
to the Outfit — like `PRESET`, so the pair travel together); otherwise the
per-user `${XDG_CONFIG_HOME:-~/.config}/outfit/remote.json` is used. Either
way, paste the `OutfitRemoteConfig` output of the `remote/` deployment there:

```json
{"start_url": "https://...lambda-url...on.aws/", "stop_url": "https://...", "region": "eu-west-1"}
```

Each field can be overridden with `OUTFIT_REMOTE_START_URL`,
`OUTFIT_REMOTE_STOP_URL` and `OUTFIT_REMOTE_REGION`. Requests are SigV4-signed
with your AWS credentials (env, profile or SSO — the standard chain), which
must be allowed `lambda:InvokeFunctionUrl`. A cold `start` takes a few
minutes while the instance boots and loads the model; `--timeout` (default
15m) caps the wait.

## Keys and endpoints

Each provider declares which environment variable holds its key (`outfit
list` shows them). Values are looked up in a `.env` beside the `Outfit` first, then
your shell environment. Local providers like Ollama, llama.cpp and oMLX need no
key;
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
`OMLX_BASE_URL`, `VLLM_BASE_URL`, `OPENAI_BASE_URL`).

## Guides

Provider- and model-specific walkthroughs live in [`examples/`](examples/), each
with a ready-to-apply `Outfit`:

- [Qwen3.6-35B-A3B on llama.cpp](examples/llamacpp/qwen3.6/README.md)
- [Gemma-4-12B-IT on llama.cpp](examples/llamacpp/gemma4/README.md)
- [Qwen3.6-35B-A3B on oMLX (Apple Silicon)](examples/omlx/qwen3.6/README.md)
- [Gemma-4-E2B on oMLX (Apple Silicon)](examples/omlx/gemma-4-e2b/README.md)

## Adding providers and models

Everything `outfit` knows lives in `internal/catalog/providers.yaml`. Add a
provider there and rebuild — no Go required. The
file is commented with the schema.

Don't want to rebuild? Write the catalogue out with
[`outfit init-providers`](docs/commands/init-providers.md), edit it, and point
`outfit` at it at runtime — the flag wins, then the env var, then the built-in
default:

```sh
outfit init-providers                 # writes ./providers.yaml
outfit list --providers providers.yaml
OUTFIT_PROVIDERS=providers.yaml outfit list
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
