# outfit list

Show the catalogue: every provider `outfit` can configure, the API key each one
needs (if any), which harnesses support it, and its model families with their
default models.

```sh
outfit list
outfit list --providers ./my-providers.yaml   # a custom catalogue
```

This is the *could* view — what's available to configure. For what your agent
*currently has* configured, see [`outfit show`](show.md).

## Flags

| Flag | Meaning |
| ---- | ------- |
| `--providers` | Path to a custom catalogue (or set `OUTFIT_PROVIDERS`) — see [`outfit init-providers`](init-providers.md) |

## Notes

- A provider marked with a required API key won't configure until that
  variable is set in a `.env` next to the tool or your environment.
- Not every provider maps to every harness — the listing names which harnesses
  each supports (AWS Bedrock, for instance, is opencode-only).

## See also

- [`outfit add`](add.md) — configure something you found here
- [`outfit init-providers`](init-providers.md) — customise the catalogue
