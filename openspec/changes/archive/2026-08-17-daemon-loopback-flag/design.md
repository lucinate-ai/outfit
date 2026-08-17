## Context

`cmdDaemon` (`cmd/outfit/serve_daemon.go`) declares `--api-addr` defaulting to
`daemon.DefaultAPIAddr` (`:4242`, every interface) and hands it to
`daemon.Listen`, which enforces the exposure rule — a non-loopback address
refuses to start without a token (internal/daemon/api.go). The loopback
bind's token-free property already exists; only its one-flag spelling is
missing. The completion mirror in `cmd/outfit/complete.go` is checked by
test, so a new flag belongs in that table. See proposal.md for motivation.

Existing `cmdDaemon` tests always listen on `127.0.0.1:0` and read the bound
address back from the daemon's stderr — never on a fixed port — and this
constraint shapes the testable shape below.

## Goals / Non-Goals

**Goals:**

- `outfit daemon --loopback` / `outfit daemon -l` binds the control API to
  `127.0.0.1:4242` and needs no token.
- Combining it with an explicit `--api-addr` fails, naming both.
- The flag completes and is documented like its siblings.

**Non-Goals:**

- No `--loopback` for `outfit serve --api` (the spec pins the shorthand to
  the daemon; serve keeps `--api-addr` alone).
- No new listen-address concepts in `daemon.Listen` — it keeps taking a
  finished address; the flag is resolved before it.
- No port override (e.g. `--loopback 4243`); the shorthand is one address,
  not a bind profile.

## Decisions

**1. `LoopbackAPIAddr` lives in `internal/daemon`, beside `DefaultAPIAddr`.**
A `const LoopbackAPIAddr = "127.0.0.1:4242"` next to the existing
`DefaultAPIAddr`/`DefaultAPIPort`, so the two default binds (and the port
they share) are discovered and edited in one place. Alternative considered:
derive it in the CLI as
`net.JoinHostPort("127.0.0.1", strconv.Itoa(daemon.DefaultAPIPort))` —
rejected because `DefaultAPIAddr` is itself a literal constant, and a sibling
literal keeps the pair symmetric; a port change touches two adjacent lines.

**2. Resolution in `cmdDaemon`, error on conflict, detected via `fs.Visit`.**
After `fs.Parse`, a small pure helper decides the address:

```go
func daemonAPIAddr(apiAddr string, addrExplicit, loopback bool) (string, error)
```

- `loopback && addrExplicit` → error naming both flags, before anything
  listens;
- `loopback` → `daemon.LoopbackAPIAddr`;
- otherwise `apiAddr` unchanged.

`addrExplicit` comes from `fs.Visit` over an `api-addr` name. Comparing
`apiAddr != DefaultAPIAddr` instead would miss
`--api-addr :4242 --loopback` (an explicitly repeated default) and let it
slide through. Alternative considered: `--loopback` wins over `--api-addr`
silently — rejected; "two at once is a conflict, not a precedence" is this
command's established idiom (the two token sources), and a silent winner
between two bind addresses is exactly the failure class that hides. The same
shape is kept testable without a listener: the helper is pure, so its mapping
and conflict are unit-tested at table depth.

**3. End-to-end coverage of the bind probes first, then skips honestly.**
One integration test runs `cmdDaemon` with `--loopback` and asserts the
logged bind is `127.0.0.1:4242` (the address already goes to stderr). It
first dials `127.0.0.1:4242`; a refusal means the port is free and the test
runs, a connection makes it `t.Skip` — the same reason the rest of the suite
never touches a fixed port (a developer running this repo's examples often
has a daemon up).

**4. Completion, usage and docs are the flag's mirrors.**
`complete.go` gains `--loopback` and `-l` in the `daemon` entry (bool flags,
so no `values` entries) — the surface-coverage test enforces the rest.
`-l` does not clash: `alias -l` is a different `FlagSet`. The `usage()` line,
README daemon block, `docs/commands/serve.md` (its daemon paragraph and
shared Flags table), `docs/http-api.md`'s lead line, and the prose in
`docs/openapi.yaml` each name the shorthand; openapi.yaml changes are
prose-only, so the contract test is untouched.

## Risks / Trade-offs

- `--loopback` could be read as "keep my port, fix the host" (e.g. with
  `--api-addr 0.0.0.0:9999`) → the spec pins it to the default port and the
  conflict rule removes the ambiguity: wanting a non-default port is
  `--api-addr 127.0.0.1:9999`.
- The e2e test skips when 4242 is busy → unit coverage of the helper carries
  the conflict and mapping behaviour, so the skip only loses the
  listen-itself assertion, which `daemon.Listen` already covers elsewhere.
- A future daemon short flag could want `-l` → same class of risk as any
  short flag today; the daemon has no short flags at present.
