All tasks are already implemented on the `feat/remote` branch; this change
records the specification delta that work introduced, so the boxes are ticked.

## 1. Outfit format

- [x] 1.1 Add the `REMOTE` keyword to the Outfit parser and `Selection`, keeping it out of `apply`'s business
- [x] 1.2 Render `REMOTE` in `Format` so `outfit export` round-trips it
- [x] 1.3 Cover the new keyword in the parser tests, including the duplicate and unknown-keyword paths

## 2. Remote transport

- [x] 2.1 Add `internal/remote` with the configuration type, its file and environment resolution, and region fallback
- [x] 2.2 SigV4-sign requests from the caller's credential chain, hashing the exact bytes sent so a request with a body signs over it
- [x] 2.3 Implement `Start` (retrying while the endpoint reports it is still starting), `Stop` and `Status`
- [x] 2.4 Implement `Deploy`, with `deploy_url` optional so existing configurations keep working for the other subcommands
- [x] 2.5 Test the flows against an `httptest` server with a pinned credential chain

## 3. Command group

- [x] 3.1 Add the `remote` dispatch with `start`/`stop`/`status`, resolving the configuration from an Outfit's `REMOTE` or the per-user file
- [x] 3.2 Add `deploy`, mapping the Outfit and its preset to a deployment: provider to engine, model or preset `hf` to weights, context, alias, and the remaining preset flags
- [x] 3.3 Drop the settings the endpoint owns, matching on canonical flag names, and layer the preset's `[*]` defaults under the chosen section
- [x] 3.4 Add `--dry-run`, and report whether the endpoint has to fetch weights before it can serve
- [x] 3.5 Test the mapping, its rejections, and that a deployment is posted and signed

## 4. Catalogue and harnesses

- [x] 4.1 Add a `vllm` provider entry
- [x] 4.2 Add `apiKeyOptional` to the provider schema and mark `llamacpp` with it, so the remote endpoint's key is injected when set
- [x] 4.3 Write Pi's keyless placeholder when an optional key resolves to nothing, keeping the `$VAR` reference for every other provider
- [x] 4.4 Test both directions for an optional key, and that a non-optional key keeps its reference when unset

## 5. Completion and documentation

- [x] 5.1 Teach the completion table about subcommands and add `remote`, satisfying the dispatch-coverage test
- [x] 5.2 Document the `REMOTE` instruction in the user documentation
- [x] 5.3 Give `outfit remote` a page in the user manual and list it in the index
- [x] 5.4 Update AGENTS.md with the new packages, and record the traps found along the way

## 6. Usable output

- [x] 6.1 Report progress while `start` waits, with the elapsed time, since the endpoint blocks until the model is serving
- [x] 6.2 Put progress on stderr and only the exports on stdout, so the result stays pipeable
- [x] 6.3 Explain `remote` in the usage text, as every other command is

## 7. Keys that actually work

- [x] 7.1 Warn when a config is written with no resolvable key for a non-local endpoint
- [x] 7.2 Write opencode's key as an `{env:VAR}` reference so no secret reaches disk
- [x] 7.3 Keep Pi's reference for a remote endpoint, since a placeholder cannot be repaired later
- [x] 7.4 Pass the keys outfit can resolve to the agent it launches
- [x] 7.5 Resolve `.env` beside the Outfit, falling back to the working directory when there is none

## 8. The deployment

- [x] 8.1 Start on demand: try each zone for capacity, take a stable address, and report ready only once the model answers
- [x] 8.2 Terminate when unused, judging activity from the engine's own counters and reading the names its engine exposes
- [x] 8.3 Order the bounds — retention, then maximum runtime, then the grace period — and let a manual stop through regardless
- [x] 8.4 Serve through a chosen engine, with an image per engine and no default
- [x] 8.5 Hold what to serve as one stored configuration, owned by whoever deploys and not overwritten by deploying the infrastructure
- [x] 8.6 Derive where weights live from engine, model and quantisation, ignoring any location a caller supplies
- [x] 8.7 Fetch absent weights within the deployment, judging presence by a marker written only on completion
- [x] 8.8 Add `scripts/check-no-cloud-identifiers.sh` and run it first in CI
- [x] 8.9 Add a `Remote deployment` workflow: install, typecheck, test, and synthesize
