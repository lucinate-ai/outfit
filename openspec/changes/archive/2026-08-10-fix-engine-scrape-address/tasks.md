## 1. Locate the engine where it actually is

- [x] 1.1 Parse `--host` and `--port` out of the engine argv in `scrapeTargetFor`, alongside the API key it already lifts from there
- [x] 1.2 Prefer that bind over the configured `BASEURL` and over the engine's `defaultBaseURL`
- [x] 1.3 Rewrite a wildcard bind (`0.0.0.0`, `::`, `[::]`) to loopback
- [x] 1.4 Leave the existing precedence intact when the argv states no address

## 2. Make a failing collector visible

- [x] 2.1 Append a scrape failure to `Stats.Errors` in `Daemon.Metrics`, naming the address tried, instead of discarding the error
- [x] 2.2 Leave an absent source silent — an engine with no metrics endpoint yields no target and reports nothing

## 3. Tests

- [x] 3.1 Regression test for the cloud shape: deploy config, no `BASEURL`, `--port 8000` → scrape `127.0.0.1:8000`
- [x] 3.2 Test the bind wins over a configured `BASEURL`
- [x] 3.3 Test the old precedence still holds when no bind is stated
- [x] 3.4 Test a wildcard bind resolves to loopback, and that port-only and host-only forms work
- [x] 3.5 Test vLLM honours its stated bind too
- [x] 3.6 Test a failed scrape reports an error naming the address, and reports no tokens

## 4. Verify

- [x] 4.1 `gofmt`, `go vet`, `go test ./...`
- [x] 4.2 Confirm on a live cloud instance that the engine's `/metrics` answers on the bound port and not on the engine default
- [x] 4.3 Confirm the daemon reports tokens after the fix, and that `lastActiveAt` advances when a request is served
