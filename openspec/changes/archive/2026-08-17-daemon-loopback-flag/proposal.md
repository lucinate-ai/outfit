## Why

`outfit daemon`'s control API binds `:4242` (every interface) by default, and
that bind is refused without a token — so a machine that wants a local-only
daemon must spell out `--api-addr 127.0.0.1:4242` to get the safe bind that
needs no token. That is the common case (the cloud's own instances run loopback
behind their gateway), and it deserves a one-flag spelling.

## What Changes

- `outfit daemon` gains a boolean `--loopback`/`-l` flag: the control API
  listens on `127.0.0.1:4242` — identical to `--api-addr 127.0.0.1:4242` —
  which needs no bearer token.
- Giving `--loopback` together with an explicit `--api-addr` fails, naming the
  conflict, instead of one silently winning (the same rule the two token
  sources already follow).
- The flag is offered by bash/zsh/powershell completion for `outfit daemon`.
- Usage text, README and the docs naming the default bind mention the
  shorthand.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `daemon-api`: the API-exposure requirement gains the loopback shorthand for
  the listen address and its conflict rule with an explicit `--api-addr`.

## Impact

- Code: `cmd/outfit/serve_daemon.go` (flag + resolution in `cmdDaemon`),
  `cmd/outfit/complete.go` (completion table), `cmd/outfit/main.go` (usage
  line), `internal/daemon` (a `LoopbackAPIAddr` constant beside
  `DefaultAPIAddr`).
- Tests: `cmd/outfit` for the flag's resolution and conflict, plus the
  completion-surface coverage.
- Docs: README daemon section, `docs/commands/serve.md`, `docs/http-api.md`,
  and the prose in `docs/openapi.yaml`. No API surface changes, so the OpenAPI
  contract test is untouched.
