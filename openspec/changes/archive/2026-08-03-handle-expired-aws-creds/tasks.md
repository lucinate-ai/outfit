## 1. Credential-error classification

- [x] 1.1 Add a helper in `internal/remote` that classifies an expired/invalid credential condition from a smithy `APIError` via `errors.As`, matching codes `ExpiredToken`, `ExpiredTokenException`, `InvalidClientTokenId`, `RequestExpired`, `UnrecognizedClientException`.
- [x] 1.2 Extend the helper to recognise the condition from an HTTP 403 response body, matching stable markers (`ExpiredToken`, `InvalidClientTokenId`, "security token" + "expired", `RequestExpired`).
- [x] 1.3 Have the helper return the actionable message (lowercase, fix in parentheses, e.g. `AWS credentials are expired or invalid (refresh your env credentials, profile, or SSO session)`), and leave non-matching errors unchanged.

## 2. Surface non-success control replies

- [x] 2.1 Route non-200 replies in `call` (and `callStats`) through a shared `statusError` helper that applies the credential classification, falling back to the existing `lambda:InvokeFunctionUrl` permissions hint for other 403s and to status+truncated-body otherwise.
- [x] 2.2 Add the missing `resp.StatusCode != http.StatusOK` guard to `Status` and `Stop` so they error via `statusError` instead of returning a blank `Response`, mirroring `Deploy`.
- [x] 2.3 Confirm `waitReady`'s explicit HTTP 503 "still starting" handling runs ahead of the new guard so its behaviour is unchanged; keep `Deploy`/`Stats` building their `Error`/`Errors` detail.
- [x] 2.4 Apply the classifier to the `Credentials.Retrieve` failure path in `sign` so an expired SSO/token that fails at retrieval gets the refresh message too.

## 3. Tests

- [x] 3.1 Add a test where `status` receives an HTTP 403 expired-token JSON body and assert it returns a non-nil error (non-zero exit) with the refresh message, not a blank state.
- [x] 3.2 Add the equivalent test for `stop`, and a case asserting a generic 403 still yields the permissions hint.
- [x] 3.3 Add a `stats` case asserting an expired-credential 403 is classified with the refresh message.

## 4. Verify

- [x] 4.1 Run `gofmt`, `go build ./...`, and `go test ./... -cover`, keeping coverage ≥ 80%.
- [x] 4.2 Update `openspec/specs/remote-endpoint/spec.md` behaviour by validating the change (`openspec validate --change handle-expired-aws-creds`).
