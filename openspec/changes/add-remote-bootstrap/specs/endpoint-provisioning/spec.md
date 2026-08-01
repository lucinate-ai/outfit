## ADDED Requirements

### Requirement: Bootstrap provisions the endpoint infrastructure

The system SHALL provide `outfit remote bootstrap`, which stands up the remote
endpoint's AWS infrastructure by obtaining the CDK project shipped in `remote/`
and driving its deploy sequence to completion — `pnpm install`, a one-time
`cdk bootstrap`, the image-pipeline deploy, the AMI bake, the runtime-stack
deploy that generates `remote.json`, and the initial `outfit remote deploy`. The
endpoint `Outfit` is committed in the sources and hand-written — nothing
generates it. Bootstrap SHALL NOT reimplement the infrastructure; it SHALL
orchestrate the existing CDK project. On success it SHALL register the deployment
as a named environment so the other `remote` subcommands can drive it.

#### Scenario: A successful bootstrap yields a usable endpoint config

- **WHEN** `outfit remote bootstrap` completes its deploy sequence
- **THEN** the generated `remote.json` (control URLs, region, and base URL) is
  registered as the target environment, ready for `outfit remote deploy` and
  `outfit remote start`

#### Scenario: Orchestration stops on a failed step

- **WHEN** any step in the sequence fails
- **THEN** bootstrap stops and reports which step failed rather than continuing
  to the next step

### Requirement: Registering the deployment as an environment

Bootstrap SHALL write the generated `remote.json` into the per-user environment
registry defined by the Remote Environments specification —
`~/.config/outfit/remotes/<env>/remote.json` — rather than into a single shared
file, so provisioning a second instance never clobbers the first. The
environment name SHALL come from a `--env` flag, defaulting to `default`. The
consent plan SHALL name the environment and the exact path it will write.

#### Scenario: The deployment is registered under its environment name

- **WHEN** `outfit remote bootstrap --env qwen3.6-27b-prod` succeeds
- **THEN** the generated `remote.json` is written to
  `~/.config/outfit/remotes/qwen3.6-27b-prod/remote.json`, and an Outfit stating
  `REMOTE qwen3.6-27b-prod` resolves to it

#### Scenario: A second instance does not clobber the first

- **WHEN** bootstrap is run for a second environment name
- **THEN** it writes a separate `remotes/<env>/` directory, leaving the first
  environment's `remote.json` intact

### Requirement: Explicit consent before creating AWS resources

Before running any action that creates or modifies AWS resources, bootstrap
SHALL present a plan naming the target AWS account and region, the resources it
will create, the rough cost of running and idle operation, and the exact
commands it will run, then SHALL require explicit confirmation to proceed. A
`--dry-run` flag SHALL print this plan and make no changes. A `--yes` flag SHALL
satisfy the confirmation without an interactive prompt, for non-interactive use.
Absent `--yes` and `--dry-run`, bootstrap SHALL prompt and SHALL treat any
answer other than an explicit yes as a decline that makes no changes.

#### Scenario: The plan is shown before anything is deployed

- **WHEN** the user runs `outfit remote bootstrap`
- **THEN** the account, region, resources, cost, and commands are printed before
  any AWS-mutating command runs

#### Scenario: Dry run changes nothing

- **WHEN** the user runs `outfit remote bootstrap --dry-run`
- **THEN** the plan is printed and no `pnpm`, `cdk`, or AWS-mutating command runs

#### Scenario: Declining stops the run

- **WHEN** the plan is shown and the user does not confirm
- **THEN** bootstrap exits without creating any resources

#### Scenario: Non-interactive consent

- **WHEN** the user runs `outfit remote bootstrap --yes`
- **THEN** bootstrap proceeds without an interactive prompt, having still printed
  the plan

### Requirement: Version-matched CDK sources

Bootstrap SHALL obtain the CDK project by downloading the `remote/` tree from the
project repository at a reference matching the running binary's version, so the
provisioned infrastructure matches the CLI driving it. A `--ref` flag SHALL
override the reference, and a `--dir` flag SHALL override where the sources are
placed (defaulting under the user config directory). For a development build with
no release version, bootstrap SHALL fall back to a documented default reference
rather than guessing. The CDK sources SHALL NOT be embedded in the binary, since
a `pnpm install` is required at runtime regardless.

