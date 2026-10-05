# Troubleshooting

The things that go wrong, and how to tell which. Each entry says what to look
for and the command that names the fix — spinloop's errors are written to say
that.

## The agent ignores the model

You applied a selection and the agent still runs something else.

- **What does the harness actually hold?** `spinloop harness show` reads the
  agent's config back and reports each configured provider and model. If the
  selection isn't there, the apply did not land where you think it did.
- **Which harness was configured, and which is launching?** Every command
  resolves the harness in this order: `--harness`/`-H`, `SPINLOOP_HARNESS`,
  your stored default, opencode. A `-H pi` apply with an opencode launch
  configures one and runs the other. `spinloop harness config` prints the
  active harness and where that choice came from.
- **On opencode, `add` sets the model as the default. Pi has no default-model
  setting** — after an apply, pick the model with `/model` in Pi.
- **You started the agent yourself, not through `spinloop code`.** The key is
  a reference to an environment variable, not a value in the config. `spinloop
  harness open` and `code` pass the keys they can resolve; a hand-started
  agent gets nothing unless you set the variables in its environment.

## The engine won't start

`spinloop serve` (or a wake) fails at launch.

- **The engine is not installed.** `serve` runs the engine; it does not ship
  one. `llama-server` must be on your `PATH` (`brew install llama.cpp`);
  oMLX is found on the `PATH` or at
  `/Applications/oMLX.app/Contents/MacOS/omlx-cli`; `mtplx` on the `PATH`.
- **The port is taken.** The engine binds the `BASEURL` address (or the
  engine's own default). Change `BASEURL` in the Spinloop, or stop whatever
  holds the port.
- **A `PRESET` with several sections and no `ALIAS`** is an error, naming the
  sections — name one. Presets are written in one engine's flag vocabulary and
  are not portable between engines.
- **`--dry-run` shows the command that will run.** Read it before debugging
  anything else.

## The context is smaller than asked for

llama.cpp's `--ctx-size` is a *total* KV-cache budget it divides across
`--parallel` slots. `spinloop` compensates: `CONTEXT 128k` + `PARALLEL 2`
renders `--ctx-size 256000 --parallel 2`, so each request still gets 128k. A
`PRESET`'s own `ctx-size`, left unstated by the Spinloop, is **not**
retroactively scaled — the preset is trusted to account for its own slots.
vLLM and MTPLX share one pool across requests, so their context is never
scaled; see [`spinloop serve`](commands/serve.md#parallelism).

## A key is missing or not picked up

- Keys are looked up in a `.env` **beside the `Spinloop` being applied** (or
  in the current directory for a command that takes no Spinloop), then your
  shell environment. A project's key travels with the project that way.
- A provider that requires a key tells you which variable to set when the
  lookup comes up empty — set the one it names.
- Local providers on localhost (Ollama, llama.cpp) need no key; Bedrock uses
  your AWS credentials.

## The daemon refuses to start, or shows `crashed`

- **A non-loopback listen with no token refuses to start**, naming the three
  ways to supply one (`--api-token-file`, `SPINLOOP_API_TOKEN`,
  `--api-token`). Giving two at once is an error, not a silent precedence.
- **A crash is reported, never auto-restarted.** The state says `crashed`;
  read `daemon/engine.log` under [spinloop's config
  directory](env-vars.md#config-directory-resolution), then start the engine
  again through the API.

## A remote endpoint won't come up

- **The control plane is not deployed.** Every `remote` command that acts on
  an endpoint needs `spinloop remote bootstrap` to have run once per account;
  a missing control plane says so. An older control plane that lacks a feature
  says to re-run `bootstrap` to add it.
- **A warning says the control plane is at a different version.** The
  `spinloop` you are running differs from the one that deployed the control
  plane. Commands still run, but re-run `spinloop remote bootstrap` to update
  it, or install the matching `spinloop`.
- **The AMI is not baked.** `spinloop remote bake` once per engine, and it
  waits until the AMI is available.
- **A cold start takes about ten minutes.** `start` prints its progress on
  stderr; `--timeout` (default 15m) bounds the wait. `status` and `logs`
  answer while it boots and after it is gone — logs are readable even from a
  terminated instance.
- **Quota.** Bootstrap needs enough GPU vCPU quota for a later launch; a launch
  that can't get an instance reports the AWS error.

## A fleet row is wrong, or a node won't answer

- **`config-error` on a `kind: remote` row** means the environment is not
  registered on this machine — `spinloop remote deploy --env <name>` (or
  `spinloop fleet deploy`) writes its `remote.json`.
- **A node's token is not in *your* shell.** The fleet file names the
  *variable* (`tokenEnv`), never the value. Set the variable the row names,
  from the `.env` beside the fleet file or your environment.
- **Host and port name the daemon, not the engine.** If the daemon answers but
  routing can't reach the engine, the engine is bound to loopback — the
  failure says so. Bind it to a reachable address or declare an `engine`
  block in the fleet file.
- **A down node never blanks the view.** It is reported in its place, the way
  every fleet view reports it — the rest of the fleet keeps answering.

## The gateway refuses a request

- **401** — the caller's token. It is the gateway's token (the three sources
  the daemon's token follows), not any node's.
- **Refused, naming a node** — nothing serves the model and no candidate may
  be woken (its own `wake`, or the file's, is off). The failure names the node
  that would have woken and the `spinloop fleet start <node>` command that
  would start it.
- **An undeployed remote environment is never a candidate** — it has nothing
  to serve yet; choosing what to deploy is `spinloop remote deploy`'s call.
- **404 naming other paths** — the gateway serves `/health`, `/v1/models`,
  `/v1/chat/completions`, `/v1/completions`, and `/v1/fleet`, and says so.

## `spinloop hf` inferred the wrong thing

The narration on stderr says what was chosen and why — provider and why,
quantisation and the alternatives, context and its source. Override any of it:
`-p` for the provider, `-q` for the quantisation, `-c` for the context, `-a`
for the alias. A quantisation the repo does not have fails listing the ones it
does.

## Still stuck

The source is on [GitHub](https://github.com/spinloop-ai/spinloop) — open an
issue with the command you ran and its output. `--dry-run` on `serve`,
`spinloop harness show`, and the daemon's `daemon/engine.log` are the three
facts that answer most questions.
