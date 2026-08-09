## 1. The node image

- [ ] 1.1 `examples/fleet-docker/Dockerfile`: multi-stage — build outfit from
      the working tree, then a slim runtime carrying the outfit binary, a
      pinned Imposter native engine binary, and `OUTFIT_CONFIG_DIR` set
- [ ] 1.2 Add the `llama-server` shim on `PATH`: parse the daemon's `--port`,
      ignore llama.cpp's other flags, and `exec` the Imposter **engine binary**
      with `IMPOSTER_PORT` set — never `imposter up` (see design D2)
- [ ] 1.3 Add the Imposter engine config: `/health`, and a `/metrics` serving
      the `llamacpp:` counters `internal/metrics` parses
- [ ] 1.4 Verify by hand: `docker run` one node, `POST /v1/start`, confirm the
      engine is a child of the daemon and `/v1/metrics` reports token stats

## 2. The fleet stack

- [ ] 2.1 `examples/fleet-docker/compose.yaml`: several nodes, deliberately not
      clones — at least one requiring a token and one without (design D5)
- [ ] 2.2 `fleet.yaml` naming the nodes by compose service name, with
      `tokenEnv` references for the nodes that need one
- [ ] 2.3 `.env.example` for the node tokens, and ensure the real `.env` is
      gitignored
- [ ] 2.4 Verify by hand: `docker compose up -d`, then `outfit fleet status`
      and `outfit fleet metrics -w` against the stack

## 3. The test run

- [ ] 3.1 `run-tests.sh`: bring up, wait for readiness, assert, tear down —
      readable output on failure, usable by a maintainer as well as CI
- [ ] 3.2 Assertions from the spec: every node rendered and exit 0 with one
      node stopped; `unauthorized` distinguished from `unreachable`; a stopped
      container reads `unreachable`; `start`/`stop` drive one node only
- [ ] 3.3 Crash assertion: kill the engine (the daemon's **direct child**) and
      assert `crashed`, then recover with `fleet start`
- [ ] 3.4 Cold-start assertion: with nothing started, every node reads `idle`
      and `fleet status` succeeds

## 4. CI

- [ ] 4.1 Add a workflow job that runs `run-tests.sh`, failing the build on a
      failed assertion
- [ ] 4.2 Decide per-PR vs nightly/on-label (design open question) and wire the
      trigger accordingly

## 5. Docs and verification

- [ ] 5.1 `examples/fleet-docker/README.md`: what it brings up, how to run it,
      how to run the assertions, and that the engine is a fake
- [ ] 5.2 Cross-link with `examples/fleet/` (real machines vs runnable stack)
      and reference it from `docs/commands/fleet.md`
- [ ] 5.3 `go test ./... -cover` still green, `gofmt`/`go vet` clean, the
      cloud-identifier guard clean
- [ ] 5.4 `openspec validate fleet-docker-example --strict` passes
