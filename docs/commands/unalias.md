# outfit unalias

Drop a name registered with [`outfit alias`](alias.md). The `Outfit` file it
pointed at is left alone — only the name goes away.

```sh
outfit unalias qwen3.6-27b
```

## Notes

- Takes exactly one name; see `outfit alias --list` for what's registered.
- With [tab completion](completion.md) set up, `outfit unalias <TAB>` offers
  exactly the names you have.

## See also

- [`outfit alias`](alias.md) — register or re-point a name
