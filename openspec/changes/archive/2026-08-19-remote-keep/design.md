## Context

The idle sweep already respects the `Retain-Until` EC2 tag on instances
(`remote/lambda/shared/aws.ts` — `RETAIN_UNTIL_TAG`, `remote/lambda/shared/idle.ts` —
`decideIdle` blocks all automatic action while the tag is in the future). The tag
exists but can only be set manually from the AWS console. The CLI and the
Lambda control plane have no path to set it.

The `tagInstance` helper already exists in `shared/aws.ts` (line 257), used by
the stop Lambda to write the `Stopped-At` tag. The start Lambda
already accepts `env` as a query parameter.

## Goals / Non-Goals

**Goals:**
- Add `outfit remote keep DURATION` that sets the `Retain-Until` tag on a running instance
- Add `outfit remote start --keep DURATION` that sets the tag at wake time
- Report the active retention deadline in `outfit remote status`

**Non-Goals:**
- Removing an existing retention tag (a new tag with a past date has no effect; a `--clear` flag is out of scope)
- Retention for fleet nodes (fleet is local; this is cloud-only)
- Persisting the retention deadline across instance terminations

## Decisions

### Create an UpdateFn Lambda for arbitrary instance commands

**Decision:** A new Lambda (`UpdateFn`) handles arbitrary post-provision instance commands. The first command is `set-keep` (set the `Retain-Until` tag). The Lambda dispatches on a `cmd` query parameter, so future commands (e.g. clearing a tag, setting other instance metadata) extend the same Lambda rather than piggybacking on an unrelated one.

**Rationale:** The stop Lambda owns termination — adding a tagging command there mixes concerns and forces its IAM role to carry `ec2:CreateTags`. A dedicated update Lambda keeps the stop Lambda focused on its existing job and gives a clean extension point for any future instance-level mutation.

**Alternatives considered:**
- Routing through the stop Lambda with `action=keep` — mixes concerns; the stop Lambda is a termination authority, not a general instance mutator.
- A dedicated `keep` Lambda — over-engineered for one command; the dispatch table in `UpdateFn` costs little and pays off when the second command arrives.

### The Go client calls the update Lambda (SigV4), not EC2 directly

**Decision:** The Go client calls the `UpdateFn` Function URL with the command and its parameters. SigV4-signed, like every other control call.

**Rationale:** Keeps the control flow consistent — all remote commands go through Lambda Function URLs with IAM auth. A direct EC2 call would require the CLI to hold EC2 permissions, know the instance ID, and bypass the environment resolution that the Lambda already provides.

### Tag at the end of wake, not during

**Decision:** The start Lambda sets `Retain-Until` after the instance is running and healthy, not at launch time.

**Rationale:** The tag is an override of the *running* idle sweep, not part of instance provisioning. Tagging at the end of wake is idempotent (a repeated `start --keep` on an already-running instance just updates the tag), and it keeps the launch path fast — the tag write is a separate, best-effort EC2 API call.

### Duration parsing in Go, not TypeScript

**Decision:** The CLI parses the duration (Go `time.ParseDuration`), computes the absolute deadline (`time.Now().Add(d)`), and sends the ISO-8601 string.

**Rationale:** The Lambda already expects ISO-8601 dates (that's what the tag format is). Parsing durations in Go reuses a well-tested stdlib function and keeps the Lambda focused on EC2 operations.

### IAM: ec2:CreateTags on the UpdateFn role

**Decision:** The UpdateFn role gets `ec2:CreateTags` (broad ARN — no resource-level scoping available for this action) and `ec2:DescribeInstances` (to resolve the instance by tag before tagging it).

**Rationale:** `ec2:CreateTags` does not support `ec2:ResourceTag` conditions on the target resource (unlike `TerminateInstances`), so the role must grant it broadly. The `DescribeInstances` permission is needed to find the managed instance by tag before tagging it.

## Risks / Trade-offs

- [Tag write races with idle sweep] → The sweep runs every 5 minutes; a tag written just before a sweep might not be read in time. **Mitigation:** The `CreateTags` call is eventually consistent — describe the instance after the tag to confirm it landed. In practice, the 5-minute interval is wide enough that a manual `keep` call will land between sweeps.
- [Instance dies between `keep` resolution and tag write] → The Lambda resolves the instance by tag, so a terminated instance simply returns "not found". **Mitigation:** Report the error clearly — "no running instance to retain".
