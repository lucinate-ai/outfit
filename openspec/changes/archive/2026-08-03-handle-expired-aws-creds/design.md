## Context

All remote control requests funnel through `call` in `internal/remote/remote.go`,
which signs the request, sends it, and parses the JSON reply into a `Response`.
`call` returns an error **only** when the body fails to unmarshal — and in that
one branch it adds a permissions hint for HTTP 403. When the body *does* parse,
`call` returns the `Response` regardless of HTTP status.

`Deploy`, `Start`/`waitReady`, and `Stats` re-check `resp.StatusCode` after
`call` and turn non-200s into errors. `Status` and `Stop` do not — they return
the parsed `Response` straight to their command handlers. An expired-credential
rejection from the Lambda Function URL authorizer is HTTP 403 with a JSON body
like `{"message":"The security token included in the request is expired"}`. That
parses cleanly into `Response{StatusCode: 403, Message: "...", State: ""}`, so
`cmdRemoteStatus` prints `state: ` (blank) and exits 0. That is the reported bug.

Credential handling today knows two cases: the chain resolves nothing
(`sign` wraps `Credentials.Retrieve` with "configure env credentials, a profile
or an SSO session"), and a JSON-unparseable 403 (hinted as a *permissions*
problem). Expired temporary credentials fall between: they `Retrieve` fine and
are rejected only at the API, currently as a blank result or a misattributed
permissions hint. The SDK/smithy stack (`smithy-go` is already an indirect dep)
exposes these as `APIError` codes: `ExpiredToken`, `ExpiredTokenException`,
`InvalidClientTokenId`, `RequestExpired`, `UnrecognizedClientException`.

## Goals / Non-Goals

**Goals:**
- No control subcommand ever prints an empty/partial result as success; a
  non-2xx control reply is always an error with non-zero exit.
- Expired or invalid credentials produce one clear, actionable message ("refresh
  your AWS credentials …"), distinct from missing-credentials and lacks-
  permission.
- Detection lives in one place in `internal/remote` so `status`, `stop`,
  `start`, `deploy`, and `stats` all benefit.

**Non-Goals:**
- Refreshing or acquiring credentials for the user (outfit stores none).
- Changing the signing path, request shape, or stored config.
- Reworking `bootstrap`'s credential flow beyond reusing the classifier if it
  is cheap; `bootstrap` already returns a non-blank error.

## Decisions

**1. Enforce the status-code check for every control reply, centrally.**
Make `call` itself reject a non-2xx reply rather than relying on each caller to
remember. The wrinkle: `waitReady` treats HTTP 503 as the expected "still
starting" signal, and `Deploy`/`Stats` build their own messages from `Error`/
`Errors`. To avoid breaking those, keep `call` returning the `Response` but add
the missing `resp.StatusCode != http.StatusOK` guard to `Status` and `Stop`
(mirroring `Deploy`), and route every non-200 through a shared
`statusError(resp, body)` helper that builds the message and applies credential
classification. `waitReady`'s explicit 503 handling stays ahead of that guard so
its semantics are unchanged.
  - *Alternative considered:* make `call` blanket-error on non-2xx. Rejected:
    it would force `waitReady`, `Deploy`, and `Stats` to special-case their way
    back out, more churn than the targeted guards.

**2. Classify expired/invalid credentials with a dedicated helper.**
Add `classifyCredentialError(err)` / a check that recognises the condition from
two sources: (a) an `errors.As` match on smithy `APIError` codes above, for the
`Retrieve` path and any SDK call; and (b) an HTTP 403 whose body carries the
same markers (`ExpiredToken`, `InvalidClientTokenId`, "security token" +
"expired", "RequestExpired"), for the Function-URL authorizer path where the
rejection arrives as an HTTP body, not a typed error. When matched, return the
"refresh your AWS credentials (env credentials, a profile, or an SSO session)"
message; otherwise fall back to the existing permissions hint for a 403, or the
raw status+body for other codes.
  - *Alternative considered:* string-only matching (the existing
    `strings.Contains(..., "does not exist")` convention). Kept for the HTTP-body
    case where there is no typed error, but preferred typed `errors.As` for SDK
    errors so it survives message-wording changes.

**3. Message wording follows the repo convention.**
Lowercase, no trailing punctuation, fix in parentheses — e.g.
`AWS credentials are expired or invalid (refresh your env credentials, profile, or SSO session)`.
Guidance goes to the returned error (stderr via `main`'s funnel), never stdout,
so `eval "$(outfit remote start)"` stays clean.

## Risks / Trade-offs

- [Body-marker matching is heuristic; AWS could reword the 403 body] → Anchor on
  stable tokens (`ExpiredToken`, `InvalidClientTokenId`, `security token`) and
  keep the generic permissions hint as the fallback, so a miss degrades to
  today's behaviour, not a blank line.
- [Adding a status-code guard to `Status`/`Stop` could surface previously-hidden
  non-200s as new errors] → That is the intent; those were silent failures. No
  path that returned 200 changes behaviour.
- [Duplicated 403 hint logic in `call` and `callStats`] → Fold both through the
  shared helper so stats and the control calls classify identically.

## Migration Plan

Pure behavioural fix in one internal package; no data, config, or interface
migration. Rollback is reverting the change. Verify by running
`outfit remote status <outfit>` with a deliberately expired token and confirming
a non-zero exit with the refresh message, plus `go test ./... -cover` staying
≥ 80%.

## Open Questions

None.
