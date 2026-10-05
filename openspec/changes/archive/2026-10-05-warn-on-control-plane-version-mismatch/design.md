## Context

The control plane is a set of Lambdas deployed by CDK from `remote/`, using sources version-matched to the CLI (`ResolveRef`). The CLI calls them through `call`, `send` and `callStats` in `internal/remote/remote.go`. All Lambdas build responses through `jsonResponse` in `remote/lambda/shared/http.ts`.

## Goals / Non-Goals

**Goals:**
- A version mismatch warning on every remote command, with no extra API call.
- One place on each side that needs to change.

**Non-Goals:**
- Blocking commands on a mismatch.
- Comparing semantic versions (older vs newer); any difference warns.
- Changing response bodies.

## Decisions

**Response header, not a body field.** `jsonResponse` is used by every Lambda response, error ones included, so one edit covers all of them. Bodies differ in shape per Lambda (some are not objects), and the Go side would need a field on each reply struct. A header is read in `send`/`callStats`, which every call passes through.

**Version injected at deploy through CDK context.** `config.ts` already reads CDK context values; add `controlPlaneVersion` (context key `controlPlaneVersion`, default `dev`) and set it as `CONTROL_PLANE_VERSION` in `commonEnv` so all Lambdas get it. Bootstrap passes `-c controlPlaneVersion=<version>` to the `deploy` script via an environment variable read by `loadConfig` (`SPINLOOP_CONTROL_PLANE_VERSION`), which avoids changing the package scripts. `jsonResponse` reads `process.env.CONTROL_PLANE_VERSION` and falls back to `dev`.

**Warning once, in the transport layer.** `send` and `callStats` return the header value; a small function in `internal/remote` compares it against the CLI version and calls a warning writer, guarded by `sync.Once`. The CLI version is set at startup from `main.version`. Comparison trims a leading `v`. Absent header, empty, or `dev` on either side produces no warning.

**Stderr.** The warning is written to stderr in the CLI's chrome style per `cli-ux`, so JSON and other piped stdout stay clean.

## Risks / Trade-offs

- A control plane deployed from a dev build reports `dev` and never warns. Accepted; dev builds are not comparable.
- A once-per-process guard means a long-running `daemon` or `gateway` process warns once, not on every poll. Accepted.
- Existing control planes show no warning until re-bootstrapped. Accepted; there is no reliable way to tell them from old ones without a call.
