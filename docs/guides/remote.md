# Deploy to a cloud GPU

A model too big for your laptop runs on a GPU in the cloud, from the **same
`Spinloop` file** you would use locally — and the endpoint is a machine that
stops when you do. While it is idle it is *stopped* (no GPU billing; a start
re-wakes it in minutes), and it is *terminated* once it has been stopped long
enough. Forgetting to stop it is not a disaster; stopping it is still the
difference between minutes and hours of GPU time.

## The one-time setup

Two steps, per AWS account:

```sh
spinloop remote bootstrap   # deploy the control plane (like `cdk bootstrap`)
spinloop remote bake        # bake the engine AMIs; waits (~20-40 min)
```

`bootstrap` prints a plan — account, region, resources, cost — and confirms
before deploying. It creates no instance and no environment; those come from
`deploy`. `bake` builds one AMI per engine (driver plus engine, no model) and
waits until they are available, so `deploy` can go when it returns. Re-bake
only when an engine version or the driver changes.

Both run on the administrator's ambient AWS credentials and need Node 22 plus
a package manager. `spinloop remote auth --store` then keeps a day-to-day
credential in this machine's OS keystore so routine commands outlive SSO
log-ins.

## Deploy a model

The Spinloop says what the environment serves; `--env` names the environment —
a machine-local choice that stays out of the file:

```sh
spinloop remote deploy --env qwen3.6-27b
```

Deploy reads the Spinloop and its preset — `PROVIDER` picks the engine, so the
file that runs a model locally under [`spinloop serve`](local-serving.md)
deploys the same model remotely — provisions the environment (Elastic IP, API
key, ingress), registers it under `~/.config/spinloop/remotes/<env>/`, and
stores what to serve. A redeploy over a registered or live environment needs
`--overwrite`; it never silently clobbers a running instance. By default only
your own IP may reach the instance (`--allowed-cidr` changes that).

## The usual flow

```sh
spinloop remote start --env qwen3.6-27b        # boots it (~10 min from cold)
eval "$(spinloop remote start --env qwen3.6-27b --print-env)"   # ...and exports
                                               # OPENAI_BASE_URL + OPENAI_API_KEY
spinloop harness apply --env qwen3.6-27b       # point your agent at it
spinloop harness open --env qwen3.6-27b        # work
spinloop remote pause --env qwen3.6-27b        # done for now: stopped, re-wakeable
spinloop remote stop --env qwen3.6-27b         # done for good: terminated
```

Every command that acts on an endpoint selects it with `--env <name>` — the
flag is **required**, because half of these subcommands start, stop, or
terminate a cloud instance, and an instance nobody named is not one to act on.
A command given no `--env` fails naming the flag and listing the environments
you have registered. `start` prints progress on stderr, and the export lines
for `eval` only with `--print-env`, so a plain `start` leaves stdout empty.

Useful along the way:

```sh
spinloop remote status --env qwen3.6-27b   # up? healthy? where? how long idle?
spinloop remote logs --env qwen3.6-27b     # readable even after it's gone
spinloop remote keep 4h --env qwen3.6-27b  # the idle sweep won't touch it for 4h
spinloop remote restart --env qwen3.6-27b  # fresh engine, same address
```

Work office hours? Let the cloud start and stop it for you:

```sh
spinloop remote schedule set --env qwen3.6-27b \
  --start "0 8 * * 1-5" --stop "0 18 * * 1-5" --timezone Europe/London
```

It's up and warm by the time you sit down, and paused when you leave. See
[`spinloop remote`](../commands/remote.md#starting-and-stopping-on-a-schedule).

## From another machine

The environment is registered per user and per machine, so two machines that
share one (each with its `remote.json` in the registry) can work the same
endpoint. Deploy from one, then on the other — with **no Spinloop at all**:

```sh
spinloop harness open --env qwen3.6-27b
```

The environment's runner becomes the provider, its served model the model, its
context size the window — read live, so a later redeploy is picked up
automatically rather than re-copied by anyone.

## Where next

- [`spinloop remote`](../commands/remote.md) — every subcommand, in full
- [Run a fleet](fleet.md) — remote environments as fleet nodes, alongside daemons
- [Deploying your own control plane](https://github.com/spinloop-ai/spinloop/tree/main/remote)
  — the AWS project behind the command, for the curious
