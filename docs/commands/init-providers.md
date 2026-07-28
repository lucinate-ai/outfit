# outfit init-providers

Write the built-in provider catalogue out as a file you can edit — the starting
point for adding your own providers, families, or models without rebuilding.

```sh
outfit init-providers                 # writes ./providers.yaml
outfit init-providers custom.yaml     # ...or to a path of your choosing
```

Edit it, then point `outfit` at it — the flag wins, then the env var, then the
built-in default:

```sh
outfit list --providers ./providers.yaml
OUTFIT_PROVIDERS=./providers.yaml outfit list
```

## Flags

| Flag | Meaning |
| ---- | ------- |
| `-F`, `--force` | Overwrite an existing file |

## Notes

- It refuses to overwrite an existing file unless `--force` is given, so a
  stray run can't destroy a catalogue you've been editing.
- The written file is commented with the schema — providers, families, key
  environment variables, and per-harness settings are all data, not code.

## See also

- [`outfit list`](list.md) — view whichever catalogue is active
