# outfit list

Show the catalogue: every provider `outfit` can configure, the API key each one
needs (if any), and which harnesses support it.

```sh
outfit list
outfit list --providers ./my-providers.yaml   # a custom catalogue
outfit list --models                           # add each provider's live models
outfit list --models openrouter                # just one provider's models
```

This is the *could* view — what's available to configure. For what your agent
*currently has* configured, see [`outfit show`](show.md).

The catalogue names providers, not models — models change too often to curate.
`--models` fills that gap on demand: it asks each provider's own endpoint what it
currently serves (OpenRouter, an OpenAI-compatible gateway, a local llama.cpp or
Ollama server, …) and lists the ids you can drop into an `Outfit`'s `MODEL`. A
positional provider name narrows the query to one provider.

## Flags

| Flag | Meaning |
| ---- | ------- |
| `--providers` | Path to a custom catalogue (or set `OUTFIT_PROVIDERS`) — see [`outfit init-providers`](init-providers.md) |
| `--models` | Also fetch each listed provider's current models, live from its endpoint |

## Notes

- A provider marked with a required API key won't configure until that
  variable is set in a `.env` beside your `Outfit`, or in your environment.
- Not every provider maps to every harness — the listing names which harnesses
  each supports (AWS Bedrock, for instance, is opencode-only).
- `--models` is best-effort: it uses the same base URL and key a selection
  would, applies a short timeout, and prints `(none found)` when a provider is
  unreachable or has no queryable endpoint (AWS Bedrock has none). A plain
  `outfit list` makes no network request.

## See also

- [`outfit add`](add.md) — configure something you found here
- [`outfit init-providers`](init-providers.md) — customise the catalogue
