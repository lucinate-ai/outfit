## Context

`remote/`'s `pnpm write-config` generated two files from the CDK stack outputs:
`remote.json` (the control URLs) and `Outfit` (from an `Outfit.example`
template, with the endpoint's Elastic IP substituted into a `BASEURL` line).
Both were gitignored, because the repository is public and an address is a
cloud identifier.

That put a user-facing file on the generated side of the line. The Outfit says
what to serve — engine, alias, context, preset — which is a set of choices
someone makes and edits. Only the address was deployment state, and it was the
sole reason the file could not be committed.

The address is still needed: `outfit apply` writes it into the harness config,
which is how a coding agent reaches the endpoint.

## Goals / Non-Goals

**Goals:**

- `remote/Outfit` is committed, hand-maintained, and states no address.
- The address travels with the rest of the deployment state, in `remote.json`.
- `outfit apply` keeps pointing a harness at the endpoint with no extra step.
- Anything the user wrote by hand still wins over anything generated.

**Non-Goals:**

- Changing how `outfit serve` derives its bind address (still `BASEURL` only).
- Making the control calls depend on the recorded address — `start` and
  `status` report the live one, and that stays the truth.
- A per-user `~/.config/outfit/remote.json` gaining any new meaning; it can
  carry `base_url` too, on exactly the same terms.

## Decisions

**The address goes in `remote.json`, not a new file.** It is deployment state
produced by the same deploy step, consumed through the same `REMOTE`
instruction that already ties an Outfit to its endpoint. A second generated
file would need its own discovery rules for no gain.

**`base_url` is optional, like `deploy_url`.** No control call reads it, so a
configuration written before this change stays valid and `LoadConfigFile` does
not learn a new way to fail. Alternative — validating it — would break existing
setups for a value the control path never uses.

**`apply` fills the base URL; `serve` does not.** They read the same Outfit for
opposite purposes: `apply` points a client at a server, `serve` binds one
locally. Feeding a remote address into `serve`'s bind flags would produce a
server that cannot start. So the lookup lives in `applySelection`, not in
`readOutfit` — the choke point all Outfit commands share, and therefore the
tempting but wrong place for it.

**A stated `BASEURL` wins.** The generated value is a fallback for the common
case, not an override. An Outfit is user-managed; anything written in it beats
anything derived. Mechanically the remote value lands in the same slot as the
`--base-url` flag, so it also beats `OUTFIT_BASE_URL` — consistent with an
Outfit's own `BASEURL`, which has always behaved that way.

**A missing remote config is not an error here.** `outfit remote` demands the
file, because a control call without URLs cannot proceed. `apply` can: it is
configuring a harness, not talking to the endpoint, and an Outfit may name a
`remote.json` the deployment has not written yet. It reads the file leniently
and moves on when there is nothing to read.

**Applying says where the address came from.** A base URL appearing from a file
the user did not edit is otherwise unexplained; the harness already reports the
value, so one line naming the source is enough.

## Risks / Trade-offs

- A stale `remote.json` silently configures the harness with an old address →
  the address only changes when the Elastic IP is replaced, which means a
  redeploy, which rewrites `remote.json`. `outfit remote status` reports the
  live address for comparison.
- The base URL now sits in a file whose name suggests control URLs → the field
  is documented in `docs/commands/remote.md`, the README, and the `Config`
  type's comment, each saying what it is for and that the control calls do not
  use it.
- Someone re-running an old `write-config` against fresh outputs would produce
  a `remote.json` without `base_url` → the script keeps a fallback that reads
  the stack's separate `BaseUrl` output, so outputs from before this change
  still yield a complete config.

## Migration Plan

`pnpm run deploy` (or `pnpm write-config` against an existing
`cdk-outputs.json`) rewrites `remote.json` with `base_url` — no manual step. An
existing local `remote/Outfit` is replaced by the committed one; a user who had
edited theirs should re-apply those edits, which is the point of the file being
tracked from now on.
