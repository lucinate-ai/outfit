# outfit remote

Run a model too big for your laptop on a GPU in the cloud, from the same
[`Outfit` file](../outfit-file.md) you'd use locally — and only pay for it
while you're using it.

```sh
outfit remote bootstrap  # once per account: deploy the control plane
outfit remote deploy     # create an endpoint (environment) and tell it what to serve
outfit remote start      # boot it; prints the exports your agent needs (progress on stderr)
outfit remote status     # is it up? is it healthy?
outfit remote logs       # what did it say? (readable after it's gone)
outfit remote stop       # shut it down now, rather than waiting for the idle timer
```

The endpoint is the one [`remote/`](../../remote/) in this repository deploys: a
GPU instance that exists only while you're using it, and terminates itself once
you stop.

## Bootstrapping the account

Before any endpoint can run, the account-level control plane has to
exist — much like `cdk bootstrap`. `outfit remote bootstrap` does it once per
account: it downloads the `remote/` CDK project (version-matched to your binary)
and deploys the control plane — the EC2 Image Builder pipelines and baked AMIs,
the lifecycle Lambdas, and the shared weights bucket, roles and VPC — publishing
them as CloudFormation outputs that `outfit remote deploy` discovers later.

```sh
outfit remote bootstrap                 # shows a consent plan, then deploys
outfit remote bootstrap --dry-run       # print the plan and do nothing
outfit remote bootstrap --runners llamacpp   # bake only one engine's AMI
outfit remote bootstrap --wait          # block until the AMI bake(s) finish
outfit remote bootstrap --package-manager npm  # use npm instead of pnpm
```

Before deploying, bootstrap prints a plan — the target account and region, the
control-plane resources, the cost, and the exact commands — and asks you to confirm
(`--yes` skips the prompt). It creates **no** Elastic IP or instance and **no**
environment; those come from `outfit remote deploy`. Re-running is safe: it
updates the control-plane stack and doesn't touch any live instance. It needs Node 22, a
Node package manager, AWS credentials, and enough GPU vCPU quota for a later
launch.

By default bootstrap uses `pnpm` and falls back to `npm` when `pnpm` isn't on the
path, logging which one it picked. To pin the choice, pass `--package-manager`
(`pnpm` or `npm`) or set `OUTFIT_REMOTE_PACKAGE_MANAGER`; the flag wins over the
env var. A pinned manager that isn't installed fails the preflight rather than
falling back.

## The usual flow

```sh
eval "$(outfit remote start)"           # boots it (~10 min from cold) and sets
                                       # OPENAI_BASE_URL and OPENAI_API_KEY
outfit apply                           # point your agent at it
outfit harness                         # work
outfit remote stop                     # done
```

Forgetting `stop` is not a disaster — the endpoint terminates itself after a
spell with no requests — but it's the difference between minutes and hours of
GPU time.

## Pointing at your endpoint

`outfit remote` needs the endpoint's control URLs, which its deployment prints.
Put them in a JSON file:

```json
{
  "start_url": "https://....lambda-url.us-east-1.on.aws/",
  "stop_url": "https://....lambda-url.us-east-1.on.aws/",
  "deploy_url": "https://....lambda-url.us-east-1.on.aws/",
  "region": "us-east-1",
  "base_url": "http://198.51.100.7:8000/v1"
}
```

`base_url` is the endpoint's own address, and it's optional — `remote` doesn't
need it, since `start` and `status` report the address themselves. It's there
for [`outfit apply`](apply.md): an Outfit for a remote endpoint can leave out
`BASEURL` and let apply take the address from here, so the address stays with
the deployment that owns it. A `BASEURL` in the Outfit wins if you set one.

Either name it from the Outfit, so a project carries its own endpoint:

```dockerfile
REMOTE ./remote.json         # a path: resolved next to the Outfit, like PRESET
REMOTE qwen3.6-27b-prod      # a bare name: an environment in the registry
```

A bare name (no slash, no `.json`) selects a **named environment** from the
per-user registry at `~/.config/outfit/remotes/<name>/remote.json`. This keeps
deployment state per-user and per-instance: two projects name two environments
without clobbering, and only the name — not the URLs — lives in the committed
Outfit. `outfit remote bootstrap` registers an environment for you; you can also
create one by hand.

Given no path, `outfit remote` reads the Outfit `OUTFIT_ALIAS` names, or
`./Outfit` when you are standing beside one. With neither — or with one that
names no `REMOTE` — it uses the `default` environment
(`~/.config/outfit/remotes/default/remote.json`), so it works from anywhere.
An existing `~/.config/outfit/remote.json` from before the registry is still
read as the default; move it to `remotes/default/remote.json` when convenient.

## Listing environments

```sh
outfit remote ls
```

lists each registered environment with its base URL and region, marking any
whose `remote.json` is missing or unreadable. It contacts no endpoint.

