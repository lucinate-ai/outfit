# Environment variables

The environment variables spinloop reads. Secrets (API keys, tokens) are resolved
from the environment or a `.env` beside the Spinloop — never written into an
`Spinloop` or a `fleet.yaml`/config file.

## spinloop's own

| Variable | Used by | Meaning |
| --- | --- | --- |
| `SPINLOOP_CONFIG_DIR` | everything | spinloop's config directory, used **verbatim** (no `spinloop` segment appended). Overrides `XDG_CONFIG_HOME` and `~/.config`. Everything spinloop owns lives here: `config.json` (default-harness preference + alias registry), `remote.json`, the `remotes/<name>/` environment registry, the `keystore/` file credential store, the daemon state dir, and the CDK source cache. Set it when there is no usable `$HOME` — e.g. a systemd service. See [config resolution](#config-directory-resolution). |
| `SPINLOOP_HARNESS` | all harness commands | Which harness to configure/launch (`opencode`, `pi` or `lucinate`). Precedence: `--harness`/`-H` flag > `SPINLOOP_HARNESS` > stored preference > `opencode`. |
| `SPINLOOP_ALIAS` | every command that takes a Spinloop path | A name registered with [`spinloop alias`](commands/alias.md), used when the command is given no path. Precedence: the path or alias argument > `SPINLOOP_ALIAS` > `./Spinloop`. It holds a registry name, never a path, and a same-named file in the working directory does not shadow it. It decides *which* Spinloop is the default, not *whether* one is applied — a bare `spinloop harness open` still applies nothing, and `spinloop alias` ignores it. |
| `SPINLOOP_PROVIDERS` | `spinloop provider list`, `spinloop harness add`, `spinloop harness apply`, … | Path to a `providers.yaml` that overrides the built-in catalogue. Precedence: `--providers` flag > `SPINLOOP_PROVIDERS` > embedded. |
| `SPINLOOP_BASE_URL` | `spinloop harness add`, `spinloop harness apply` | Base-URL override for the provider being configured. Precedence: `--base-url`/`-u` > `SPINLOOP_BASE_URL` > the provider's own option var > the catalogue default. |
| `SPINLOOP_API_TOKEN` | `spinloop daemon`, `spinloop serve --api`, `spinloop gateway` | Bearer token for the daemon control API — and the token a [gateway](commands/gateway.md)'s callers must present. One of three peer sources, alongside `--api-token-file` and `--api-token`; two at once is an error. From a service manager prefer the file form — see [serve](commands/serve.md). A non-loopback listen without any of them refuses to start. |
| `SPINLOOP_REMOTE_KEYSTORE` | `spinloop remote auth` | Set to `file` to keep the stored control-plane credential in the owner-only file under the config directory, even where an OS keystore is reachable — the opt-out for a machine whose keystore is locked or unreachable. Unset, the OS keystore is used where available. See [credentials](commands/remote.md#credentials). |
| `SPINLOOP_LOG_LEVEL` | `spinloop daemon`, `spinloop serve` | How much spinloop records about the control API and the supervised engine: `debug`, `info` (default), `warn` or `error`. Precedence: `--log-level` flag > `SPINLOOP_LOG_LEVEL` > `info`. An unrecognised value refuses to start rather than falling back to the default. Under `spinloop serve` the `.env` beside the Spinloop can set it; the daemon reads no Spinloop, so there it comes from the environment its service manager gives it. Records go to stderr; see [what gets logged](commands/serve.md#what-gets-logged). |
| *(per-node, named by `tokenEnv`)* | `spinloop fleet` | A fleet node's bearer token. `fleet.yaml` names the variable rather than holding the value; it resolves from the environment, then the `.env` beside the fleet file. See [the fleet file](fleet-file.md#tokens). |
| *(per-node, named by `engineTokenEnv`)* | `spinloop fleet`, `spinloop harness open` | The key a fleet node's **engine** is gated with. Resolved the same way, and supplied by the client when it starts that engine — so the node holds no key of its own and the two ends cannot disagree. See [the fleet file](fleet-file.md#tokens). |
| *(fleet-wide, named by `apiKeyEnv`)* | `spinloop fleet`, `spinloop harness open` | The default key for a `kind: remote` environment's engine, for every remote node that does not name its own `engineTokenEnv`. Resolved the same way. See [the fleet file](fleet-file.md#tokens). |

## Remote (`spinloop remote`)

| Variable | Meaning |
| --- | --- |
| `SPINLOOP_REMOTE_START_URL` | Override the start Lambda Function URL from the remote config. |
| `SPINLOOP_REMOTE_STOP_URL` | Override the stop Lambda Function URL. |
| `SPINLOOP_REMOTE_DEPLOY_URL` | Override the deploy Lambda Function URL. |
| `SPINLOOP_REMOTE_STATS_URL` | Override the stats Lambda Function URL. |
| `SPINLOOP_REMOTE_ENV_URL` | Override the env Lambda Function URL. |
| `SPINLOOP_REMOTE_UPDATE_URL` | Override the update Lambda Function URL (drives `keep`). |
| `SPINLOOP_REMOTE_SCHEDULE_URL` | Override the schedule Lambda Function URL (drives `schedule`). |
| `SPINLOOP_REMOTE_REGION` | Override the AWS region (else `AWS_REGION`, else the region in the Function URL host). |
| `SPINLOOP_REMOTE_PACKAGE_MANAGER` | Pin the package manager (`pnpm`/`npm`) `spinloop remote bootstrap` and `bake` use. |

These let the remote commands run without a `remote.json` on disk — the config
can come entirely from the environment. `--env <name>` is still required, and
on this path the name you give *is* the environment identifier the control
plane acts on, since there is no file to take one from:

```sh
SPINLOOP_REMOTE_START_URL=... SPINLOOP_REMOTE_STOP_URL=... SPINLOOP_REMOTE_REGION=... \
  spinloop remote start --env ci
```

## Standard variables spinloop honours

| Variable | Meaning |
| --- | --- |
| `XDG_CONFIG_HOME` | Base for spinloop's config dir (`$XDG_CONFIG_HOME/spinloop`) when `SPINLOOP_CONFIG_DIR` is unset. |
| `AWS_REGION` | AWS region for the remote control calls when the remote config names none. |
| `HF_TOKEN` | Hugging Face token. Read by `spinloop hf` (sent as a bearer for gated or private repos) and used to seed gated model weights during `spinloop remote deploy`. Precedence in `hf`: `HF_TOKEN` > `HUGGING_FACE_HUB_TOKEN` > the token file. |
| `HUGGING_FACE_HUB_TOKEN` | Hugging Face token, the second of the two `spinloop hf` reads, after `HF_TOKEN`. |
| `HF_HOME` | Base of the Hugging Face home: its `token` file (third in `hf`'s token order) and, when `HF_HUB_CACHE` is unset, the hub cache lives at `$HF_HOME/hub`. |
| `HF_HUB_CACHE` | The Hugging Face hub cache `spinloop hf` checks for a copy already on disk, before the hub. Default `$HF_HOME/hub`, else `~/.cache/huggingface/hub`. |
| `HF_ENDPOINT` | The hub host `spinloop hf` reads repo metadata from. Default `https://huggingface.co`. |
| `LLAMA_CACHE` | llama.cpp's cache `spinloop hf` also checks for a copy already on disk — a model `llama-server` downloaded lives here, not in the hub cache. Default the platform's user cache directory (`~/.cache/llama.cpp` on Linux, `~/Library/Caches/llama.cpp` on macOS). |
| `OPENAI_API_KEY` | The key spinloop resolves for OpenAI-compatible, oMLX and MTPLX providers (from the environment or the adjacent `.env`). |

Each provider in the catalogue also names its own key variable (and sometimes a
base-URL or region variable); `spinloop provider list` shows the provider details, and the
key is resolved the same way — environment first, then the `.env` beside the
Spinloop.

## Config directory resolution

spinloop resolves its config directory once, in this order:

1. `SPINLOOP_CONFIG_DIR`, used verbatim;
2. `$XDG_CONFIG_HOME/spinloop`;
3. `~/.config/spinloop`.

If none of those can be determined — no override, no `XDG_CONFIG_HOME`, and no
resolvable home (as under a bare systemd service) — spinloop fails with an error
naming `SPINLOOP_CONFIG_DIR`, rather than silently reading or writing a bogus
path. This is why the cloud instance's `spinloop daemon` unit pins
`SPINLOOP_CONFIG_DIR=/var/lib/spinloop`.
