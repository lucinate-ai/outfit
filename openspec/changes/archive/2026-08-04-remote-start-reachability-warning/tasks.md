## 1. Probe function in internal/remote

- [x] 1.1 Add `ProbeReachability` function that TCP-dials a host:port with a configurable timeout (default 5s)
- [x] 1.2 Expose `probeTimeout` as a package-level variable for test injection
- [x] 1.3 Add unit tests for ProbeReachability: success, connection refused, timeout

## 2. Warn in cmdRemoteStart on probe failure

- [x] 2.1 After start succeeds and "ready" is printed, call `ProbeReachability` with the base URL
- [x] 2.2 On probe failure, detect the caller's public CIDR using `detectPublicCIDR` and print the warning to stderr
- [x] 2.3 Ensure the warning uses a placeholder CIDR if IP detection fails
- [x] 2.4 Ensure the command still exits 0 with exports after a warning

## 3. Tests for the start flow with probe

- [x] 3.1 Add test for start success with reachable probe (no warning output)
- [x] 3.2 Add test for start success with unreachable probe (warning on stderr)
- [x] 3.3 Add test for start success with unreachable probe and failed IP detection (placeholder in warning)

## 4. Update completion flags

- [x] 4.1 Verify `__complete` still covers the remote start command (no new flags added, but check for regressions)

## 5. Verify

- [x] 5.1 Run `go test ./...` to ensure all tests pass
- [x] 5.2 Run `go test ./... -cover` to verify coverage remains >= 80%
- [x] 5.3 Run `gofmt -w ./...` to ensure formatting