Requests are signed with **your** AWS credentials (the usual profile, SSO
session, or environment variables), and the endpoint's URLs require it. Outfit
stores no credentials of its own. Beyond invoking those URLs, the only extra
permission it wants is for [reading logs](#reading-the-logs), which talks to
CloudWatch rather than to an endpoint.

## Reading the logs

```sh
outfit remote logs                      # the last hour of engine output
outfit remote logs --source boot        # the start-up log, before the engine ran
outfit remote logs --since 6h --limit 500
outfit remote logs -f                   # follow, until you interrupt it
```

Instances ship two logs to CloudWatch: the inference engine's own output, and
the boot log covering everything that runs before the engine starts (the
weights download, credential setup). `logs` reads them from CloudWatch with
your AWS credentials, not from the instance — so **the logs outlive the
instance**. An environment that is stopped, or whose instance terminated hours
ago, still has readable logs, which is exactly when `status` and `metrics` have
nothing left to tell you.

Use `--source boot` when a start failed and the engine log is empty: the
failure happened before the engine existed, so only the boot log saw it.
`--source all` interleaves both in time order.

Output is oldest first, one event per line with its local timestamp. When more
than one instance or both sources are in play, each line is prefixed with
`source/instance`; with a single origin that prefix is left off. `--format
json` emits the same events as an array for scripting.

| Flag | Meaning |
| ---- | ------- |
| `--source` | `engine` (default), `boot`, or `all` |
| `--since` | How far back to look, as a duration (default `1h`) |
| `--limit` | Most events to print, keeping the most recent (default 200) |
| `--instance` | Only this instance's events |
| `-f`, `--follow` | Keep printing new events until interrupted |
| `--format` | `text` (default) or `json` |

If more events match than `--limit`, the earlier ones are dropped and the count
is reported — raise `--limit` to see them.

Reading logs needs one permission beyond the usual endpoint access:
`logs:FilterLogEvents` on `/cloud-vm-llm/*`. Without it the command says so
rather than reporting an empty log.

If it reports that no log group exists, the control plane was deployed before
log shipping existed; `outfit remote bootstrap` re-deploys it and the next
instance will ship. Logs already lost with a terminated instance can't be
recovered — only what's shipped from then on.

## Creating an endpoint: `deploy`

`outfit remote deploy` creates an **environment** on the bootstrapped control
plane and tells it what to serve. It reads the Outfit and its preset:
`PROVIDER` picks the engine (so the file that runs a model locally under
[`outfit serve`](serve.md) deploys the same model remotely), and `REMOTE` names
the environment — the committed link between the Outfit and its deployment:

```dockerfile
PROVIDER llamacpp        # the engine to run: llamacpp or vllm
ALIAS    qwen3.6-27b     # the name your agent asks for — and the name served
CONTEXT  131072
PRESET   ./preset.ini    # the model and its flags
REMOTE   qwen3.6-27b     # the environment deploy creates and registers
```

Deploy discovers the control plane from the bootstrap stack's outputs, then
provisions the environment's own Elastic IP, API key, ingress rule and state,
registers it under `~/.config/outfit/remotes/<env>/`, and stores what to serve.
Everything the endpoint sets itself — host, port, where the weights live, the
API key, the context size, the alias — is dropped from the preset, so one
preset works both locally and remotely without edits.

Who may reach the instance is **per environment**: `--allowed-cidr` sets it,
defaulting to your public IP as a `/32` on first deploy; later deploys leave
ingress alone unless you pass it again. Deploying over an environment that is
already registered, or whose instance is live, requires `--overwrite` — a
redeploy never silently clobbers a running instance.

Deploying doesn't start anything. If the shared bucket doesn't have those
weights yet it fetches them (about 15–20 minutes, entirely on its side) and
says so; wait for that before your first `start`, or the model won't be there.

Switching model, quantisation, or engine is an edit to those two files and one
`deploy` — no redeployment of the infrastructure. A second Outfit naming a
different `REMOTE` gets its own environment, side by side.

```sh
outfit remote deploy --dry-run       # see what would be sent
outfit remote deploy path/to/Outfit  # deploy a different one
outfit remote deploy --overwrite     # redeploy over the existing environment
```

## Flags

| Flag | Meaning |
| ---- | ------- |
| `--timeout` | How long `start` waits for the endpoint (default 15m) |
| `-n`, `--dry-run` | `deploy` only: print what would be sent, without sending it |

`logs` has its own set — see [reading the logs](#reading-the-logs).

## Notes

- Every subcommand takes an optional Outfit path, or a
  [registered alias](alias.md). Given none, it uses the alias `OUTFIT_ALIAS`
  names, and failing that `./Outfit`.
- `deploy` always needs an Outfit — it's the thing being deployed. The others
  fall back to your per-user config.
- `deploy_url` is optional: a config written before `deploy` existed still
  works for `start`, `stop`, and `status`.
- Only a self-hosted engine can be deployed (`llamacpp` or `vllm`). A hosted
  provider has nothing to deploy.

## See also

- [The `Outfit` file](../outfit-file.md) — including `REMOTE`
- [`outfit serve`](serve.md) — the same Outfit, run on your own machine
- [`outfit apply`](apply.md) — point your agent at the endpoint
