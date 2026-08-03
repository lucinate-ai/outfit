## ADDED Requirements

### Requirement: A Node package manager is selected, overridable, and logged

Bootstrap SHALL select the Node package manager it drives the CDK project with.
Absent an explicit choice, it SHALL auto-detect by PATH lookup, preferring `pnpm`
and falling back to `npm` when `pnpm` is not on the path. The user MAY override
the selection with a `--package-manager` flag or an `OUTFIT_REMOTE_PACKAGE_MANAGER`
environment variable, whose only accepted values are `pnpm` and `npm`; the flag
SHALL take precedence over the environment variable, which SHALL take precedence
over auto-detection. An unrecognised override value SHALL be rejected with an
error naming the accepted values. The selected manager SHALL be used consistently
for every Node step (install, `cdk`, `deploy:image`, `bake`, `deploy`) and
reflected in the printed plan. Before the steps run, bootstrap SHALL log which
package manager it selected, so the run is self-explanatory. When auto-detecting
and both managers are present, `pnpm` SHALL win; the choice SHALL NOT depend on
which lockfiles are present, since the `remote/` project ships a `pnpm` lockfile
yet runs correctly under either manager.

#### Scenario: pnpm is preferred when auto-detecting

- **WHEN** no override is given and both `pnpm` and `npm` are on the path
- **THEN** bootstrap selects `pnpm`, logs that it is using `pnpm`, and runs every
  step with `pnpm`

#### Scenario: npm is used when pnpm is absent

- **WHEN** no override is given, `pnpm` is not on the path, but `npm` is
- **THEN** bootstrap selects `npm`, logs that it is using `npm`, and runs every
  step with `npm` using npm's argument conventions

#### Scenario: An explicit override is honoured

- **WHEN** the user passes `--package-manager npm` (or sets
  `OUTFIT_REMOTE_PACKAGE_MANAGER=npm`) while `pnpm` is also present
- **THEN** bootstrap uses `npm` regardless of auto-detection, and the flag wins
  if both the flag and the environment variable are set

#### Scenario: An unrecognised override is rejected

- **WHEN** the override value is neither `pnpm` nor `npm`
- **THEN** bootstrap fails with an error naming the accepted values, before
  deploying anything

## MODIFIED Requirements

### Requirement: Version-matched CDK sources

Bootstrap SHALL obtain the CDK project by downloading the `remote/` tree from the
project repository at a reference matching the running binary's version, so the
infrastructure matches the CLI driving it. A `--ref` flag SHALL override the
reference, and a `--dir` flag SHALL override where the sources are placed
(defaulting under the user config directory). For a development build with no
release version, bootstrap SHALL fall back to a documented default reference. The
CDK sources SHALL NOT be embedded in the binary, since a package-manager install
is required at runtime regardless.

The default source location SHALL be keyed by the resolved reference, so a re-run
at the same version reuses its sources while a different binary version downloads
fresh. On a successful bootstrap using the default location, sources from other
references SHALL be pruned. An explicit `--dir` SHALL be treated as the user's own
location: neither keyed by reference nor pruned.

#### Scenario: Sources match the binary version

- **WHEN** a released `outfit` runs bootstrap with no `--ref`
- **THEN** it downloads the `remote/` sources at the tag matching its version

#### Scenario: Development build falls back

- **WHEN** a `dev` build runs bootstrap with no `--ref`
- **THEN** it uses the documented fallback reference rather than guessing

#### Scenario: A new version does not reuse stale sources

- **WHEN** bootstrap runs from a binary whose resolved reference differs from a
  previously downloaded one in the default location
- **THEN** it downloads sources for the new reference, and on success the
  superseded reference's sources are pruned

### Requirement: Preflight checks before deploying

Bootstrap SHALL verify its prerequisites before presenting the plan and fail
early with actionable guidance when one is missing: a suitable Node runtime, a
supported package manager (`pnpm` or `npm`) on the path, and resolvable AWS
credentials. When the user has pinned a package manager (via the flag or the
environment variable), the preflight SHALL require that specific manager on the
path; otherwise it SHALL require at least one of `pnpm` or `npm`. It SHALL report
the resolved AWS account and region for the plan, and SHALL note when the account
is already bootstrapped (the shared stack exists, via CloudFormation
`DescribeStacks`). It SHALL surface the GPU vCPU quota as a warning when it cannot
be confirmed, without attempting to raise it.

#### Scenario: Missing tooling fails early

- **WHEN** neither `pnpm` nor `npm` is on the path
- **THEN** bootstrap fails before deploying, naming the missing prerequisite

#### Scenario: A pinned manager that is not installed fails early

- **WHEN** the user pins a manager that is not on the path
- **THEN** bootstrap fails before deploying, naming the pinned manager, rather
  than silently falling back to the other

#### Scenario: Unresolvable credentials fail early

- **WHEN** AWS credentials cannot be resolved
- **THEN** bootstrap fails before deploying, explaining how to provide them

#### Scenario: Already bootstrapped is noted, not fatal

- **WHEN** the shared stack already exists in the account and region
- **THEN** bootstrap notes it will update the shared infrastructure, and
  continues (the deploy is idempotent)

### Requirement: Explicit consent before creating AWS resources

Before running any action that creates or modifies AWS resources, bootstrap SHALL
present a plan naming the target AWS account and region, the shared resources it
will create, a qualitative cost caveat, and the exact commands it will run, then
SHALL require explicit confirmation. A `--dry-run` flag SHALL print this plan and
make no changes. A `--yes` flag SHALL satisfy the confirmation non-interactively.
Absent `--yes` and `--dry-run`, bootstrap SHALL prompt and SHALL treat any answer
other than an explicit yes as a decline that makes no changes.

#### Scenario: The plan is shown before anything is deployed

- **WHEN** the user runs `outfit remote bootstrap`
- **THEN** the account, region, shared resources, cost caveat, and commands are
  printed before any AWS-mutating command runs

#### Scenario: Dry run changes nothing

- **WHEN** the user runs `outfit remote bootstrap --dry-run`
- **THEN** the plan is printed and no package-manager, `cdk`, or AWS-mutating
  command runs

#### Scenario: Declining stops the run

- **WHEN** the plan is shown and the user does not confirm
- **THEN** bootstrap exits without creating any resources

### Requirement: Idempotent bootstrap with an asynchronous bake

Bootstrap SHALL be safe to re-run: it SHALL skip the package-manager install when
dependencies are present and `cdk bootstrap` when the account and region are
already bootstrapped, and SHALL not redeploy a shared stack that is unchanged.
Because it touches only shared infrastructure and never a live instance,
re-running SHALL NOT require any override. Because the AMI bake is slow, by
default bootstrap SHALL start the bake and hand off, telling the user how to wait,
rather than blocking. A `--wait` flag SHALL block until the bake completes.

#### Scenario: Re-running skips satisfied steps

- **WHEN** bootstrap is re-run
- **THEN** it skips installation and CDK bootstrap that are already done and
  no-ops the unchanged shared stack, without requiring an override

#### Scenario: The slow bake does not block by default

- **WHEN** bootstrap reaches the AMI bake without `--wait`
- **THEN** it starts the bake and reports how to wait for it, rather than
  blocking for the full bake duration

#### Scenario: Waiting on request

- **WHEN** the user passes `--wait`
- **THEN** bootstrap blocks until the bake completes before finishing
