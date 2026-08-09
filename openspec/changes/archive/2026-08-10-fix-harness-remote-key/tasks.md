## 1. Fetch before apply

- [x] 1.1 Move the remote env fetch into `applyBeforeLaunch`, ahead of the apply, and return the response alongside the selection.
- [x] 1.2 Add `remoteLaunchResolver`: the local lookup widened with the fetched key, for the API key variable only and only where nothing local answers.
- [x] 1.3 Give `applySelection` the resolver as a parameter; `add` and `apply` pass the plain `opencode.EnvResolver`, so their behaviour is unchanged.
- [x] 1.4 Use the same resolver for the launch environment and for `lucinateLaunchKey`, so a remote Outfit under `-H lucinate` gets `LUCINATE_OPENAI_API_KEY`.

## 2. Report the fetch

- [x] 2.1 Announce the fetch on stderr, naming the remote.
- [x] 2.2 Bound the call with a 30s timeout.
- [x] 2.3 Fail — before the apply writes anything — when the fetch fails and no key is available from the environment, the adjacent `.env`, or an `ENV` instruction; the error names the remote, the cause, and both remedies.
- [x] 2.4 Warn and continue when a key is available.

## 3. Eval-safe stdout

- [x] 3.1 Write `readOutfit`'s alias line to stderr.
- [x] 3.2 Name the endpoint variables once (`remoteBaseURLEnv`/`remoteAPIKeyEnv`) so the export lines and the injected environment cannot drift.

## 4. Tests

- [x] 4.1 The apply does not warn when the fetch supplies the key, and the key reaches the launched environment.
- [x] 4.2 A failed fetch with no key available fails the launch, names both remedies, and leaves the harness config unwritten.
- [x] 4.3 A failed fetch with an exported key, and with an `ENV` key, warns and carries on.
- [x] 4.4 The fetch is announced on stderr, not stdout.
- [x] 4.5 An Outfit with no `REMOTE` contacts nothing and reports nothing.
- [x] 4.6 `remoteLaunchResolver` leaves a local value and unrelated variables alone.
- [x] 4.7 Every stdout line of `outfit remote env <alias>` is an export line.
- [x] 4.8 `TestApply_ByAlias` asserts the alias line is on stderr and absent from stdout.

## 5. Verify

- [x] 5.1 `gofmt -l` clean, `go vet ./...`, `go test ./...` green.
- [x] 5.2 Manual: `outfit harness dev-1` against the real endpoint reports the key is read from the environment, with no missing-key warning, and writes a byte-identical config.
- [x] 5.3 Manual: `eval "$(outfit remote env dev-1)"` sets both variables with no shell error.
