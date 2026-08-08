# outfit serve

Run the inference server for the model an [`Outfit` file](../outfit-file.md)
names — so the same file that points your agent at a local model can also start
the server behind it.

```sh
outfit serve              # reads ./Outfit and runs its PROVIDER's server
outfit serve path/to/Outfit
outfit serve qwen3.6-27b  # a name registered with `outfit alias`
outfit serve --dry-run    # print the command without launching the server
```

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

A relative `PRESET` path resolves against the Outfit's own directory, so the
pair can travel together.

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

## Daemon mode and the control API

`serve --daemon` (`-d`) supervises the engine instead of running it in the
foreground: it starts the engine detached, writes its output to
`~/.config/outfit/daemon/engine.log`, tracks its state (`idle`, `running`,
`stopped`, `crashed` — a crash is reported, never auto-restarted), and keeps
running until interrupted, stopping the engine on the way out. The daemon
stays in the foreground itself; background it with tmux, systemd, launchd or
similar.

With `--daemon` the control API is on by default (`--api=false` turns it off);
`--api` alone exposes the same API over an ordinary foreground serve.
The API listens on `:4242` (change with `--api-addr`) and speaks JSON.
See [HTTP Control API](../http-api.md) for details:


| Endpoint | Meaning |
| -------- | ------- |
| `GET /v1/status` | Engine state, what is served, the engine log path |
| `POST /v1/start` | Start the engine (409 while one runs) |
| `POST /v1/stop` | Stop the engine (idempotent) |
| `GET /v1/metrics` | Engine token counters plus host GPU/CPU/RAM |
| `PUT /v1/deploy-config` | Set what the *next* start serves |

Requests carry `Authorization: Bearer $OUTFIT_API_TOKEN`. The token is read
from the environment (the `.env` beside the Outfit works — it is loaded
first), never from a flag; a non-loopback listen with no token refuses to
start. What to serve resolves in order: a pushed deploy config (the same shape
`outfit remote deploy` derives from an Outfit and its preset), else the
Outfit the daemon started beside; with neither the daemon starts idle and
waits for a push. A supervised engine gets its own `/metrics` endpoint
switched on (llama.cpp `--metrics`), which is where the token counters come
from; GPU readings need `nvidia-smi` (no Apple GPU source yet).

## Flags

| Flag | Meaning |
| ---- | ------- |
| `-n`, `--dry-run` | Print the server command without running it |
| `-d`, `--daemon` | Supervise the engine and keep running (implies `--api`) |
| `-a`, `--api` | Expose the control API (on by default with `--daemon`) |
| `--api-addr` | Control API listen address (default `:4242`) |

## Notes

- `serve` needs the engine installed: `llama-server` on your `PATH` (e.g.
  `brew install llama.cpp`), or [oMLX](https://omlx.ai).
- For llama.cpp, an Outfit with no `PRESET` must name a `MODEL`. oMLX needs
  neither.

## See also

- Worked examples with real models: [`examples/`](../../examples/)
- [The `Outfit` file](../outfit-file.md) — full syntax
