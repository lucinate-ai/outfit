## Why

The daemon's control API is described only in prose, in `docs/http-api.md`.
That prose is already behind the code more than once — it does not mention the
deploy config a start request may carry, and it gained `lastActiveAt` and
`idleSeconds` only because someone remembered to add them. Meanwhile the API
has real consumers that are not people: the stop and stats Lambdas curl it over
SSM and hand-maintain TypeScript interfaces mirroring the Go response structs
(`remote/lambda/shared/daemon.ts`), and a `fleet` client will want the same.
Every one of those mirrors is a copy that can silently drift.

A machine-readable description fixes the consumer problem, but only if it is
itself guaranteed current — an OpenAPI file nobody checks is one more thing to
drift. So the spec ships with a test that fails when it disagrees with the
code.

## What Changes

- Add `docs/openapi.yaml`: an OpenAPI 3.1 description of the daemon control API
  — every route, its auth, its request body where it has one, and the schemas of
  its replies.
- Add a Go test that compares the spec against the running code: the routes the
  handler registers, and the JSON field names of the structs it serialises. A
  route or field in one and not the other fails the build. This is what makes
  "always up to date" a property rather than an intention.
- `docs/http-api.md` links the spec and keeps its prose for the behaviour a
  schema cannot express (what a 409 means, why stopping the engine does not end
  the daemon).
- Attach `docs/openapi.yaml` to each GitHub release, so a consumer can fetch the
  contract for a specific outfit version rather than reading whatever is
  currently on the default branch.

## Capabilities

### New Capabilities
- `daemon-api-contract`: the machine-readable description of the control API —
  what it must cover, that it is verified against the implementation rather
  than maintained by hand, and that it is published per release.

### Modified Capabilities
None. The API itself is unchanged; this describes what is already there.

## Impact

- **New files**: `docs/openapi.yaml`, and a drift test in `internal/daemon`.
- **Changed**: `docs/http-api.md` (link the spec), `.goreleaser.yaml`
  (`release.extra_files` attaches the spec to the release).
- **No code changes**: no handler, route or response type is touched. If the
  test fails on first run, the spec is wrong, not the API.
