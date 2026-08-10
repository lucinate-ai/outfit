## 1. The daemon's log reader

- [ ] 1.1 Add a bounded reader over the supervisor's log file: read the tail when no offset is given, read forward from an offset when one is, cap the request at the endpoint's own maximum
- [ ] 1.2 Trim reads to line boundaries and report the offset actually stopped at, so a partial line is never rendered and the cursor stays exact
- [ ] 1.3 Distinguish the readable states: no log file at all (engine never ran, or output goes to the daemon's stdio), a log that exists and is empty, and an offset beyond the current end
- [ ] 1.4 Unit-test the reader against a temp file: tail read, resume from offset, nothing-new, line trimming, truncation detected, cap enforced

## 2. The `/v1/logs` endpoint

- [ ] 2.1 Define `LogsResponse` (content, next offset, size, and the missing-log and stale-offset signals) with JSON tags
- [ ] 2.2 Add `handleLogs`, parsing and validating `offset` and `limit`, returning JSON errors in the existing shape for bad input
- [ ] 2.3 Register `GET /v1/logs` in `Routes()` and `Handler()` so it sits behind the bearer token like every other route
- [ ] 2.4 Add `LogsResponse` to `schemaFor()` in `openapi_test.go` — a type with no line there has no contract coverage
- [ ] 2.5 Write the path and schema into `docs/openapi.yaml` by hand, matching the route and property names, and confirm the contract test passes
- [ ] 2.6 Test the endpoint: reading while running, after a crash, with and without a cursor, unauthorised without the token, and the bad-input errors

## 3. The fleet client

- [ ] 3.1 Add `Logs` to `fleet.Client`, taking an offset and limit and returning the daemon's response
- [ ] 3.2 Add `Logs` to the `Node` interface and `daemonNode`, and a `LogsCall` for `FanOut`
- [ ] 3.3 Classify a 404 from a node as "this daemon predates the endpoint, upgrade it" rather than a generic failure
- [ ] 3.4 Test the client and fan-out against a stub node: success, unreachable, unauthorised, no-log, and the too-old-daemon case

## 4. The `outfit fleet logs` subcommand

- [ ] 4.1 Create `cmd/outfit/fleet_logs.go` with `cmdFleetLogs`, parsing the optional node argument, `--follow`/`-f`, `--limit`, `--format` and the existing `--fleet` path flag
- [ ] 4.2 Render text output: node prefix only when more than one node's output is printed, each node's lines kept in their own order and never interleaved by a fabricated time order
- [ ] 4.3 Render `--format json`, carrying the node, the content and the offset
- [ ] 4.4 Implement the follow loop: hold a per-node offset, poll each node from its own cursor, exit cleanly on SIGINT/SIGTERM
- [ ] 4.5 Report per-node failures alongside the output that did arrive, without failing the command
- [ ] 4.6 Add the `logs` case to `cmdFleet`, its usage string and the unknown-subcommand error, and add it to the fleet subcommand list in `cmd/outfit/complete.go`
- [ ] 4.7 Test the command against a stub fan-out: single node unlabelled, several nodes labelled, json output, follow without duplicates, clean exit on cancel, and a mixed fleet where one node fails

## 5. Extracting the shared rendering

- [ ] 5.1 Lift the small pieces `outfit remote logs` and `outfit fleet logs` genuinely share — the "label only when origins are mixed" rule and the follow loop's cancel-and-exit shape — into one place both call, without building an abstraction over the two dissimilar fetches
- [ ] 5.2 Confirm `outfit remote logs` behaviour is unchanged by the extraction: its existing tests must pass untouched

## 6. Documentation

- [ ] 6.1 Document `outfit fleet logs` and its flags in `docs/commands/fleet.md`, in that file's existing voice
- [ ] 6.2 Document the `/v1/logs` endpoint in `docs/http-api.md`, including the cursor and the bounding rules
- [ ] 6.3 Note that log content crosses the network to whoever holds the fleet token, since engine output can carry prompts and model output
- [ ] 6.4 Say plainly that the daemon does not rotate the engine log, so an operator knows what they are accumulating

## 7. Verification

- [ ] 7.1 `gofmt -l .` clean, `go build ./...` and `go vet ./...` pass
- [ ] 7.2 `go test ./... -cover` passes, including the OpenAPI contract test
- [ ] 7.3 `bash scripts/check-spec-purposes.sh` and `openspec validate add-fleet-logs-command` pass
- [ ] 7.4 Manually verify against a real fleet: a running node, a crashed node, a node that never ran an engine, `--follow` across two nodes, and one unreachable node
