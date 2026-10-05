## 1. Gateway limit

- [x] 1.1 Add `DefaultMaxRequestBytes` and `Options.MaxRequestBytes` in `internal/gateway`
- [x] 1.2 Read the body with `http.MaxBytesReader` in `requestModel`, returning a distinct error for an over-limit body and for any other short read
- [x] 1.3 Answer an over-limit body `413` naming the limit and `--max-request-bytes`; never forward a body not read in full

## 2. Command

- [x] 2.1 Add `--max-request-bytes` to `spinloop gateway`, reject values below 1 at startup, pass it to the handler
- [x] 2.2 Flag completion needs no change: the gateway's flags are completed from its Cobra flag set

## 3. Tests and docs

- [x] 3.1 Tests: just under the limit routes, over the limit gets 413 naming limit and flag, nothing reaches an engine, flag validation
- [x] 3.2 Document the flag and default in `docs/commands/gateway.md`
