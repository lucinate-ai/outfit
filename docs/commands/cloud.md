# spinloop cloud

Run a model too big for your laptop on a GPU in the cloud, from the same
[`Spinloop` file](../spinloop-file.md) you'd use locally — and only pay for it
while you're using it.

```sh
spinloop cloud bootstrap  # once per account: deploy the control plane
spinloop cloud auth       # store or report the credential this machine signs with
spinloop cloud bake       # bake the runner AMI(s) an environment runs from
spinloop cloud deploy     # create an endpoint (environment) and tell it what to serve
spinloop cloud start      # boot it; with --print-env, prints the exports your agent needs
spinloop status --env <name>     # is it up? is it healthy?
spinloop logs --env <name>       # what did it say? (readable after it's gone)
spinloop cloud pause      # stop it now; a later start re-wakes it
spinloop cloud restart    # fresh engine, same address: stop it and wake it again
spinloop cloud keep 4h    # prevent the idle sweep from stopping it for 4 hours
spinloop cloud schedule   # start and stop it on cron schedules (set, show, clear)
spinloop cloud stop       # terminate it now, rather than waiting for the idle timer
```

The endpoint is the one
[`remote/`](https://github.com/spinloop-ai/spinloop/tree/main/remote) in this
repository deploys: a GPU instance that exists only while you're using it. When it goes idle it is
stopped (so a re-wake is fast), and terminated once it has been stopped long
enough that the pause is over.

## Bootstrapping the account

Before any endpoint can run, the account-level control plane has to
exist — much like `cdk bootstrap`. `spinloop cloud bootstrap` does it once per
account: it downloads the `remote/` CDK project (version-matched to your binary)
and deploys the control plane — the EC2 Image Builder pipelines, the lifecycle
Lambdas, and the shared weights bucket, roles and VPC — publishing them as
CloudFormation outputs that `spinloop cloud deploy` discovers later. It bakes
**no** AMIs — that is the separate `spinloop cloud bake` step below.

```sh
spinloop cloud bootstrap                 # shows a consent plan, then deploys
spinloop cloud bootstrap --dry-run       # print the plan and do nothing
spinloop cloud bootstrap --package-manager npm  # use npm instead of pnpm
```

Before deploying, bootstrap prints a plan — the target account and region, the
control-plane resources, the cost, and the exact commands — and asks you to confirm
(`--yes` skips the prompt). It creates **no** Elastic IP or instance and **no**
environment; those come from `spinloop cloud deploy`. Re-running is safe: it
updates the control-plane stack and doesn't touch any live instance. It needs Node 22, a
Node package manager, AWS credentials, and enough GPU vCPU quota for a later
launch.

By default bootstrap uses `pnpm` and falls back to `npm` when `pnpm` isn't on the
path, logging which one it picked. To pin the choice, pass `--package-manager`
(`pnpm` or `npm`) or set `SPINLOOP_CLOUD_PACKAGE_MANAGER`; the flag wins over the
env var. A pinned manager that isn't installed fails the preflight rather than
falling back. `spinloop cloud bake` honours the same flags.

Bootstrap stamps the control plane with the version of the `spinloop` that deploys
it, and every control plane response carries that version. When a later `cloud`
command sees a version different from its own, it prints one warning to stderr
naming both, and carries on; re-run `spinloop cloud bootstrap` to bring the
control plane up to date. No warning appears for a control plane deployed before
this was added, or when either side is a development build.

## Baking the AMIs

Each engine runs from a baked AMI (driver + engine, no model).
`spinloop cloud bake` starts a bake for each runner you name — both `llamacpp`
and `vllm` when you name none — and **waits** until the AMI(s) are available, so
the command returns at the point `spinloop cloud deploy` can go:

```sh
spinloop cloud bake                # bake both engines' AMIs; waits (~20-40 min)
spinloop cloud bake llamacpp       # bake one engine's AMI
spinloop cloud bake --no-wait      # return once the bakes are queued
```

Bakes are slow (a builder instance runs for 20–40 minutes) and independent of
the weight seed, so `--no-wait` lets them run in parallel — the command prints
how to check on them. A bake deploys nothing: it needs the control plane's
Image Builder pipelines, so if the control plane isn't deployed it fails telling
you to run `spinloop cloud bootstrap` first. Re-bake only when the engine
version or the driver changes; the model is **not** baked in, and a new AMI is
picked up automatically once it is available.

## The usual flow

```sh
eval "$(spinloop cloud start --env qwen3.6-27b --print-env)"   # boots it (~10 min
                                        # from cold) and sets OPENAI_BASE_URL and
                                        # OPENAI_API_KEY
spinloop harness apply --env qwen3.6-27b         # point your agent at it
spinloop harness open --env qwen3.6-27b  # work
spinloop cloud pause --env qwen3.6-27b  # done for now: stopped, re-wakeable with start
spinloop cloud stop --env qwen3.6-27b   # done for good: terminate it
```

Every command that acts on an endpoint — `start`, `status`, `metrics`, `logs`,
`pause`, `restart`, `keep`, `schedule`, `stop` — selects it with the same `--env <name>`
flag; with no flag they use the `default` environment. `start` prints its
progress on stderr, and the export lines for `eval` only with `--print-env`, so
a plain `start` leaves stdout empty for other uses.

Forgetting `stop` is not a disaster — after a spell with no requests the
endpoint is **stopped** (no more GPU billing; a start re-wakes it) and then
**terminated** once the retention passes — but it's the difference between
minutes and hours of GPU time.

## Pointing at your endpoint

`spinloop cloud` needs the endpoint's control URLs, which its deployment prints.
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

`base_url` is the endpoint's own address, and it's optional — `cloud` doesn't
need it, since `start` and `status` report the address themselves. It's there
for [`spinloop harness apply`](harness.md#spinloop-harness-apply): a Spinloop for a cloud endpoint can leave out
`BASEURL` and let apply take the address from here, so the address stays with
the deployment that owns it. A `BASEURL` in the Spinloop wins if you set one.

Name the environment with the `--env` flag:

```sh
spinloop status --env <name> --env qwen3.6-27b-prod
```

The flag selects a **named environment** from the
per-user registry at `~/.config/spinloop/clouds/<name>/cloud.json`. This keeps
deployment state per-user and per-machine: two projects name two environments
without clobbering, and the Spinloop carries none of the URLs at all.
`spinloop cloud deploy` registers an environment for you; you can also
create one by hand. A name is a plain identifier — `--env ./cloud.json` fails,
saying an environment name has no path.

**`--env` is required.** There is no environment a command falls back to: half
of these subcommands start, stop or terminate a cloud instance, and an instance
nobody named is not one to act on. A command given no `--env` fails naming the
flag and listing the environments you have registered, so the next thing to
type is in the error. `default` is an ordinary name — call an environment that
if you like, and pass `--env default` to use it.

A command may also be given a Spinloop path (or a [registered
alias](alias.md), or a URL): its `ENV` lines and the `.env` beside it are read
before the command signs its AWS calls, so credentials, region and
`SPINLOOP_CLOUD_*` overrides can travel with the Spinloop. The Spinloop does
not select the environment — that is the flag's job alone.

`spinloop cloud env --env <name>` fetches a running endpoint's credentials
(`export OPENAI_BASE_URL`/`export OPENAI_API_KEY`, safe to `eval`) without
booting it. It also reports what is deployed to the environment — runner,
served model, context size — whenever something is: this is what lets
[`spinloop harness open --env <name>`](harness.md#launching-with-no-spinloop-at-all)
configure the harness from a deployed environment with no Spinloop at all. It
appears once the control plane has been redeployed with `spinloop cloud
bootstrap`; an older control plane simply omits it, and `spinloop harness open
--env` with no Spinloop fails naming that as the fix.

## Listing environments

```sh
spinloop cloud ls
```

lists each registered environment with its base URL and region, marking any
whose `cloud.json` is missing or unreadable. It contacts no endpoint.

Requests are signed with an AWS credential resolved per region — explicit
environment credentials or a named profile first, then the stored
control-plane credential from `spinloop cloud auth --store`, then the usual
chain of config files, SSO sessions, and instance metadata; see
[credentials](#credentials). The endpoint's URLs require it. Beyond invoking
those URLs, the only extra permission it wants is for
[reading logs](#reading-the-logs), which talks to CloudWatch rather than to an
endpoint.

## Credentials

Every `spinloop cloud` command signs its requests with an AWS credential,
resolved for the region the command targets, in this order:

1. Explicit credentials in the process environment (`AWS_ACCESS_KEY_ID` and
   friends) or an explicit `AWS_PROFILE` — a deliberate per-process choice, so
   they always win.
2. The stored control-plane credential, when one is stored for the region —
   it outlives SSO log-ins, which is the point of it.
3. The standard chain: shared config files, SSO sessions, instance metadata.

`spinloop cloud auth` manages the stored credential on this machine:

```sh
spinloop cloud auth                # what is stored (no AWS call)
spinloop cloud auth --store        # store one for the region; rotates it when stored
spinloop cloud auth --store --region ap-southeast-2
spinloop cloud auth --clear        # remove it and delete the access key
```

`--store` creates an access key for the control-plane user the stack makes
(`cloud-vm-llm-remote-cli`) and keeps it in this machine's OS keystore —
Keychain on macOS, Credential Manager on Windows, the Secret Service on Linux.
Where no keystore is reachable it keeps it in an owner-only file under the
spinloop config directory instead, and every report says which store it used;
`SPINLOOP_CLOUD_KEYSTORE=file` selects the file store even where a keystore
is, for a machine whose keystore is locked or unreachable. The secret is never
printed. The key is scoped to day-to-day control only: invoke the control
URLs, read the instance logs, discover the stack, price an instance, and manage
this user's own access keys — nothing that provisions.

A first store runs on the administrator's ambient credentials and verifies the
new key resolves to the caller's account before storing it; a key that
resolves elsewhere is deleted, not kept. When a credential is already stored,
`--store` rotates instead: it creates the replacement with the stored key
alone — no other AWS credential needed — swaps the entry, and deletes the
superseded key on the AWS side. Rotate roughly every 90 days, like any
long-lived key.

`--clear` removes the local entry and deletes the access key on the AWS side
with the stored credential, so a cleared key does not linger in the account.
If the AWS-side deletion cannot be made, the local entry is still removed and
the failure reported.

A control plane deployed before this capability has no control-plane user, so
`--store` against it fails naming `spinloop cloud bootstrap` — re-run it to
add the user, then store.

`bootstrap` and `bake` never consult the stored credential: they provision the
control plane itself and run on the administrator's ambient credentials. The
fleet's operations on cloud environments sign through the same resolution, so
a stored key covers them too.

## Checking on an endpoint

```sh
spinloop status --env <name>                   # is it up, is it healthy, where is it
spinloop metrics --env <name>                  # what is it doing — tokens, GPU, CPU, RAM
spinloop metrics --env <name> -w               # the same, redrawn every 60 seconds
```

`metrics` draws its resource series as **gauge** format by default: the
current reading per series as a filled progress gauge, coloured green at or
below 80%, yellow to 90%, red above. `--format=bar` draws each series as a
sparkline of the last 10 minutes instead, taken by the on-instance daemon at
its sampler's cadence while the engine ran, with only the latest point
coloured; a daemon that predates the history, or an engine with no reading
yet, falls back to the gauge drawing of the current reading, so the bar
output degrades rather than goes blank. `--format=table` is a key-value
table, and `--format=json` is the raw reply. A stopped engine keeps its
readings: the sparkline runs to the stop, ending at it.

While a start is under way, `status` shows **`starting`** for the endpoint,
whichever machine began the start, until the instance is running. Once it is
running but still loading the model it reads `running` and not ready, as
before. Two starts for one endpoint never launch two instances: the second is
told "another start is in progress" and retries on its own, so running
`start` twice, or from two machines, is safe. A start that is waiting for GPU
capacity is between attempts, not in progress, and `status` does not show it.
Stopping an endpoint during a start is always allowed; the start ends and
reports that the instance was stopped, and a client that keeps retrying will
wake it again.

Both report **`active`** — how long since the endpoint's engine last did
any work. It comes from the activity the on-instance daemon tracks, so it is
one answer decided on the box rather than something each command re-derives
from raw counters. `status` asks the daemon alongside the health check it
already makes, so it is no slower than before.

The figure is left out rather than guessed at whenever there is nothing to
report: an engine that has not yet served anything, a daemon that cannot be
reached, and — on the cloud — an instance that is **stopped**, since reaching
the daemon needs a running box. (That last one differs from a stopped *engine*
on a machine you run yourself, which does still report; see
[`spinloop fleet`](fleet.md).) In none of these cases does the rest of the
report change.

It appears once the control plane has been redeployed with `pnpm run deploy`
(the `run` matters — plain `pnpm deploy` is pnpm's own built-in command). An
older control plane simply omits it, and the commands print what they always
did.

## Keeping an instance alive

```sh
spinloop cloud keep 4h                          # retain for 4 hours from now
spinloop cloud start --keep 2h                  # start and retain for 2 hours
```

`keep` sets the `Retain-Until` tag on the environment's instance, preventing
the idle sweep from stopping or terminating it before the deadline. It is a
minimum runtime — once the deadline passes, normal idle checking resumes. A
manual `pause` or `stop` still takes effect: the tag guards against accidental
death, not deliberate shutdown.

`start --keep DURATION` sets the same tag at wake time, so the instance is
retained from the moment it boots. Useful when you know you need the instance
for a fixed period (e.g. overnight debugging) and don't want to type `keep`
afterwards.

The deadline appears in `status` output when the tag is present, so you can
see how long the instance is protected for.

It requires a control plane with the update Lambda (bootstrap with a recent
version, or re-bootstrap).

## Starting and stopping on a schedule

```sh
spinloop cloud schedule set --env dev \
  --start "0 8 * * 1-5" --stop "0 18 * * 1-5" --timezone Europe/London
spinloop cloud schedule show --env dev    # the schedules, and when each action next runs
spinloop cloud schedule clear --env dev   # remove them all
```

A schedule is a five-field cron expression (minute, hour, day of month, month,
day of week) with an action, `start` or `stop`. `set` takes one or more
`--start` and `--stop` flags and a single `--timezone` (an IANA name such as
`Europe/London`, default `UTC`) that applies to all of them. It replaces the
environment's whole list, so what you pass is exactly what runs afterwards.
Expressions are checked before anything changes: a bad one is refused, naming
it, and the earlier schedules carry on. Day of month and day of week cannot
both be set in one expression, and names such as `MON` are not accepted;
use numbers (`0` or `7` is Sunday).

The control plane stores and runs the schedules, so they fire whether or not
any of your machines is on. Times follow the zone's clock, including daylight
saving: `0 8 * * *` in `Europe/London` is 08:00 local all year.

- A scheduled start does what `start` does, including the weights check. If the
  instance is already running it is left alone. If it cannot start (no GPU
  capacity, weights not seeded), the reason is in the start Lambda's log and
  nothing retries until the next firing.
- A scheduled stop pauses the instance, as `pause` does, so a later start
  re-wakes it. If the instance is being kept (`keep`, `start --keep`) the stop
  is skipped.
- `show` prints one line per schedule, then `next start` and `next stop` as UTC
  times. An action with no schedule has no line.

It requires a control plane with the schedule Lambda: a deployment that predates
it fails with a message to re-run `spinloop cloud bootstrap`. The URL is the
`schedule_url` in the environment's `cloud.json`, or
`SPINLOOP_CLOUD_SCHEDULE_URL`.

## Restarting the engine

```sh
spinloop cloud restart             # fresh engine, same endpoint
spinloop cloud restart --force     # skip the graceful engine stop
```

`restart` stops the instance the way `pause` does — without terminating it, so
the boot disk and its weights survive — and then wakes it, blocking until the
model serves again. Because the box is only stopped, the address does not
change and the re-wake loads the weights already on disk: the fastest way back
to a fresh engine. Nothing about what the endpoint serves changes; that is
[deploy](#creating-an-endpoint-deploy)'s job.

The stop asks the on-instance daemon to shut the engine down politely first.
When the engine or its daemon is wedged and will not answer, `--force` skips
that step and takes the box down directly — the EC2 stop does not go through
the daemon, so it still lands. It also kills whatever the engine is doing,
which is why the default stays polite.

With the instance already stopped, `restart` just wakes it — the same as
`start`. Like `start`, it takes a `--timeout` (default 15m) and prints the
endpoint's base URL when it serves, so you can check the address is unchanged.

## Reading the logs

```sh
spinloop logs --env <name>                      # the last hour of engine output
spinloop logs --env <name> --source boot        # the start-up log, before the engine ran
spinloop logs --env <name> --since 6h --limit 500
spinloop logs --env <name> -f                   # follow, until you interrupt it
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
log shipping existed; `spinloop cloud bootstrap` re-deploys it and the next
instance will ship. Logs already lost with a terminated instance can't be
recovered — only what's shipped from then on.

## Creating an endpoint: `deploy`

`spinloop cloud deploy` creates an **environment** on the bootstrapped control
plane and tells it what to serve. It reads the Spinloop and its preset —
`PROVIDER` picks the engine, so the file that runs a model locally under
[`spinloop serve`](serve.md) deploys the same model remotely — and `--env <name>`
names the environment it creates and registers. The flag is required: a deploy
without it fails, naming the flag and a name to use. The Spinloop says what the
environment serves; the name is a machine-local choice, so it stays out of the
file:

```dockerfile
PROVIDER llamacpp        # the engine to run: llamacpp or vllm
ALIAS    qwen3.6-27b     # the name your agent asks for — and the name served
CONTEXT  131072
PRESET   ./preset.ini    # the model and its flags
```

```sh
spinloop cloud deploy --env qwen3.6-27b
```

Deploy discovers the control plane from the bootstrap stack's outputs, then
provisions the environment's own Elastic IP, API key, ingress rule and state,
registers it under `~/.config/spinloop/clouds/<env>/`, and stores what to serve.
Everything the endpoint sets itself — host, port, where the weights live, the
API key, the context size, the alias — is dropped from the preset, so one
preset works both locally and remotely without edits.

Who may reach the instance is **per environment**: `--allowed-cidr` sets it,
defaulting to your public IP as a `/32` on first deploy; later deploys leave
ingress alone unless you pass it again. Deploying over an environment that is
already registered, or whose instance is live, requires `--overwrite` — a
redeploy never silently clobbers a running instance.

The environment's instances install spinloop — the daemon that hosts the
engine — at boot, and `--spinloop-version` pins the release a fresh boot
installs. Without it a boot installs the latest published release; with a
pin (`1.26.1`; a leading `v` is fine) it installs exactly that. The pin is
environment state, not engine state: it takes effect at the next boot, so a
running instance keeps the daemon it was deployed with.

`--instance-type` names the EC2 instance type the environment's instances
launch as (a family and size separated by a dot, e.g. `g6e.2xlarge`). Like the
pin, it is recorded on the environment at deploy time and applies from its next
**fresh** launch: a re-wake of a stopped instance keeps the type it launched
with, so a changed value takes effect only once the instance is terminated and
launched again. Without it the environment launches as the control plane's
default type.

Deploying doesn't start anything. If the shared bucket doesn't have those
weights yet it fetches them (about 15–20 minutes, entirely on its side) and
says so; wait for that before your first `start`, or the model won't be there.

Switching model, quantisation, or engine is an edit to those two files and one
`deploy` — no redeployment of the infrastructure. A second Spinloop deployed
under a different `--env` name gets its own environment, side by side.

```sh
spinloop cloud deploy --env qwen3.6-27b --dry-run        # see what would be sent
spinloop cloud deploy --env other-env path/to/Spinloop   # deploy a different file
spinloop cloud deploy --env qwen3.6-27b --overwrite      # redeploy over the existing environment
spinloop cloud deploy --env qwen3.6-27b --reseed         # re-fetch weights already in S3
```

Deploy fetches the weights only when they are not in S3 already. `--reseed`
fetches them regardless — for a repo whose files changed under the same name,
or a seed you want to run again. It starts the same ~20-minute seed instance a
first deploy does, and re-downloads the weights, so it is opt-in rather than
something to reach for by habit.

By default deploy stores the engine key the environment was first deployed
with — one generated per environment, held in its secret. To choose or change
it, `--api-key-env VAR` resolves `VAR` from your environment (or the `.env`
beside the Spinloop) and sends the value: the deploy creates or **rotates** the
environment's key, so the old value stops working — and the reply says which
happened, never the value itself.

Deploying several environments this way means running `cloud deploy` once
per Spinloop file. [`spinloop fleet deploy`](fleet.md#deploying-cloud-nodes)
does the same derivation, consent, and registration (`--api-key-env`
included) for every `kind: cloud` node a fleet file names — or a chosen few
— in one command, each from its own resolved Spinloop source.

## Flags

| Flag | Meaning |
| ---- | ------- |
| `--env` | The environment the command acts on, by registered name (required by `deploy`; default `default`) |
| `--print-env` | `start` only: print the export lines (`OPENAI_BASE_URL`/`OPENAI_API_KEY`) to stdout for `eval` |
| `--timeout` | How long `start` or `restart` waits for the endpoint (default 15m) |
| `-F`, `--force` | `restart` only: skip the graceful engine stop and take the instance down directly |
| `--keep` | `start` only: retain the instance until `now + DURATION`, preventing the idle sweep from stopping it |
| `-n`, `--dry-run` | `deploy` only: print what would be sent, without sending it |
| `--reseed` | `deploy` only: re-fetch the weights even if they are already in S3 |
| `--spinloop-version` | `deploy` only: the spinloop release fresh boots install (default: the latest published release) |
| `--instance-type` | `deploy` only: the EC2 instance type the environment's instances launch as (e.g. `g6e.xlarge`); recorded on the environment, applied on its next fresh launch (default: the control plane's default type) |
| `--api-key-env` | `deploy` only: name the environment variable holding the engine key to create or rotate; with no flag the stored key is kept |

`bootstrap` and `bake` have their own too (`--ref`, `--dir`, `--region`,
`--package-manager`, and `--no-wait` on bake) — see their sections above.
`logs` has its own set — see [reading the logs](#reading-the-logs).

## Notes

- `bootstrap`, `bake`, and `auth` take no Spinloop: the control plane and the
  AMIs are shared by every environment, and the stored credential belongs to
  the machine, not a project.
- `deploy` always needs a Spinloop — it's the thing being deployed — and a
  required `--env <name>` for the environment it creates. The others take an
  optional Spinloop path, a [registered alias](alias.md), or a URL, read only
  for its `ENV` lines and `.env` — never to select the environment. Which
  environment a command acts on is its `--env` flag, defaulting to the
  `default` environment.
- `deploy_url` is optional: a config written before `deploy` existed still
  works for `start`, `stop`, and `status`.
- Only a self-hosted engine can be deployed (`llamacpp` or `vllm`). A hosted
  provider has nothing to deploy. `mtplx` is a self-hosted engine too, but it is
  Apple-Silicon-only and has no machine image, so it is not a cloud runner — it
  serves locally and on a [fleet node](fleet.md), never on the cloud GPU.

## See also

- [The `Spinloop` file](../spinloop-file.md) — what the Spinloop serves
- [`spinloop serve`](serve.md) — the same Spinloop, run on your own machine
- [`spinloop harness apply`](harness.md#spinloop-harness-apply) — point your agent at the endpoint
