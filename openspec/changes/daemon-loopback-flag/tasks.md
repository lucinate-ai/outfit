## 1. Core implementation

- [ ] 1.1 Add `LoopbackAPIAddr` constant (`"127.0.0.1:4242"`) in
      `internal/daemon/api.go` beside `DefaultAPIAddr`, with a comment tying
      it to the `--loopback` flag and the token-free loopback bind
- [ ] 1.2 In `cmdDaemon` (`cmd/outfit/serve_daemon.go`), register a boolean
      `--loopback`/`-l` flag ("bind the control API to loopback, like
      `--api-addr 127.0.0.1:4242`; needs no token")
- [ ] 1.3 After parsing, resolve the address through a pure helper
      `daemonAPIAddr(apiAddr string, addrExplicit, loopback bool) (string,
      error)`: both given is an error naming both flags; `--loopback` alone
      yields `daemon.LoopbackAPIAddr`; detect `addrExplicit` with
      `fs.Visit`

## 2. Mirrors

- [ ] 2.1 Add `--loopback` and `-l` to the `daemon` entry of the completion
      table in `cmd/outfit/complete.go` (bool; no `values` entries)
- [ ] 2.2 Update the `outfit daemon` line in `usage()` in
      `cmd/outfit/main.go` to name `[--loopback]`

## 3. Tests

- [ ] 3.1 Unit-test `daemonAPIAddr`: `--loopback` alone (default `--api-addr`)
      → `LoopbackAPIAddr`; explicit `--api-addr` without `--loopback` →
      unchanged; both given → error naming both; neither → default passes
      through
- [ ] 3.2 Integration test: `cmdDaemon` with `--loopback` (no token
      configured) binds — probe `127.0.0.1:4242` first and `t.Skip` when in
      use, otherwise assert the logged bind and that an unauthenticated
      request gets an answer
- [ ] 3.3 Verify `TestCompletionCoversDispatch` and the flag-mirror test pass
      with the new flag, and that completion offers `--loopback`/`-l` for
      `outfit daemon`

## 4. Docs

- [ ] 4.1 README: the daemon usage line and the daemon example block name the
      loopback shorthand
- [ ] 4.2 `docs/commands/serve.md`: the bind sentence and the Flags table
      mention `--loopback`/`-l` (daemon only)
- [ ] 4.3 `docs/http-api.md` lead line and `docs/openapi.yaml` prose name the
      shorthand (prose only; no contract change)

## 5. Verification

- [ ] 5.1 `gofmt -w ./...`, `go vet ./...`, `go test ./... -cover` (total
      coverage stays ≥ 80%)
- [ ] 5.2 `openspec validate daemon-loopback-flag` is clean
