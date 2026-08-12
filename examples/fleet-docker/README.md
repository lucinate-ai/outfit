# A fleet you can actually run

Three machines' worth of `outfit daemon`, on your laptop, in containers. No
GPUs, no cloud, no model downloads — so you can see what
[`outfit fleet`](../../docs/commands/fleet.md) does before setting up any real
hardware.

```sh
cp .env.example .env
docker compose up -d --build

# from this directory, with the tokens exported
set -a && . ./.env && set +a

outfit fleet status --fleet ./fleet.yaml
outfit fleet start studio --fleet ./fleet.yaml
outfit fleet metrics -w --fleet ./fleet.yaml
```

```
NODE     STATE         SERVING
studio   running       llamacpp  org/fake-model  (up 1m 4s)
gpu-box  idle
laptop   idle
```

Tear it down with `docker compose down -v`.

## What is real and what is not

**Real**: each node runs the actual `outfit daemon` from this repository,
serving its control API over the network with bearer-token auth. It supervises
its engine as a real child process, captures its logs, and reports it as
`crashed` if it dies. `outfit fleet` talks to all three over HTTP exactly as it
would to real machines.

**Not real**: the engine. Instead of `llama-server` there is a
[`llama-server` shim](shim/llama-server) that starts
[Imposter](https://imposter.sh)'s native engine, which serves a canned
`/health` and a `/metrics` in llama.cpp's Prometheus dialect. So the token
counters you see are genuinely scraped and parsed by outfit — they are just
always the same numbers, and nothing is inferring anything.

That trade is deliberate: what is being demonstrated (and tested) is the fleet
control path, not inference.

**Also real**: routing. Each node's engine binds `8080` inside its container and
is published on a different port outside (`18080`–`18082`), which its daemon has
no way to know — so `fleet.yaml` declares a per-node `engine:` block, and that is
the case those blocks exist for. `outfit fleet route` resolves the published port
and the endpoint it names genuinely answers. What you cannot do here is get a
useful reply out of the agent: the fake engine serves `/health` and `/metrics`
and nothing else.

There is only one Outfit here, and it belongs to the client:
[`client/Outfit`](client/Outfit). The nodes hold nothing — their daemons start
with no arguments and no files, and run whatever a start request tells them to.
That is why the container's `CMD` is a bare `outfit daemon`.

`studio`'s engine is also **gated**. Its `fleet.yaml` entry names
`STUDIO_ENGINE_KEY`, which lives only on this side: the client sends it when it
starts the engine, and uses the same value to talk to it. Nothing is kept in
step between two ends, because only one end holds it. The node will tell you a
key is required and never what it is, and the key reaches the engine as a file
path — `docker compose exec studio ps ax` shows `--api-key-file`, not the key.

## Things worth trying

```sh
# Which node would a harness launch pick? (Changes nothing.)
outfit fleet route ./client/Outfit

# Actually launch an agent against the fleet, waking a node if none is serving.
outfit harness ./client/Outfit

# A node that goes away: the row degrades, the rest keep reporting, exit 0.
docker compose stop gpu-box
outfit fleet status --fleet ./fleet.yaml

# A wrong token reads `unauthorized`, not `unreachable` — the box is up, the
# credential is wrong.
STUDIO_TOKEN=nope outfit fleet status --fleet ./fleet.yaml

# Kill an engine and watch the node report `crashed`, then bring it back.
docker compose exec studio sh -c 'kill -9 $(pgrep imposter-go)'
outfit fleet status --fleet ./fleet.yaml
outfit fleet start studio --fleet ./fleet.yaml
```

## It is also the integration test

`./run-tests.sh` drives this same stack and asserts the behaviours above. CI
runs it on every pull request, which is the point: an example that is exercised
cannot quietly stop working.

```sh
./run-tests.sh          # up, assert, tear down
./run-tests.sh --keep   # leave the stack running to poke at
```

## How it fits together

| File | What it is |
| --- | --- |
| `compose.yaml` | Three nodes. Each needs a token, because the daemon refuses to listen on a non-loopback address without one. |
| `fleet.yaml` | What `outfit fleet` reads. Names each node's token by *variable name* — no secrets in the file. |
| `Dockerfile` | Builds outfit from this working tree, adds the Imposter engine and the shim. |
| `shim/llama-server` | Stands in for the engine binary. Execs the Imposter engine **directly**, so the daemon supervises it as its own child. |
| `engine/` | What the fake engine serves: `/health`, and a `/metrics` outfit can parse. |
| `client/Outfit` | What a *client* wears to use the fleet: a model, and a `FLEET`. The nodes hold no Outfit at all. |

Two details that are easy to get wrong, and matter:

- **The shim execs the engine binary, not `imposter up`.** The CLI wrapper
  exits 0 when its child dies, which the daemon would correctly record as a
  clean stop — so a crash test would pass while testing nothing.
- **`OUTFIT_CONFIG_DIR` is set in the image.** A container has no useful
  `$HOME`, and outfit's config directory would otherwise resolve somewhere
  unhelpful. The cloud instance's daemon pins it for exactly the same reason.

## See also

- [`examples/fleet/`](../fleet/) — a `fleet.yaml` for machines you actually own
- [`docs/commands/fleet.md`](../../docs/commands/fleet.md)
- [HTTP Control API](../../docs/http-api.md)