The default source location SHALL be keyed by the resolved reference, so a
re-run at the same version reuses its sources while a different binary version
downloads fresh rather than reusing a mismatched cache. On a successful
bootstrap using the default location, sources from other references SHALL be
pruned so stale-version copies do not accumulate. An explicit `--dir` SHALL be
treated as the user's own location: neither keyed by reference nor pruned.

#### Scenario: Sources match the binary version

- **WHEN** a released `outfit` runs bootstrap with no `--ref`
- **THEN** it downloads the `remote/` sources at the tag matching its version

#### Scenario: A new version does not reuse stale sources

- **WHEN** bootstrap runs from a binary whose resolved reference differs from a
  previously downloaded one in the default location
- **THEN** it downloads sources for the new reference rather than reusing the old
  ones, and on success the superseded reference's sources are pruned

#### Scenario: An explicit directory is left alone

- **WHEN** the user passes `--dir`
- **THEN** bootstrap uses that path as given, without reference-keying or pruning
  it

#### Scenario: Development build falls back

- **WHEN** a `dev` build runs bootstrap with no `--ref`
- **THEN** it uses the documented fallback reference rather than failing or
  guessing a version

#### Scenario: Reference override

- **WHEN** the user passes `--ref <ref>`
- **THEN** bootstrap downloads the sources at that reference

### Requirement: Preflight checks before deploying

Bootstrap SHALL verify its prerequisites before presenting the plan and fail
early with actionable guidance when one is missing: a suitable Node runtime and
`pnpm` on the path, and resolvable AWS credentials. It SHALL report the resolved
AWS account and region so they appear in the consent plan. It SHALL surface the
GPU vCPU quota as a warning when it cannot be confirmed, without attempting to
raise it, since a later `start` depends on it.

#### Scenario: Missing tooling fails early

- **WHEN** `pnpm` is not on the path
- **THEN** bootstrap fails before deploying, naming the missing prerequisite

#### Scenario: Unresolvable credentials fail early

- **WHEN** AWS credentials cannot be resolved
- **THEN** bootstrap fails before deploying, explaining how to provide them

#### Scenario: Quota is a warning, not a blocker

- **WHEN** the GPU vCPU quota cannot be confirmed sufficient
- **THEN** bootstrap warns that a later `start` may fail until the quota is
  raised, and continues

### Requirement: Required settings are collected

Bootstrap SHALL collect the two settings the CDK has no default for — the
allowed CIDR (the caller's public address as a `/32`) and the runner
(`llamacpp` by default, or `vllm`) — and SHALL write them where the CDK reads
them so the deploy picks them up. The allowed CIDR SHALL be provided by a flag or
detected from the caller's public address; the runner SHALL be selectable by a
flag.

#### Scenario: Runner defaults to llamacpp

- **WHEN** the user runs bootstrap without selecting a runner
- **THEN** the deploy is configured for the `llamacpp` runner

#### Scenario: Allowed CIDR is recorded for the deploy

- **WHEN** bootstrap resolves the allowed CIDR
- **THEN** it is written where the CDK reads it before the deploy runs

### Requirement: Resumable orchestration with an asynchronous bake

Bootstrap SHALL be safe to re-run: it SHALL skip steps already satisfied, not
repeating `pnpm install` when dependencies are present nor `cdk bootstrap` when
the account and region are already bootstrapped, and SHALL not redeploy a stack
that is unchanged. Because the AMI bake is slow, by default bootstrap SHALL start
the bake and hand off, telling the user how to wait for it and how to resume,
rather than blocking. A `--wait` flag SHALL block until the bake completes.

#### Scenario: Re-running skips satisfied steps

- **WHEN** bootstrap is re-run after a partial completion
- **THEN** it skips installation and CDK bootstrap that are already done rather
  than repeating them

#### Scenario: The slow bake does not block by default

- **WHEN** bootstrap reaches the AMI bake without `--wait`
- **THEN** it starts the bake and reports how to wait for it and resume, rather
  than blocking for the full bake duration

#### Scenario: Waiting on request

- **WHEN** the user passes `--wait`
- **THEN** bootstrap blocks until the bake completes before finishing
