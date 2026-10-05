# spinloop serve

Run the inference server for the model an [`Spinloop` file](../spinloop-file.md)
names — so the same file that points your agent at a local model can also start
the server behind it.

```sh
spinloop serve              # reads ./Spinloop and runs its PROVIDER's server
spinloop serve path/to/Spinloop
spinloop serve qwen3.6-27b  # a name registered with `spinloop alias`
spinloop serve https://example.com/Spinloop   # a URL, fetched instead of read from disk
spinloop serve --dry-run    # print the command without launching the server
```

With no argument, `SPINLOOP_ALIAS` names the Spinloop before `./Spinloop` is tried —
see [`spinloop alias`](alias.md#naming-one-for-the-whole-shell).

It prints the command before running it, and never touches your agent's
config — pair it with [`spinloop harness apply`](harness.md#spinloop-harness-apply) to point the agent at the
server.

## On a terminal, the serve view

Run `serve` on a terminal and the engine runs under a full-screen view rather
than forwarding its output: the engine's **metrics** above, its **log** below,
and a footer naming the keys the view answers to.

- **Metrics** — the same facts the dashboard's node detail screen shows
  for the same engine: state and uptime, what is served, last active, and the
  resource series — CPU, RAM, and each GPU's utilisation and memory — with
  every series drawn in both formats at once, each on one line: a gauge of
  the current reading beside the bar of its retained history. Below them, the
  token and request counters. The reading comes from the daemon the serve
  process runs in-process and refreshes on the dashboard's own local cadence;
  a reading the view could not renew is shown with its age.
- **Log** — the engine's own output, tailed and followed, so new lines appear
  as they are written. An engine that has written nothing yet shows a waiting
  note, not an empty pane.
- **Footer** — the view's keys, and nothing the view cannot do. Starting,
  stopping, keeping and aborting are not among them: the engine is serve's
  own, and leaving is what stops it.

| Key | What it does |
| --- | ------------ |
| `↑` / `↓` | Scroll the log one line; a press at either end leaves the window where it is |
| `pgup` / `pgdown` | Scroll the log by a page |
| `f` | Pause and resume the log's follow — the metrics section keeps refreshing either way |
| `q` or `Ctrl+C` | Leave — stops the engine and exits serve |

While the log's window is on the newest line it sticks to the tail: new
output appears as it is written. Scrolled away from it, the window stays put
and the new lines accrue behind it. Pausing the follow holds the window, and
resuming fetches whatever the engine wrote in the meantime — nothing is lost.

The engine's own exit closes the view and serve exits with the engine's exit
status, exactly as a foreground serve does.

Under the view, the engine's stdout and stderr are captured to the same
`daemon/engine.log` [spinloop's daemon](#the-control-api-api-and-spinloop-daemon)
writes, from the engine's first line — so with `--api` the control API's log
endpoint serves the engine's output rather than reporting the log missing.

Off a terminal — piped or redirected — there is no view: the engine's output
is forwarded to serve's own stdio as before, and the printed command stays on
stdout. On a terminal the view owns stdout, so the command serve prints goes
to stderr there. `--dry-run` never opens the view.

## The engine comes from `PROVIDER`

`PROVIDER` already names the engine, so `serve` needs no keyword of its own —
the same way [`spinloop remote deploy`](remote.md) picks the engine for a cloud
GPU:

| `PROVIDER` | `serve` runs |
| ---------- | ------------ |
| `llamacpp` | `llama-server` |
| `omlx` | [oMLX](https://omlx.ai) on Apple Silicon |
| `vllm` | `vllm serve` (the model as its positional argument) |
| `mtplx` | [`mtplx serve`](https://mtplx.com) on Apple Silicon |

Any other provider is an error: `serve` launches a self-hosted engine, and the
rest of the catalogue names endpoints somebody else runs.

Each engine reads its `PRESET` in its **own** flag vocabulary. That matters:
llama.cpp's short aliases would rewrite another engine's keys (`m` to `--model`,
`c` to `--ctx-size`), so a preset is never portable between engines.

## Parallelism

`CONTEXT` always means the context window a **single request** gets, whatever
engine serves it. `PARALLEL` sets the number of concurrent request slots, and
`spinloop` translates it per engine so `CONTEXT`'s meaning holds everywhere:

| `PROVIDER` | `PARALLEL n` becomes | Effect on `CONTEXT` |
| ---------- | --------------------- | -------------------- |
| `llamacpp` | `--parallel n` | Scaled: `--ctx-size` becomes `context * n` |
| `vllm`     | `--max-num-seqs n` | Unscaled: `--max-model-len` is unaffected |
| `omlx`     | `--max-concurrent-requests n` | No context flag either way |
| `mtplx`    | `--max-active-requests n` | Unscaled: `--context-window` is unaffected |

The `llamacpp` scaling exists because llama.cpp's own `--ctx-size` is a total
KV-cache budget it divides across `--parallel` slots — so without help, asking
for `CONTEXT 128k` with two parallel slots would silently give each request
64k. `spinloop` compensates by scaling: `CONTEXT 128k` + `PARALLEL 2` renders
`--ctx-size 256000 --parallel 2`, so each slot still gets the 128k the Spinloop
asked for.

`vllm` and `mtplx` need no such compensation: both share one dynamically-sized
KV-cache pool across concurrent requests via continuous batching rather than
dividing a fixed budget per slot, so their own context settings
(`--max-model-len`, `--context-window`) are already a per-request ceiling —
`PARALLEL` only caps how many requests run at once. `omlx` has no context flag
at all, so there is nothing to compensate there either.

If `PARALLEL` is left out entirely, it changes nothing — no `--parallel`-family
flag is added, and `CONTEXT` maps to the engine's context flag exactly as it
always has. It only applies to a Spinloop-stated `CONTEXT`: a `PRESET`'s own
`ctx-size`, left unstated by the Spinloop, is not retroactively scaled — the
preset is trusted to already account for its own slots. Like `CONTEXT`,
`PARALLEL` overrides a preset's own `np`/`parallel`, `max-num-seqs`, or
`max-concurrent-requests` value by the usual override rule.

`PARALLEL` is Spinloop-file-only — like `PRESET`, it has no meaning for a hosted
provider selection, so there is no `spinloop harness add --parallel`.

## llama.cpp

### Simple case — straight from the Spinloop

With no `PRESET`, `serve` builds the command from the Spinloop itself:

```dockerfile
PROVIDER llamacpp
MODEL    unsloth/Qwen3.6-35B-A3B-GGUF:UD-Q4_K_XL   # an HF repo, or a .gguf path
ALIAS    qwen3.6                                    # llama-server --alias
CONTEXT  32768                                      # llama-server --ctx-size
PARALLEL 2                                          # llama-server --parallel (see below)
BASEURL  http://127.0.0.1:8080/v1                   # llama-server --host/--port
```

`MODEL` becomes `-hf` (a Hugging Face repo) or `-m` (anything that looks like a
path or ends in `.gguf`); `ALIAS`, `CONTEXT`, and `BASEURL` fill in the rest.

### Full control — a llama.cpp preset

For flags a Spinloop doesn't model — `-ngl`, `--jinja`, KV-cache types, draft
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
**Spinloop** also states wins over both, so you can keep a shared preset and
tweak one field per project: `CONTEXT` overrides the section's `ctx-size`,
`BASEURL` its `host`/`port`, `ALIAS` its `alias`, and `MODEL` its `hf`/`model`.
Keys map straight to flags — `ctx-size = 262144` becomes `--ctx-size 262144`,
`hf` becomes `--hf-repo`, and boolean toggles like `mmap = 1` become a bare
`--mmap`. Which section runs:

- `ALIAS` names the section.
- With no `ALIAS`, a preset holding exactly one section serves that one.
- Several sections and no `ALIAS` is an error — name one.

A relative `PRESET` path resolves against the Spinloop's own directory — or
against its URL, when the Spinloop itself was fetched from one — so the pair
can travel together either way. `PRESET` may also be an absolute URL of its
own, fetched only when `serve` builds the command, never merely because the
Spinloop was read. See [Fetching a Spinloop from a
URL](../spinloop-file.md#fetching-a-spinloop-from-a-url).

## oMLX

[oMLX](https://omlx.ai) serves MLX models on Apple Silicon. It differs from
llama.cpp in one way that shapes everything else: it loads a whole **model
directory** and picks the model per request, rather than being launched with one
model. So a bare Spinloop is enough to start it:

```dockerfile
PROVIDER omlx
BASEURL  http://127.0.0.1:8000/v1   # omlx-cli serve --host/--port
```

- `BASEURL` sets the bind address. With none, oMLX's own defaults stand.
- `MODEL` and `ALIAS` keep their usual job of naming what the *agent* asks for;
  they are not launch flags.
- `CONTEXT` sizes the *harness's* window — oMLX has no context flag.
- `PARALLEL` becomes `--max-concurrent-requests` — see
  [Parallelism](#parallelism) above.

Everything else — the model directory, the memory guard, the SSD cache — comes
from a `PRESET` written in oMLX's own flags, or from oMLX's settings
(`~/.omlx/settings.json`, and its admin panel):

```ini
[*]
model-dir = /Users/you/models

[default]                      # selected by the Spinloop's ALIAS
memory-guard            = safe
paged-ssd-cache-dir     = /Users/you/.omlx/cache
max-concurrent-requests = 16
```

Preset values are passed to the server verbatim, so `~` is not expanded — write
paths out in full.

`serve` never passes `--api-key`. It prints the command it runs, and oMLX takes
its key on the command line, so passing one would put the secret on your screen
and in the process table. If you want auth on the server, configure it in oMLX;
`spinloop harness add`/`apply` still picks up `OPENAI_API_KEY` for the agent's own config.

Note that oMLX can require a key even on localhost (it is an admin-panel
setting). Because the `omlx` provider is `apiKeyOptional`, `spinloop` only writes
the key reference when `OPENAI_API_KEY` is set **at apply time** — so if your
oMLX needs a key, set it before `add`/`apply`, not just before launching the
agent.

### Finding the binary

oMLX ships as a macOS app, so `serve` looks for `omlx-cli` on your `PATH` first
and falls back to `/Applications/oMLX.app/Contents/MacOS/omlx-cli`. If you've
only ever launched it from the menu bar, the fallback is the one that finds it.

## vLLM

```dockerfile
PROVIDER vllm
MODEL    Qwen/Qwen3.6-27B-FP8
ALIAS    friendly                          # vllm serve --served-model-name
CONTEXT  32768                             # vllm serve --max-model-len
PARALLEL 4                                 # vllm serve --max-num-seqs (see below)
BASEURL  http://0.0.0.0:8000/v1            # vllm serve --host/--port
```

`MODEL` is `vllm serve`'s positional argument, not a flag. `ALIAS`, `CONTEXT`,
and `BASEURL` fill in the rest, exactly as for llama.cpp. `PARALLEL` becomes
`--max-num-seqs` — a concurrency cap, not a context divisor: see
[Parallelism](#parallelism) above for why vLLM's `CONTEXT` is never scaled by
it, unlike llama.cpp's.

`--tensor-parallel-size`/`--pipeline-parallel-size` (sharding a model across
GPUs) are a different concept from `PARALLEL` and are not derived from it —
set them by hand in a `PRESET`.

## MTPLX

[MTPLX](https://mtplx.com) serves optimised models on Apple Silicon. Like
llama.cpp it is launched with one model, so a `MODEL` is required:

```dockerfile
PROVIDER mtplx
MODEL    Youssofal/Qwen3.8-27B-MTPLX-Optimized-Speed   # an HF repo, or a local path
ALIAS    qwen                                           # mtplx serve --model-id
CONTEXT  128k                                           # mtplx serve --context-window
PARALLEL 2                                              # mtplx serve --max-active-requests (see below)
BASEURL  http://127.0.0.1:8000/v1                       # mtplx serve --host/--port
```

- `MODEL` becomes `--model`, taken verbatim — an Hugging Face repo id or a local
  path alike. `--download` is always passed, so a repo that is not already
  local is fetched by the engine rather than failing the launch.
- `ALIAS` becomes `--model-id` — the name the model is served under.
- `CONTEXT` becomes `--context-window` — a per-request ceiling, never scaled by
  `PARALLEL`: see [Parallelism](#parallelism) above.
- `PARALLEL` becomes `--max-active-requests` — an admission cap on how many
  requests run at once. It never selects the engine's scheduling mode.
- `BASEURL` sets the bind address. With none, no bind flag is emitted and
  MTPLX's own defaults stand.

The scheduling mode (`--scheduler-mode`) is per-deployment tuning, not a
Spinloop field — set it in a `PRESET`, written in MTPLX's own long-form flags.
`serve` passes the value through without checking it, so it must be valid for
the installed `mtplx` (`mtplx serve --help` lists the current modes). Every
preset key is passed through unchanged, so a preset is portable only to MTPLX,
as with every engine.

`serve` never passes `--api-key`, for the same reason as [oMLX](#omlx): it
prints the command it runs, and a key on the line would be in your screen and
the process table. A supervised engine (`--api` or the daemon) is gated with a
key file the daemon writes instead. Because the `mtplx` provider is
`apiKeyOptional`, `spinloop harness add`/`apply` only writes the key reference when
`OPENAI_API_KEY` is set at apply time.

### Finding the binary

`serve` looks for `mtplx` on your `PATH`. Install it from
[mtplx.com](https://mtplx.com) if it is not there.

## The control API (`--api`) and `spinloop daemon`

`serve` is strictly foreground: it runs the engine in front of you until one
of you exits. Two related surfaces build on it:

- `serve --api` (`-a`) exposes the control API *beside* the foreground
  engine — status and metrics answer, start fails (the engine is already
  running), and stop terminates the engine, after which serve exits as it
  always has. The flag changes only whether the API listens: the foreground
  behaviour — the view on a terminal, stdio forwarding off one — is the same
  with and without it.
- `spinloop daemon` is the long-lived agent: it supervises one engine, writes
  its output to `daemon/engine.log` under
  [spinloop's config directory](../env-vars.md#config-directory-resolution),
  tracks its state
  (`idle`, `running`, `stopped`, `crashed` — a crash is reported, never
  auto-restarted), and starts **nothing** until a start request asks. Stopping
  the engine leaves the daemon answering; only a signal ends it. The daemon
  stays in the foreground itself; background it with tmux, systemd, launchd or
  similar.

The daemon is a worker: **its inputs are its flags and its API, and nothing
else**. It reads no Spinloop, no preset and no `fleet.yaml`, and takes no Spinloop
path — passing one is an error rather than being quietly ignored. What a node
runs is decided by the client that asks: the start request's own deploy config,
or the one stored from a previous ask. With neither, a start says so.

That is why a node and a client want different files. A client's Spinloop names a
model and a fleet; a node holds nothing. See
[`examples/fleet-local/`](https://github.com/spinloop-ai/spinloop/tree/main/examples/fleet-local)
for the whole shape on one machine.

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
| `GET /v1/logs` | A slice of the engine's captured output, by offset — where the output is captured: under the daemon, and under the serve view; a plain foreground serve forwards its engine's output to its own stdio, and the endpoint reports the log missing |
| `PUT /v1/deploy-config` | Set what the *next* start serves |

Requests carry `Authorization: Bearer <token>`. The token comes from one of
three places, and giving two at once is an error rather than a silent
precedence:

| Source | Notes |
| ------ | ----- |
| `--api-token-file <path>` | The file's contents, trimmed. |
| `SPINLOOP_API_TOKEN` | The environment. |
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
readings come from `nvidia-smi` on Linux and from the I/O Kit accelerator
(`ioreg`) on macOS, where only utilisation and the GPU name are reported — no
GPU memory or temperature.

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
| `debug` | The above, plus every successful request summary and the full engine command line |
| `info` (default) | Starts, stops, clean exits, rejections and failures — successful requests are debug |
| `warn` | Only rejected requests (401, a bad cursor), a slow shutdown escalating to a kill, and crashes |
| `error` | Only crashes, failed starts, and requests that failed inside spinloop |

`--log-level warn` is the setting for a node a fleet polls: a `status`
refresh every few seconds is a request each, and at the default level polling is
quiet. `--log-level debug` is how to see the routine traffic; at `warn` the
polling disappears and a wrong token still shows up.

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
| `--log-level` | `debug`, `info` (default), `warn` or `error`; overrides [`SPINLOOP_LOG_LEVEL`](../env-vars.md) |

## Notes

- `serve` needs the engine installed: `llama-server` on your `PATH` (e.g.
  `brew install llama.cpp`), or [oMLX](https://omlx.ai).
- For llama.cpp, a Spinloop with no `PRESET` must name a `MODEL`. oMLX needs
  neither.

## See also

- [`spinloop up`](up.md) — the one-word form: this, from a directory holding
  the Spinloop
- [`spinloop fleet`](fleet.md) — one spinloop observing the daemons on every
  machine you run
- Worked examples with real models:
  [`examples/`](https://github.com/spinloop-ai/spinloop/tree/main/examples)
- [The `Spinloop` file](../spinloop-file.md) — full syntax
