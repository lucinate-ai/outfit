# outfit remote

Run a model too big for your laptop on a GPU in the cloud, from the same
[`Outfit` file](../outfit-file.md) you'd use locally — and only pay for it
while you're using it.

```sh
outfit remote deploy   # tell the endpoint what to serve
outfit remote start    # boot it; prints the exports your agent needs (progress on stderr)
outfit remote status   # is it up? is it healthy?
outfit remote stop     # shut it down now, rather than waiting for the idle timer
```

The endpoint is the one the companion [`cloud-vm-llm`](https://github.com/lucinate-ai/cloud-vm-llm)
project deploys: a GPU instance that exists only while you're using it, and
terminates itself once you stop. `outfit remote` drives it; it doesn't create
it.

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
  "region": "us-east-1"
}
```

Either name it from the Outfit, so a project carries its own endpoint:

```dockerfile
REMOTE ./remote.json    # resolved next to the Outfit, like PRESET
```

…or save it once as `~/.config/outfit/remote.json`, which is used whenever no
Outfit names one — so `outfit remote` works from anywhere.

Requests are signed with **your** AWS credentials (the usual profile, SSO
session, or environment variables), and the endpoint's URLs require it. Outfit
stores no credentials of its own and needs no permission beyond invoking those
URLs.

## Choosing what it serves

`outfit remote deploy` reads the Outfit and its preset, and tells the endpoint
what to load. `PROVIDER` picks the engine, so the file that runs a model
locally under [`outfit serve`](serve.md) deploys the same model remotely:

```dockerfile
PROVIDER llamacpp        # the engine to run: llamacpp or vllm
ALIAS    qwen3.6-27b     # the name your agent asks for — and the name served
CONTEXT  131072
PRESET   ./preset.ini    # the model and its flags
REMOTE   ./remote.json
```

Everything the endpoint sets itself — host, port, where the weights live, the
API key, the context size, the alias — is dropped from the preset, so one
preset works both locally and remotely without edits.

Deploying doesn't start anything. If the endpoint doesn't have those weights
yet it fetches them (about 15–20 minutes, entirely on its side) and says so;
wait for that before your first `start`, or the model won't be there.

Switching model, quantisation, or engine is an edit to those two files and one
`deploy` — no redeployment of the infrastructure.

```sh
outfit remote deploy --dry-run       # see what would be sent
outfit remote deploy path/to/Outfit  # deploy a different one
```

## Flags

| Flag | Meaning |
| ---- | ------- |
| `--timeout` | How long `start` waits for the endpoint (default 15m) |
| `-n`, `--dry-run` | `deploy` only: print what would be sent, without sending it |

## Notes

- Every subcommand takes an optional Outfit path, or a
  [registered alias](alias.md), and defaults to `./Outfit`.
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
