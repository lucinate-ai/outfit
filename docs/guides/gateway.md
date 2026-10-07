# Serve the fleet as a gateway

A fleet of [daemons](daemon.md) and [environments](cloud.md) is many
addresses. `spinloop gateway` puts one in front of them: an
OpenAI-compatible endpoint where each request is answered by the fleet's own
selector, and a stopped node is started — or the request refused — the way the
fleet file's wake policy says. A machine running agents then needs nothing but
a URL and one token.

```sh
spinloop gateway                    # serves ./fleet.yaml on :4000
spinloop gateway --listen 0.0.0.0:4000
spinloop gateway --api-token-file /run/secrets/gateway
```

It runs in the foreground, the way `serve` does: it holds the fleet file it
serves, and a signal shuts it down cleanly. On startup it resolves the file
and its own token, and a `tokenEnv` variable set nowhere fails here rather
than surfacing later as a per-request 401.

## What it answers

| Path | Meaning |
| ---- | ------- |
| `GET /health` | That the gateway is up — touches no node; how you tell gateway-down from fleet-down |
| `GET /v1/models` | What a request can reach: what running nodes serve, plus what a woken stopped node would start with |
| `POST /v1/chat/completions` | Routed to the node serving the request's `model` |
| `POST /v1/completions` | The same, for the completions endpoint |
| `GET /v1/fleet` | The fleet's topology — what the [orchestrator](work-items.md) reads |

The body goes out unmodified and streamed replies flush as they are produced.
The caller's authorisation never travels past the gateway: the engine is
reached with the key its fleet entry names, and the reply the engine gives is
the reply the caller gets — no retry on another node; an upstream failure
names the node.

## Waking a node

When no running node serves the model and [waking is allowed](../fleet-file.md#waking)
for at least one candidate, the gateway starts one and holds the request until
its engine answers, bounded by `--wake-timeout` (default 5m) — a timeout fails
the request and leaves the engine running, so a slow load is not thrown away.
Concurrent requests for the same model wake at most one engine: a request that
arrives mid-wake joins the one already under way, which is what keeps a burst
from booting — and billing for — more than one cloud instance.

A request nothing may serve fails without starting anything, naming the node
that would have woken and the `spinloop fleet start <node>` command that would
start it.

## Point an agent at it

A `gateway` section in the fleet file names the address and the variable
holding the token:

```yaml
# fleet.yaml
gateway:
  url: http://gateway.internal:4000
  tokenEnv: GATEWAY_TOKEN
```

```sh
spinloop code -O=./Spinloop --fleet ./fleet.yaml
```

The launch is pointed at the gateway rather than a node, and because the
gateway resolves the model per request, a launch through one needs no
Spinloop at all — `spinloop code --fleet ./fleet.yaml` is enough. The agent
holds exactly the gateway's one credential; the node tokens and engine keys
live with the gateway.

## The gateway's token

Callers present it as a bearer token on every request. The same three
sources as the daemon's token — `--api-token-file`, `SPINLOOP_API_TOKEN`,
`--api-token` — and giving two at once is an error. A non-loopback listen with
no token refuses to start; the default `:4000` binds every interface, which is
why the token is not optional there.

## Where next

- [`spinloop gateway`](../commands/gateway.md) — the full reference
- [Work a backlog](work-items.md) — one-shot agents against the gateway, at the fleet's declared pace
- [The `Spinloop` file](../spinloop-file.md#running-the-model-on-another-machine-you-own) — the `fleet` and `gateway` fields
