## Context

`docs/http-api.md` is the only description of the daemon control API, and it is
prose. The API has non-human consumers already: `remote/lambda/shared/daemon.ts`
hand-maintains `DaemonMetrics` and `DaemonStatus` interfaces mirroring Go's
`metrics.Stats` and `daemon.StatusResponse`, and the stop and stats Lambdas
parse replies against them. Those mirrors have no mechanical link to the Go
structs; they drift, and the drift shows up as a field silently arriving as
`undefined` in a Lambda.

The routes live in one place — `Handler` in `internal/daemon/api.go`, registered
as Go 1.22 method patterns (`"GET /v1/status"`) on a `http.ServeMux`. The
response shapes are three structs with JSON tags: `daemon.StatusResponse`,
`metrics.Stats` (and its nested `TokenStats`, `GpuStat`, `CpuStat`,
`MemoryStat`), and `remote.DeployConfig` for the request bodies.

## Goals / Non-Goals

**Goals:**

- One machine-readable contract for the control API, covering every route.
- The contract cannot silently fall behind the code — a test fails first.
- Consumers can pin the contract to an outfit version.

**Non-Goals:**

- Generating the TypeScript Lambda interfaces from the spec. Worth doing later;
  it needs a codegen step in the pnpm build, and this change is about having a
  trustworthy contract first.
- Serving the spec from the daemon itself (a `/v1/openapi.yaml` route). Nothing
  needs it at runtime, and it would mean embedding the file in the binary.
- Generating the spec from the Go types. See D2.
- Changing the API. If the spec and the code disagree on first run, the spec is
  wrong.

## Decisions

**D1 — OpenAPI 3.1, hand-written, at `docs/openapi.yaml`.** 3.1 rather than 3.0
because it is JSON Schema 2020-12 compatible, which matters for the one place
the API is loose: `remote.DeployConfig`'s `serveArgs` is an arbitrary string
array. `docs/` rather than the repository root because it is documentation and
`docs/http-api.md` sits beside it.

**D2 — Verified by a test, not generated.** A generator would be the stronger
guarantee, but it means a code-generation dependency and a `go:generate` step in
a project whose whole shape is "no runtime dependencies, stdlib where possible",
and it produces a spec whose descriptions and examples are then unwritable by
hand. The test gets the property that actually matters — the two cannot
disagree — at the cost of writing the YAML once. Rejected alternatives: a
generator (dependency, loses hand-written prose); nothing at all (the status
quo, which is how the prose drifted).

**D3 — The test compares names, not types.** It checks two things:

1. **Routes.** The set of `"METHOD /path"` patterns registered in `Handler`
   against the set of method/path pairs in the spec's `paths`. Go's `ServeMux`
   cannot enumerate its own routes, so `Handler` gains a small exported
   `Routes()` returning the pattern list, and registers from it — one list, used
   both to build the mux and to check the spec, so they cannot diverge.
2. **Response fields.** Reflection over each response struct's JSON tags against
   the `properties` of the named schema in the spec, in both directions.

It deliberately does *not* check types, formats, required-ness or descriptions.
Those are where a hand-written spec adds value over a generated one, and a test
that policed them would be a generator with extra steps. Field and route names
are where drift actually happens and where it actually hurts.

**D4 — A schema-name mapping table in the test.** The test needs to know that
`daemon.StatusResponse` is the spec's `StatusResponse` schema. That is a literal
table in the test file, one line per struct. Adding a response type without
adding its line is the one drift this design does not catch; it is a much
smaller hole than the one being closed, and a reviewer sees the table right
beside the new type.

**D5 — Attached to releases via GoReleaser's `release.extra_files`.** The
release already goes through GoReleaser, which attaches arbitrary files to the
GitHub release without a separate upload step or a third-party action. No CI
change is needed beyond the config, and the asset lands under the same
`GITHUB_TOKEN` the release already uses.

## Risks / Trade-offs

- **A hand-written spec can be wrong in ways the test does not check** — a field
  typed `string` that is really an integer, say. → Accepted, and bounded: names
  are checked mechanically, types are reviewed. The alternative trades that risk
  for a codegen dependency and unwritable prose.

- **`Routes()` is API surface added for a test.** → It is small, honest (the mux
  genuinely cannot enumerate itself), and it removes the duplicate route list
  rather than adding one — `Handler` registers from it.

- **A consumer might treat the spec as the source of truth and be wrong.** → The
  test makes the code and spec agree on names, which is what a consumer keys
  off. Where they could still disagree, the code wins, and the spec says so.
