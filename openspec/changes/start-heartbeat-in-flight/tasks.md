## 1. In-flight state in internal/remote

- [x] 1.1 Add a package-level `StateInFlight` constant (value `"in-flight"`) in `internal/remote` with a doc comment stating it is the client-side report for an attempt in flight and that no Lambda reply ever carries it
- [x] 1.2 In `Start`, call `onState(StateInFlight)` at the top of the retry loop, before each `call` (covering the first attempt, the retry after a 503, and the re-attach after a dropped connection), and update `Start`'s doc comment so the observer contract reads: the raw state of every poll that returns a response, plus the in-flight report when a new attempt is issued

## 2. Tests

- [x] 2.1 Update `TestStart_ReportsEachPollState` to the new reported sequence (in-flight before each attempt: `in-flight,no-capacity,in-flight,ready`) rather than loosening the exact-match assertion
- [x] 2.2 Add a test for the shape of the bug: a server that answers no-capacity, then ready on the second attempt, asserting the observer sees the in-flight report between the no-capacity report and ready, so a boot following a capacity wait can no longer leave a stale no-capacity state
- [x] 2.3 In the `startProgress` heartbeat tests, add a case driving `setState` with the in-flight sentinel and assert the line reads as starting, not waiting for capacity

## 3. Verification

- [x] 3.1 Run `gofmt -l .`, `go vet ./...`, and `go test ./... -cover`; confirm everything passes and total coverage stays at or above 80%
