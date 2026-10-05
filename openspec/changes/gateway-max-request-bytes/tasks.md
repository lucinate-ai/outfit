## 1. Gateway limit

- [ ] 1.1 Add `DefaultMaxRequestBytes` and `Options.MaxRequestBytes` in `internal/gateway`
- [ ] 1.2 Read the body with `http.MaxBytesReader` in `requestModel`, returning a distinct error for an over-limit body and for any other short read
- [ ] 1.3 Answer an over-limit body `413` naming the limit and `--max-request-bytes`; never forward a body not read in full

## 2. Command

- [ ] 2.1 Add `--max-request-bytes` to `spinloop gateway`, reject values below 1 at startup, pass it to the handler
- [ ] 2.2 Update flag completion if the flag list lives in `cmd/spinloop/complete.go`

## 3. Tests and docs

- [ ] 3.1 Tests: just under the limit routes, over the limit gets 413 naming limit and flag, nothing reaches an engine, flag validation
- [ ] 3.2 Document the flag and default in `docs/commands/gateway.md`
