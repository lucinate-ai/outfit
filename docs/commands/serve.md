# outfit serve

Run the inference server for the model an [`Outfit` file](../outfit-file.md)
names — so the same file that points your agent at a local model can also start
the server behind it.

```sh
outfit serve              # reads ./Outfit and runs its PROVIDER's server
outfit serve path/to/Outfit
outfit serve qwen3.6-27b  # a name registered with `outfit alias`
outfit serve https://example.com/Outfit   # a URL, fetched instead of read from disk
outfit serve --dry-run    # print the command without launching the server
```

With no argument, `OUTFIT_ALIAS` names the Outfit before `./Outfit` is tried —
see [`outfit alias`](alias.md#naming-one-for-the-whole-shell).

It prints the command before running it, and never touches your agent's
config — pair it with [`outfit apply`](apply.md) to point the agent at the
server.

## The engine comes from `PROVIDER`

`PROVIDER` already names the engine, so `serve` needs no keyword of its own —
the same way [`outfit remote deploy`](remote.md) picks the engine for a cloud
GPU:

| `PROVIDER` | `serve` runs |
| ---------- | ------------ |
| `llamacpp` | `llama-server` |
| `omlx` | [oMLX](https://omlx.ai) on Apple Silicon |
| `vllm` | `vllm serve` (the model as its positional argument) |

Any other provider is an error: `serve` launches a self-hosted engine, and the
rest of the catalogue names endpoints somebody else runs.

Each engine reads its `PRESET` in its **own** flag vocabulary. That matters:
llama.cpp's short aliases would rewrite another engine's keys (`m` to `--model`,
`c` to `--ctx-size`), so a preset is never portable between engines.

## llama.cpp

### Simple case — straight from the Outfit

With no `PRESET`, `serve` builds the command from the Outfit itself:

```dockerfile
PROVIDER llamacpp
MODEL    unsloth/Qwen3.6-35B-A3B-GGUF:UD-Q4_K_XL   # an HF repo, or a .gguf path
ALIAS    qwen3.6                                    # llama-server --alias
CONTEXT  32768                                      # llama-server --ctx-size
BASEURL  http://127.0.0.1:8080/v1                   # llama-server --host/--port
```

`MODEL` becomes `-hf` (a Hugging Face repo) or `-m` (anything that looks like a
path or ends in `.gguf`); `ALIAS`, `CONTEXT`, and `BASEURL` fill in the rest.

### Full control — a llama.cpp preset

For flags an Outfit doesn't model — `-ngl`, `--jinja`, KV-cache types, draft
models — point at a llama.cpp
[preset `.ini`](https://github.com/ggml-org/llama.cpp/blob/master/docs/preset.md):
a set of `llama-server` flags grouped under named `[model]` sections, with a
`[*]` section for shared defaults. Presets are built for the server's router
(multi-model) mode, so there's no clean way to launch a single model from one —
which is exactly what `serve` does.

```dockerfile
PROVIDER llamacpp
ALIAS    qwen3.6-35b-a3b   # selects the preset's [qwen3.6-35b-a3b] section
PRESET   ./preset.ini
```

`serve` flattens the `[*]` defaults and the matching section into explicit
`llama-server` flags, the section winning over the defaults. Anything the
**Outfit** also states wins over both, so you can keep a shared preset and
tweak one field per project: `CONTEXT` overrides the section's `ctx-size`,
`BASEURL` its `host`/`port`, `ALIAS` its `alias`, and `MODEL` its `hf`/`model`.
Keys map straight to flags — `ctx-size = 262144` becomes `--ctx-size 262144`,
`hf` becomes `--hf-repo`, and boolean toggles like `mmap = 1` become a bare
`--mmap`. Which section runs:

- `ALIAS` names the section.
- With no `ALIAS`, a preset holding exactly one section serves that one.
- Several sections and no `ALIAS` is an error — name one.

A relative `PRESET` path resolves against the Outfit's own directory — or
against its URL, when the Outfit itself was fetched from one — so the pair
can travel together either way. `PRESET` may also be an absolute URL of its
own, fetched only when `serve` builds the command, never merely because the
Outfit was read. See [Fetching an Outfit from a
URL](../outfit-file.md#fetching-an-outfit-from-a-url).

## oMLX

[oMLX](https://omlx.ai) serves MLX models on Apple Silicon. It differs from
llama.cpp in one way that shapes everything else: it loads a whole **model
directory** and picks the model per request, rather than being launched with one
model. So a bare Outfit is enough to start it:

```dockerfile
PROVIDER omlx
BASEURL  http://127.0.0.1:8000/v1   # omlx-cli serve --host/--port
```

- `BASEURL` sets the bind address. With none, oMLX's own defaults stand.
- `MODEL` and `ALIAS` keep their usual job of naming what the *agent* asks for;
  they are not launch flags.
- `CONTEXT` sizes the *harness's* window — oMLX has no context flag.

Everything else — the model directory, the memory guard, the SSD cache — comes
from a `PRESET` written in oMLX's own flags, or from oMLX's settings
(`~/.omlx/settings.json`, and its admin panel):

```ini
[*]
model-dir = /Users/you/models

[default]                      # selected by the Outfit's ALIAS
memory-guard            = safe
paged-ssd-cache-dir     = /Users/you/.omlx/cache
max-concurrent-requests = 16
```

Preset values are passed to the server verbatim, so `~` is not expanded — write
paths out in full.

`serve` never passes `--api-key`. It prints the command it runs, and oMLX takes
its key on the command line, so passing one would put the secret on your screen
and in the process table. If you want auth on the server, configure it in oMLX;
`outfit add`/`apply` still picks up `OPENAI_API_KEY` for the agent's own config.

Note that oMLX can require a key even on localhost (it is an admin-panel
setting). Because the `omlx` provider is `apiKeyOptional`, `outfit` only writes
the key reference when `OPENAI_API_KEY` is set **at apply time** — so if your
oMLX needs a key, set it before `add`/`apply`, not just before launching the
agent.

### Finding the binary

oMLX ships as a macOS app, so `serve` looks for `omlx-cli` on your `PATH` first
and falls back to `/Applications/oMLX.app/Contents/MacOS/omlx-cli`. If you've
only ever launched it from the menu bar, the fallback is the one that finds it.

## The control API (`--api`) and `outfit daemon`

`serve` is strictly foreground: it runs the engine in front of you until one
of you exits. Two related surfaces build on it:

- `serve --api` (`-a`) exposes the control API *beside* the foreground
  engine — status and metrics answer, start fails (the engine is already
  running), and stop terminates the engine, after which serve exits as it
  always has.
- `outfit daemon` is the long-lived agent: it supervises one engine, writes
  its output to `daemon/engine.log` under
  [outfit's config directory](../env-vars.md#config-directory-resolution),
  tracks its state
  (`idle`, `running`, `stopped`, `crashed` — a crash is reported, never
  auto-restarted), and starts **nothing** until a start request asks. Stopping
  the engine leaves the daemon answering; only a signal ends it. The daemon
  stays in the foreground itself; background it with tmux, systemd, launchd or
  similar.

The daemon is a worker: **its inputs are its flags and its API, and nothing
else**. It reads no Outfit, no preset and no `fleet.yaml`, and takes no Outfit
path — passing one is an error rather than being quietly ignored. What a node
runs is decided by the client that asks: the start request's own deploy config,
or the one stored from a previous ask. With neither, a start says so.

That is why a node and a client want different files. A client's Outfit names a
model and a fleet; a node holds nothing. See
[`examples/fleet-local/`](../../examples/fleet-local/) for the whole shape on one
machine.

The API listens on `:4242` (change with `--api-addr`; on the daemon,
`--loopback`/`-l` binds `127.0.0.1:4242` instead) and speaks JSON.
See [HTTP Control API](../http-api.md) for details, or
[`openapi.yaml`](../openapi.yaml) for the full contract:

| Endpoint | Meaning |
| -------- | ------- |
| `GET /v1/status` | Engine state, what is served, the engine log path, and how long it has been idle |
| `POST /v1/start` | Start the engine (optional deploy-config body, optionally carrying the engine's API key; 409 while one runs) |
| `POST /v1/stop` | Stop the engine (idempotent; never ends the daemon) |
| `GET /v1/metrics` | Engine token counters plus host GPU/CPU/RAM |
| `GET /v1/logs` | A slice of the engine's captured output, by offset |
| `PUT /v1/deploy-config` | Set what the *next* start serves |

Requests carry `Authorization: Bearer <token>`. The token comes from one of
three places, and giving two at once is an error rather than a silent
precedence:

| Source | Notes |
| ------ | ----- |
| `--api-token-file <path>` | The file's contents, trimmed. |
| `OUTFIT_API_TOKEN` | The environment. |
| `--api-token <value>` | The token itself. |

A non-loopback listen with no token refuses to start; a loopback one needs
none.

Which to use is a question about who else can log in to that machine. A command
line is readable by every local user through `ps`, so `--api-token` discloses
the token to anyone with a shell there; the file and environment forms do not.
On a machine only you can reach, that costs nothing.

**From a service manager, use `--api-token-file`.** A literal in a unit file or
plist is a secret in a config file *and* in the process list — the worst of
both — while `systemd`'s `EnvironmentFile=` and launchd's `EnvironmentVariables`
are the environment form if you would rather keep it there.

This is also why the *engine's* key can never be given literally (see below):
that key is set remotely by a client and persists on the node, where this token
is configured locally by whoever starts the daemon.

### Gating the engine

An engine can require its own API key, separately from the token above — one
authorises driving the node, the other authorises using its engine. **The
caller supplies it**, in the start request, and a node sources no key of its
own. The daemon writes it to a private file and points the engine at that path,
so the key never appears in the node's process list; an engine with no
key-file option is refused rather than gated with a literal argument.

Because the client sets the key, it knows the key — which is what it gives the
agent it launches. `/v1/status` reports only *that* a key is required, never
what it is, and no endpoint returns it. A supervised engine gets its own `/metrics` endpoint switched on
(llama.cpp `--metrics`), which is where the token counters come from; GPU
readings need `nvidia-smi` (no Apple GPU source yet).

Those counters are also read every 15 seconds in the background, so
`/v1/status` and `/v1/metrics` can both report `lastActiveAt` and
`idleSeconds` — how long it has been since the engine last had a request in
flight or moved a counter. Both endpoints answer from one record, so they
cannot disagree, and both keep answering after the engine stops: the point of
holding the record across a stop is that it still says when work last
happened.

## What gets logged

Both commands log what the API and the engine do: one line per API request
(method, path, status, how long it took, how many bytes came back, who asked),
and the engine's lifecycle — the start, what it resolved to serve, the stop,
and the exit.

Records are graded, which is what makes the level worth setting:

| Level | What you see |
| ----- | ------------ |
| `debug` | The above, plus the full engine command line |
| `info` (default) | Every request, plus starts, stops and clean exits |
| `warn` | Only rejected requests (401, a bad cursor), a slow shutdown escalating to a kill, and crashes |
| `error` | Only crashes, failed starts, and requests that failed inside outfit |

`--log-level warn` is the setting for a node a fleet polls: a `fleet status`
refresh every few seconds is a request each, and at `info` that is all you will
see. At `warn` the polling disappears and a wrong token still shows up.

Records go to **stderr**, so a foreground `serve` keeps forwarding the engine's
own output untouched. Nothing rotates them — where they end up is your service
manager's business (`journalctl` under systemd, the log files launchd is
pointed at, `docker logs`). The bearer token never appears in a record, and
neither does any request or response body: a pushed deploy config can carry
credentials in its serve args, and the logs endpoint's replies are engine
output.

## Flags

| Flag | Meaning |
| ---- | ------- |
| `-n`, `--dry-run` | Print the server command without running it |
| `-a`, `--api` | Expose the control API beside the foreground engine |
| `--api-addr` | Control API listen address (default `:4242`) |
| `--loopback`, `-l` | (daemon only) Bind the control API to loopback, `127.0.0.1:4242` — needs no token |
| `--log-level` | `debug`, `info` (default), `warn` or `error`; overrides [`OUTFIT_LOG_LEVEL`](../env-vars.md) |

## Notes

- `serve` needs the engine installed: `llama-server` on your `PATH` (e.g.
  `brew install llama.cpp`), or [oMLX](https://omlx.ai).
- For llama.cpp, an Outfit with no `PRESET` must name a `MODEL`. oMLX needs
  neither.

## See also

- [`outfit fleet`](fleet.md) — one outfit observing the daemons on every
  machine you run
- Worked examples with real models: [`examples/`](../../examples/)
- [The `Outfit` file](../outfit-file.md) — full syntax
