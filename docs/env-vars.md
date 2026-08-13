# Environment variables

The environment variables outfit reads. Secrets (API keys, tokens) are resolved
from the environment or a `.env` beside the Outfit — never written into an
`Outfit` or a `fleet.yaml`/config file.

## outfit's own

| Variable | Used by | Meaning |
| --- | --- | --- |
| `OUTFIT_CONFIG_DIR` | everything | outfit's config directory, used **verbatim** (no `outfit` segment appended). Overrides `XDG_CONFIG_HOME` and `~/.config`. Everything outfit owns lives here: `config.json` (default-harness preference + alias registry), `remote.json`, the `remotes/<name>/` environment registry, the daemon state dir, and the CDK source cache. Set it when there is no usable `$HOME` — e.g. a systemd service. See [config resolution](#config-directory-resolution). |
| `OUTFIT_HARNESS` | all harness commands | Which harness to configure/launch (`opencode`, `pi` or `lucinate`). Precedence: `--harness`/`-H` flag > `OUTFIT_HARNESS` > stored preference > `opencode`. |
| `OUTFIT_ALIAS` | every command that takes an Outfit path | A name registered with [`outfit alias`](commands/alias.md), used when the command is given no path. Precedence: the path or alias argument > `OUTFIT_ALIAS` > `./Outfit`. It holds a registry name, never a path, and a same-named file in the working directory does not shadow it. It decides *which* Outfit is the default, not *whether* one is applied — a bare `outfit harness` still applies nothing, and `outfit alias` ignores it. |
| `OUTFIT_PROVIDERS` | `list`, `add`, `apply`, … | Path to a `providers.yaml` that overrides the built-in catalogue. Precedence: `--providers` flag > `OUTFIT_PROVIDERS` > embedded. |
| `OUTFIT_BASE_URL` | `add`, `apply` | Base-URL override for the provider being configured. Precedence: `--base-url`/`-u` > `OUTFIT_BASE_URL` > the provider's own option var > the catalogue default. |
| `OUTFIT_API_TOKEN` | `outfit daemon`, `outfit serve --api` | Bearer token for the daemon control API. One of three peer sources, alongside `--api-token-file` and `--api-token`; two at once is an error. From a service manager prefer the file form — see [serve](commands/serve.md). A non-loopback API listen without any of them refuses to start. |
| `OUTFIT_LOG_LEVEL` | `outfit daemon`, `outfit serve` | How much outfit records about the control API and the supervised engine: `debug`, `info` (default), `warn` or `error`. Precedence: `--log-level` flag > `OUTFIT_LOG_LEVEL` > `info`. An unrecognised value refuses to start rather than falling back to the default. Under `outfit serve` the `.env` beside the Outfit can set it; the daemon reads no Outfit, so there it comes from the environment its service manager gives it. Records go to stderr; see [what gets logged](commands/serve.md#what-gets-logged). |
| *(per-node, named by `tokenEnv`)* | `outfit fleet` | A fleet node's bearer token. `fleet.yaml` names the variable rather than holding the value; it resolves from the environment, then the `.env` beside the fleet file. See [fleet](commands/fleet.md). |
| *(per-node, named by `engineTokenEnv`)* | `outfit fleet`, `outfit harness` | The key a fleet node's **engine** is gated with. Resolved the same way, and supplied by the client when it starts that engine — so the node holds no key of its own and the two ends cannot disagree. See [fleet](commands/fleet.md). |

## Remote (`outfit remote`)

| Variable | Meaning |
| --- | --- |
| `OUTFIT_REMOTE_START_URL` | Override the start Lambda Function URL from the remote config. |
| `OUTFIT_REMOTE_STOP_URL` | Override the stop Lambda Function URL. |
| `OUTFIT_REMOTE_DEPLOY_URL` | Override the deploy Lambda Function URL. |
| `OUTFIT_REMOTE_STATS_URL` | Override the stats Lambda Function URL. |
| `OUTFIT_REMOTE_ENV_URL` | Override the env Lambda Function URL. |
| `OUTFIT_REMOTE_REGION` | Override the AWS region (else `AWS_REGION`, else the region in the Function URL host). |
| `OUTFIT_REMOTE_PACKAGE_MANAGER` | Pin the package manager (`pnpm`/`npm`) `outfit remote bootstrap` uses. |

These let the remote commands run without a `remote.json` on disk — the
config can come entirely from the environment. `outfit remote logs` is the
exception: it needs the environment's name to find its log streams, and that
comes only from the config, so it wants a registered environment (or an Outfit
naming one) rather than environment variables alone.

## Standard variables outfit honours

| Variable | Meaning |
| --- | --- |
| `XDG_CONFIG_HOME` | Base for outfit's config dir (`$XDG_CONFIG_HOME/outfit`) when `OUTFIT_CONFIG_DIR` is unset. |
| `AWS_REGION` | AWS region for the remote control calls when the remote config names none. |
| `HF_TOKEN` | Hugging Face token, used only to seed gated model weights during `outfit remote deploy`. |
| `OPENAI_API_KEY` | The key outfit resolves for OpenAI-compatible and oMLX providers (from the environment or the adjacent `.env`). |

Each provider in the catalogue also names its own key variable (and sometimes a
base-URL or region variable); `outfit list` shows the provider details, and the
key is resolved the same way — environment first, then the `.env` beside the
Outfit.

## Config directory resolution

outfit resolves its config directory once, in this order:

1. `OUTFIT_CONFIG_DIR`, used verbatim;
2. `$XDG_CONFIG_HOME/outfit`;
3. `~/.config/outfit`.

If none of those can be determined — no override, no `XDG_CONFIG_HOME`, and no
resolvable home (as under a bare systemd service) — outfit fails with an error
naming `OUTFIT_CONFIG_DIR`, rather than silently reading or writing a bogus
path. This is why the cloud instance's `outfit daemon` unit pins
`OUTFIT_CONFIG_DIR=/var/lib/outfit`.
