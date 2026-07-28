# outfit add

Point your coding agent at a provider and model. Settings are deep-merged into
the agent's config — other providers, your theme, even your comments stay
exactly where you left them.

```sh
outfit add --provider <name> [--model-family <family>] [--model <id>]
           [--alias <name>] [--context <size>] [--output <size>]
           [--base-url <url>]
```

## Examples

```sh
# A whole family from OpenRouter (key from .env or the environment)
outfit add -p openrouter -f deepseek-v4

# A local Ollama model (no key required)
outfit add -p ollama -f llama

# Claude on AWS Bedrock (uses your AWS credentials)
outfit add -p amazon-bedrock -f claude

# Any OpenAI-compatible endpoint
OPENAI_API_KEY=sk-... \
  outfit add -p openai-compatible -m my-model --base-url https://my-endpoint/v1

# Pin a specific default model within a family
outfit add -p openrouter -f deepseek-v4 -m deepseek/deepseek-v4-pro

# Record the context window and cap the output tokens
outfit add -p llamacpp -m my-model -c 128k -o 32k
```

## Flags

| Flag | Meaning |
| ---- | ------- |
| `-p`, `--provider` | Provider name — see [`outfit list`](list.md). Required. |
| `-f`, `--model-family` | Family to add; brings all its models, its default becomes the default model |
| `-m`, `--model` | The provider-native model id to add or pin as the default |
| `-a`, `--alias` | Friendly name for the model — the key your agent shows |
| `-c`, `--context` | Context window; `128k`, `1m`, `200000`, even `128 K tokens` all work |
| `-o`, `--output` | Max output tokens, same format; defaults to a quarter of the context |
| `-u`, `--base-url` | Override the provider's API base URL (or set `OUTFIT_BASE_URL`) |
| `-H`, `--harness` | Which harness to configure (or set `OUTFIT_HARNESS`) |
| `--providers` | Path to a custom catalogue — see [`outfit init-providers`](init-providers.md) |

## Notes

- You need at least one of `--model-family`, `--model`, or `--alias` alongside
  the provider.
- API keys are read from a `.env` beside the `Outfit` — or, for `outfit add`,
  which has no Outfit, from a `.env` in the current directory — then your
  environment, and
  never written anywhere they'll leak. A provider that requires a key tells you
  which variable to set.
- On opencode, `add` sets the chosen model as the default. Pi has no
  default-model setting, so `add` tells you which model to pick with `/model`.
- `--output` needs `--context`, and cannot exceed it.

## See also

- [`outfit remove`](remove.md) — the inverse
- [`outfit apply`](apply.md) — the same thing, driven by an `Outfit` file
- [`outfit show`](show.md) — what the agent has configured now
