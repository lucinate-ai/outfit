## Context

`requestModel` in `internal/gateway/gateway.go` reads the body with `io.ReadAll(io.LimitReader(r.Body, 1<<20))`, returns the model field and the bytes read, and the proxy forwards those bytes. The 1 MiB figure matches the daemon's control API, whose bodies are small. Gateway bodies carry whole conversations.

## Goals / Non-Goals

**Goals:**
- An over-limit body gets a `413` that names the limit and the flag.
- The limit is configurable and the default is above a realistic agent turn.
- Only a fully read body is ever forwarded.

**Non-Goals:**
- Streaming bodies to engines without buffering them.
- Changing the daemon control API's limit.

## Decisions

- Use `http.MaxBytesReader(w, r.Body, limit)`. Its read error is `*http.MaxBytesError`, which `requestModel` returns as a typed error that the handler maps to `413`. `requestModel` takes the `ResponseWriter` and the limit as arguments.
- Any other read error is also returned and refused, so a short read never reaches the parse step or the proxy.
- Default 64 MiB (`DefaultMaxRequestBytes`), held in `Options.MaxRequestBytes`; zero means the default. The flag rejects values below 1 at startup.
- The `413` body uses the gateway's existing error shape (`type: gateway_error`) and names the limit in bytes and `--max-request-bytes`.

## Risks / Trade-offs

- The body is held in memory per request, so a higher limit raises the memory a burst of large requests can use. 64 MiB is a default an operator can lower.
