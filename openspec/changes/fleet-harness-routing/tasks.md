## 1. The Outfit instruction

- [ ] 1.1 Add the `FLEET` keyword to `internal/outfit`: parse it, add `Fleet` to
      `Selection`, and classify the value as a path or a URL (a scheme means a
      URL)
- [ ] 1.2 Reject an Outfit naming both `FLEET` and `REMOTE`, naming both
      instructions in the error
- [ ] 1.3 Tests: a fleet path parses, a fleet URL parses as an endpoint, the
      duplicate rule still applies, `FLEET` + `REMOTE` fails, `FLEET` + `BASEURL`
      parses

## 2. The daemon reports where its engine serves

- [ ] 2.1 Extract the engine-endpoint derivation from `scrapeTargetFor` in
      `cmd/outfit/serve_daemon.go` into a helper yielding port, path prefix,
      loopback-only, and requires-key from the engine and its argv
- [ ] 2.2 Add the engine endpoint to `daemon.StatusResponse`, populated from a
      hook the CLI supplies alongside `BuildArgv`, omitted when no engine runs
      and never carrying a key value
- [ ] 2.3 Update `docs/openapi.yaml` and the API contract test with the new
      status fields
- [ ] 2.4 Tests: a running engine reports port and flags, an idle daemon reports
      none, a gated engine reports requires-key and no key, the reported port is
      the engine's and not the API's

## 3. Fleet file: engine endpoint and engine key

- [ ] 3.1 Add the optional per-node `engine:` block (host, port, path) to
      `fleet.NodeConfig`, each field falling back independently
- [ ] 3.2 Add `engineTokenEnv` and resolve it exactly as the daemon token
      reference is (environment, then the `.env` beside the fleet file)
- [ ] 3.3 Tests: a full override, a partial override, no block at all, an unset
      engine token variable naming itself

## 4. Selection

- [ ] 4.1 Add a selector in `internal/fleet` that ranks `[]NodeResult`: running
      nodes serving the wanted model first, longest-idle wins, fleet-file order
      breaks ties, non-answering nodes skipped
- [ ] 4.2 Add endpoint resolution: the node's engine override, else the node's
      host with the daemon's reported port and path; refuse a loopback-only
      engine on a non-loopback node, and refuse a running node that reports no
      endpoint
- [ ] 4.3 Resolve the chosen node's engine key when the node reports one is
      required, failing before anything is launched when it cannot be resolved
- [ ] 4.4 Tests: the ranking rules, each refusal, and a whole fleet that cannot
      be reached

## 5. Waking a node

- [ ] 5.1 Derive a `remote.DeployConfig` from a Selection, sharing the
      derivation with `outfit remote deploy` rather than copying it
- [ ] 5.2 Wake a candidate: push the config on `POST /v1/start`, move to the
      next candidate on a rejected config, and treat an already-running refusal
      as a re-read rather than a failure
- [ ] 5.3 Wait for readiness — poll status to `running`, then probe the resolved
      engine endpoint — bounded by a package-level timeout, reporting progress
      and leaving a started engine running on timeout
- [ ] 5.4 Tests against an HTTP test server: a woken node, a node that rejects
      the config, every node rejecting it, a slow engine, a timeout, and losing
      the race to another client

## 6. Routing the harness launch

- [ ] 6.1 Add `--fleet`, `--node`, `--no-wake` and `--wake-timeout` to
      `outfit harness`, with `--fleet` overriding the Outfit's `FLEET`
- [ ] 6.2 Route before the apply, beside `fetchRemoteEnv`: select, wake if
      needed, and fill `sel.BaseURL` from the chosen node — skipping selection
      when the Outfit pins a `BASEURL`, and saying so
- [ ] 6.3 Inject `OPENAI_BASE_URL` and the node's engine key into the launched
      agent's environment without overriding what is already set, including the
      harness-specific key name lucinate reads
- [ ] 6.4 Fail a `FLEET` URL with "gateway routing is not implemented yet"
- [ ] 6.5 Report the chosen node, its endpoint, and the reason on stderr before
      launching
- [ ] 6.6 Tests: routed launch, pinned node, `--no-wake` failing with the node
      table, a pinned `BASEURL` skipping selection, an exported
      `OPENAI_BASE_URL` winning, and a failed route leaving the harness config
      untouched

## 7. `outfit fleet route`

- [ ] 7.1 Add the `route` subcommand: resolve the Outfit and fleet as a launch
      does, report the node, endpoint and reason, and change nothing
- [ ] 7.2 Report the node a launch would wake when none is serving, without
      waking it
- [ ] 7.3 Tests: an explained choice, a no-choice report, and that nothing is
      started or written

## 8. Documentation and examples

- [ ] 8.1 Document `FLEET` in `docs/outfit-file.md` and the new flags in
      `docs/commands/harness.md`
- [ ] 8.2 Document `outfit fleet route`, the `engine:` block and
      `engineTokenEnv` in `docs/commands/fleet.md`
- [ ] 8.3 Update `examples/fleet/fleet.yaml` with a commented engine override and
      engine token reference
- [ ] 8.4 Extend `examples/fleet-docker` to route a harness launch at a node —
      the published-port case the engine override exists for — and cover it in
      `run-tests.sh`
- [ ] 8.5 Update `AGENTS.md` with the routing path and its invariants (never
      displace a running engine, never return an engine key, resolve before
      writing the config)

## 9. Verification

- [ ] 9.1 `gofmt`, `go vet`, and `go test ./... -cover` at or above 80%
- [ ] 9.2 `openspec validate fleet-harness-routing --strict`
- [ ] 9.3 Run the `fleet-docker` example end to end: route, wake a node, launch a
      harness against it
