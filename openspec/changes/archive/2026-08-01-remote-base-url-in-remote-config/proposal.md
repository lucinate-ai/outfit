## Why

The `remote/` deployment generated the Outfit that drives it, substituting the
endpoint's address into a `BASEURL` line from an `Outfit.example` template. That
made a user-facing, hand-editable file into build output: edits landed in the
template rather than the file people actually read, the generated `Outfit` had
to stay untracked because it carried a deployment address, and one file mixed
two things — what to serve (a user's choice) with where it lives (deployment
state). The address belongs with the other deployment state in `remote.json`.

## What Changes

- The remote deployment SHALL no longer generate an Outfit. `remote/Outfit`
  becomes a committed, hand-maintained file that states no `BASEURL`, and the
  `Outfit.example` template is removed.
- The generated `remote.json` gains `base_url`, the endpoint's own address,
  alongside the control URLs and region it already carries. It stays optional:
  no control call needs it, so existing configs keep working.
- `apply` fills a selection's base URL from the remote config named by an
  Outfit's `REMOTE` when the Outfit states no `BASEURL`. A `BASEURL` written in
  the Outfit takes precedence, and a remote config that does not exist yet is
  not an error — the deployment that writes it may not have run.
- Not changed: `serve` still derives its bind address from `BASEURL` alone. A
  remote endpoint's address is not something to bind to.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `remote-endpoint`: "Remote configuration discovery" — the remote
  configuration also carries the endpoint's own base URL, optional because the
  control calls do not use it.
- `provider-selection`: "Base URL precedence" — a remote configuration named by
  an Outfit joins the precedence chain, below anything the user stated
  explicitly.

## Impact

- **Code**: `internal/remote` (`Config.BaseURL`), `cmd/outfit/remote.go`
  (`remoteBaseURL`, `remoteConfigPath` now taking a directory),
  `cmd/outfit/main.go` (`applySelection` fills the base URL from the remote
  config).
- **`remote/`**: `Outfit` committed and `Outfit.example` deleted;
  `scripts/write-config.mjs` writes only `remote.json`; the CDK
  `OutfitRemoteConfig` output carries `base_url`.
- **Docs**: `AGENTS.md`, `README.md`, `docs/outfit-file.md`,
  `docs/commands/apply.md`, `docs/commands/remote.md`, `remote/README.md`.
- **Compatibility**: no breaking change. An Outfit with a `BASEURL` behaves
  exactly as before; a `remote.json` without `base_url` leaves the base URL to
  the catalogue, as it does today.
