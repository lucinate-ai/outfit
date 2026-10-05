## Why

`spinloop gateway` reads at most 1 MiB of a completion request body with `io.LimitReader`, which cuts a larger body off without saying so. The cut-off JSON fails to parse and the caller gets `400 "the request is not a JSON body: unexpected end of JSON input"`, which points at the caller's JSON when the body was well formed. A long coding-agent session passes 1 MiB routinely, so the agent works for a while and then fails every turn. The same truncated buffer is also what the proxy forwards, so the only thing stopping a truncated prompt reaching an engine is the parse failure.

## What Changes

- The gateway reads the request body through `http.MaxBytesReader`, so a body over the limit is an error and not a silent truncation.
- A body over the limit is answered `413` with a message naming the limit and the `--max-request-bytes` flag.
- The default limit rises from 1 MiB to 64 MiB, and `spinloop gateway --max-request-bytes <n>` sets it.
- A body that was not read in full is never forwarded to an engine.
- `docs/commands/gateway.md` documents the flag and its default.

## Capabilities

### New Capabilities

### Modified Capabilities
- `fleet-gateway`: adds a requirement for the request body limit, its `413` answer, and the flag that sets it.

## Impact

- `internal/gateway/gateway.go`: `requestModel`, `Options`, `Handler`.
- `cmd/spinloop/gateway.go`: the new flag, passed to the handler.
- `cmd/spinloop/complete.go`: completion of the new flag if flags are listed there.
- `docs/commands/gateway.md`, `openspec/specs/fleet-gateway/spec.md` (via the delta).
