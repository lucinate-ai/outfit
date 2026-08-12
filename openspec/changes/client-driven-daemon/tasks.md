## 1. The engine key arrives with a start

- [x] 1.1 Add an engine API key to the start request body alongside the deploy
      config, and to the stored config so a later bare start reuses it
- [x] 1.2 Write a supplied key 0600 into the daemon's state directory and pass
      the engine's key-file argument, never a literal one; replace the file when
      a new key arrives
- [x] 1.3 Keep the key out of every reply, error and log line the API produces
- [x] 1.4 Reject a start carrying a key while an engine is running without
      storing either the key or the config
- [x] 1.5 Tests: a gated start, a keyless start, a bare start reusing the stored
      key, a superseding key, a refused start storing neither, and that the key
      appears in no reply and no process command line

## 2. The daemon stops reading workload configuration

- [x] 2.1 Remove the Outfit path from `outfit daemon`, and fail with a message
      naming the start request when one is given
- [x] 2.2 Delete the Outfit branch of the daemon's `BuildArgv`, its
      `resolveDaemonOutfit`, and its `OUTFIT_ALIAS`/`defaultOutfitNamed` gate;
      what to serve comes from the request or the stored config alone
- [x] 2.3 Make "nothing to serve" name what would supply it
- [x] 2.4 Tests: an Outfit path is refused, an adjacent Outfit is not read, a
      bare start with nothing stored fails, and a stored config still survives a
      restart

## 3. The bearer token gains a file and a flag

- [x] 3.1 Add `--api-token-file` (trimmed) and `--api-token` beside
      `OUTFIT_API_TOKEN`; fail naming both when more than one is given
- [x] 3.2 Fail at startup when a token file cannot be read, rather than
      listening without a token
- [x] 3.3 Update the non-loopback refusal message to name every way a token can
      be supplied, and stop referring to an Outfit's `.env`
- [x] 3.4 Tests: each source, a conflict, an unreadable file, and that loopback
      still needs none

## 4. The client supplies the key

- [x] 4.1 Resolve the engine key from `engineTokenEnv` before waking a node and
      send it with the start
- [x] 4.2 Give the launched agent the key the client set, rather than resolving
      one against what the node reports
- [x] 4.3 Keep the early failure for an already-running gated node whose fleet
      entry names no key, and for a variable that resolves to nothing
- [x] 4.4 Wake an ungated engine when the node's entry names no key
- [x] 4.5 Tests: a woken node is gated with the client's key and the agent gets
      the same value; no key wakes an ungated engine; an already-running gated
      node with no key fails before launching

## 5. Documentation and examples

- [x] 5.1 Update `docs/openapi.yaml` with the start request's key field, stating
      that it is never returned
- [x] 5.2 Document the daemon's inputs in `docs/commands/serve.md` — its flags
      and its API, and that it reads no Outfit, preset or fleet file
- [x] 5.3 Document the token sources in `docs/env-vars.md` and the serve docs,
      recommending the file form and stating what the literal flag costs
- [x] 5.4 Rewrite `examples/fleet-local` for a daemon started with no Outfit,
      and drop the paragraph about `fleet start` working standalone
- [x] 5.5 Rewrite `examples/fleet-docker`: remove `node/Outfit` and the `CMD`
      that passes it, gate one node's engine to cover the key path, and assert
      it in `run-tests.sh`
- [x] 5.6 Update `AGENTS.md`: the daemon's two inputs, the key's path to the
      engine, and why the token's literal flag reverses an earlier decision

## 6. Verification

- [x] 6.1 `gofmt`, `go vet`, and `go test ./... -cover` at or above 80%
- [x] 6.2 `openspec validate client-driven-daemon --strict`, and `concord check`
      clean once `fleet-harness-routing` has archived
- [x] 6.3 Run the `fleet-docker` example end to end, including a routed launch
      against a gated node
- [x] 6.4 Confirm by hand on a running node that the engine's key appears in no
      process listing and no API reply
