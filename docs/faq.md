# FAQ

**What is spinloop?** A CLI that points your coding agent at any model — local
or hosted — with one command, merging the selection into the agent's existing
config instead of replacing it. It also runs the engines locally, supervises
them over HTTP, deploys them to a cloud GPU that stops when you do, and
serves a fleet of them under one endpoint.

**Which coding agents does it support?** opencode (the default), Pi, and
lucinate. The harness is chosen at runtime — `--harness`, `SPINLOOP_HARNESS`,
or a stored default — and never appears in a `Spinloop` file, so the same file
works for any of them.

**Which models can I point it at?** Anything a catalogue provider serves:
OpenRouter, AWS Bedrock, Google Vertex, Ollama, llama.cpp, vLLM, oMLX, MTPLX,
or any OpenAI-compatible endpoint you run yourself
(`-p openai-compatible --base-url …`). `spinloop provider list` shows the
full catalogue, the key each provider needs, and which harnesses support it.

**Does spinloop download model weights?** No. A command that describes a model
never starts a multi-gigabyte transfer as a side effect: `spinloop hf` reads
repo metadata only, and the weights are fetched by the *engine* at load time,
from its own cache.

**Does spinloop store API keys anywhere?** No. The agent's config holds a
*reference* to an environment variable, never the value, and no harness writes
a resolved secret to disk. Keys are read from a `.env` beside the `Spinloop`
being applied, then your environment.

**Where does the agent's config live?** opencode:
`${XDG_CONFIG_HOME:-~/.config}/opencode/opencode.json`. Pi:
`~/.pi/agent/models.json`. lucinate: `~/.lucinate/connections.json`.
spinloop's own config — the default harness and the alias registry — is at
`~/.config/spinloop/config.json` (respecting `XDG_CONFIG_HOME` and
`SPINLOOP_CONFIG_DIR`).

**Can two projects use different models?** That is what the `Spinloop` file
is for: one per project, applied per project. Register each under a short name
with `spinloop alias` and the names work anywhere a path does;
`SPINLOOP_ALIAS` names one for the whole shell.

**What does `spinloop cloud` cost?** It runs in your own AWS account. The
control plane and baked AMIs are one-time; a GPU instance bills only while it
is running, and an idle endpoint is stopped (no GPU billing) and later
terminated by its retention window. You can also read
[`remote/`](https://github.com/spinloop-ai/spinloop/tree/main/remote) and
deploy the control plane yourself.

**Does a `Spinloop` or `fleet.yaml` file hold secrets?** No. A fleet file that
needs a token names the *variable* holding it (`tokenEnv`), and a `Spinloop`
never names a key at all — keys are looked up beside the file. Both files are
safe to commit.

**How do I watch what an engine is doing?** On a terminal, `spinloop serve`
shows metrics and the log in a full-screen view. Under a daemon,
`GET /v1/status` and `/v1/metrics` answer over the [control
API](http-api.md), and `spinloop fleet metrics -w` or the fleet's dashboard
draws the whole fleet in place.

**Where do I report a problem?** [GitHub
issues](https://github.com/spinloop-ai/spinloop/issues). Bring the command you
ran and its output — see [Troubleshooting](troubleshooting.md) for the facts
that answer most questions.

**I want to contribute.** [Development](maintainer/development.md) covers
building, testing, and where to add a provider or another harness.
