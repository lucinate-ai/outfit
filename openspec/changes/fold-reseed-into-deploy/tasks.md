## 1. The Lambda

- [x] 1.1 Parse an optional boolean `reseed` from the raw request body in
  `remote/lambda/deploy/index.ts`, beside `allowedCidr`, rejecting a
  non-boolean value with a 400 — deliberately NOT via `parseDeployConfig`, so
  it can never reach the persisted deploy-config
- [x] 1.2 Change the seed guard to
  `if (reseed || !(await weightsPresent(...)))`, leaving `launchSeedInstance`,
  the 502 path and the `seeding`/`seedInstanceId` reply fields untouched
- [x] 1.3 Include `reseed` in the deploy log line, so a forced seed is
  distinguishable from an automatic one in CloudWatch
- [x] 1.4 Test: reseed with weights present starts a seed; reseed with weights
  absent starts exactly one; no reseed with weights present starts none; a
  non-boolean `reseed` is a 400; and `reseed` does not appear in the config
  written to SSM

## 2. The `outfit` client

- [x] 2.1 Add `reseed bool` to `remote.Deploy`'s request struct in
  `internal/remote/remote.go` (`json:"reseed,omitempty"`), beside
  `allowedCidr` — not on `DeployConfig`
- [x] 2.2 Add the `--reseed` flag to `cmdRemoteDeploy` and pass it through,
  with help text saying it re-fetches weights already in S3 and costs a seed
- [x] 2.3 Show the intent in `--dry-run` output, so `--reseed --dry-run` says a
  re-seed would be requested rather than looking identical to a plain deploy
- [x] 2.4 **Not applicable.** `docs/openapi.yaml` describes the *daemon's*
  control API, not the deploy Lambda's Function URL, and `reseed` is
  deliberately not on `DeployConfig`. Adding it to that schema would assert the
  daemon accepts a field it does not, and break the Go-parity contract test
- [x] 2.5 Test the flag reaches the request body, and that its absence omits
  the field entirely

## 3. Removing the script

- [x] 3.1 Delete `remote/scripts/seed-model.mts`
- [x] 3.2 Remove the `seed-model` entry from `remote/package.json`
- [x] 3.3 Replace the script's mention in `remote/README.md` ("Force a re-seed
  of weights already in S3") with `outfit remote deploy --reseed`
- [x] 3.4 Update the `scripts/seed-model.mts` row in
  `remote/docs/architecture.md`
- [x] 3.5 Check nothing else references the script or `pnpm seed-model`
  (including `AGENTS.md` and `docs/`)

## 4. Verification

- [x] 4.1 `pnpm -C remote test` and `npx tsc --noEmit` clean
- [x] 4.2 Full Go suite clean, including the OpenAPI contract test
- [x] 4.3 `outfit remote deploy --reseed --dry-run` against the Glimmer example
  shows the re-seed intent and the unchanged config
