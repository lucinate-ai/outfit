# Endpoint Provisioning Specification

## Purpose

Define how the account-level AWS control plane for cloud inference
endpoints is provisioned through `spinloop cloud bootstrap`.

## Requirements

### Requirement: Bootstrap deploys the control plane

The system SHALL provide `spinloop cloud bootstrap`, which deploys the
account-level control plane that every cloud environment reuses — the EC2
Image Builder pipelines, the environment-aware lifecycle Lambdas and their IAM,
and the shared S3 weights bucket, IAM roles and VPC, and the IAM user that
holds the long-lived control-plane credential (see the Cloud Auth
specification) together with its policy — by obtaining the CDK project shipped
in `remote/` and driving its deploy of the control-plane stack. Bootstrap SHALL
NOT start any AMI bake; the bake is a separate
`spinloop cloud bake` step. Bootstrap SHALL NOT create any Elastic IP or EC2
instance, and SHALL NOT register an environment; those belong to
`spinloop cloud deploy`. Bootstrap SHALL NOT reimplement the infrastructure;
it SHALL orchestrate the existing CDK project. On success, bootstrap SHALL
signpost `spinloop cloud bake` as the next step, ahead of
`spinloop cloud deploy`.

#### Scenario: A successful bootstrap yields the control plane

- **WHEN** `spinloop cloud bootstrap` completes
- **THEN** the control-plane stack is deployed — Image Builder pipelines, the
  lifecycle Lambdas, and the shared bucket/roles/VPC — with no Elastic IP or
  instance created and no AMI bake started

#### Scenario: The control-plane credential user is deployed

- **WHEN** `spinloop cloud bootstrap` completes
- **THEN** the control-plane IAM user exists with a policy covering the
  day-to-day cloud commands only — invoking the control URLs, reading the
  control-plane log groups, describing the control-plane stack, and managing
  its own access keys — and no permission to deploy, bake, or otherwise
  provision AWS resources

#### Scenario: Bootstrap signposts the bake

- **WHEN** `spinloop cloud bootstrap` completes
- **THEN** its output names `spinloop cloud bake` as the next step, ahead of
  `spinloop cloud deploy`

#### Scenario: Orchestration stops on a failed step

- **WHEN** any step in the sequence fails
- **THEN** bootstrap stops and reports which step failed rather than continuing

### Requirement: The control plane is discoverable

The control-plane stack SHALL publish, as CloudFormation stack outputs under a
well-known stack name, the values a later `spinloop cloud deploy` needs to create
and drive environments: the lifecycle Lambda URLs, the weights bucket, the shared
roles, and the region. Discovery SHALL be from those outputs rather than a file
bootstrap writes, so it reflects what is actually deployed and works from any
machine with account access.

#### Scenario: Deploy can discover the control plane

- **WHEN** the control-plane stack is deployed and `spinloop cloud deploy` runs later
- **THEN** it reads the Lambda URLs, bucket, roles and region from the stack's
  outputs, without a local file having to carry them

### Requirement: Explicit consent before creating AWS resources

Before running any action that creates or modifies AWS resources, bootstrap SHALL
present a plan naming the target AWS account and region, the control-plane resources it
will create, a qualitative cost caveat, and the exact commands it will run, then
SHALL require explicit confirmation. A `--dry-run` flag SHALL print this plan and
make no changes. A `--yes` flag SHALL satisfy the confirmation non-interactively.
Absent `--yes` and `--dry-run`, bootstrap SHALL prompt and SHALL treat any answer
other than an explicit yes as a decline that makes no changes.

#### Scenario: The plan is shown before anything is deployed

- **WHEN** the user runs `spinloop cloud bootstrap`
- **THEN** the account, region, control-plane resources, cost caveat, and commands are
  printed before any AWS-mutating command runs

#### Scenario: Dry run changes nothing

- **WHEN** the user runs `spinloop cloud bootstrap --dry-run`
- **THEN** the plan is printed and no package-manager, `cdk`, or AWS-mutating
  command runs

#### Scenario: Declining stops the run

- **WHEN** the plan is shown and the user does not confirm
- **THEN** bootstrap exits without creating any resources

### Requirement: Version-matched CDK sources

Bootstrap and `spinloop cloud bake` SHALL obtain the CDK project by
downloading the `remote/` tree from the project repository at a reference
matching the running binary's version, so the infrastructure matches the CLI
driving it. A `--ref` flag SHALL override the reference, and a `--dir` flag
SHALL override where the sources are placed (defaulting under the user config
directory). For a development build with no release version, bootstrap and
bake SHALL fall back to a documented default reference. The CDK sources SHALL
NOT be embedded in the binary, since a package-manager install is required at
runtime regardless.

The default source location SHALL be keyed by the resolved reference, so a re-run
at the same version reuses its sources while a different binary version downloads
fresh. On a successful bootstrap or bake using the default location, sources
from other references SHALL be pruned. An explicit `--dir` SHALL be treated as
the user's own location: neither keyed by reference nor pruned.

#### Scenario: Sources match the binary version

- **WHEN** a released `spinloop` runs bootstrap with no `--ref`
- **THEN** it downloads the `remote/` sources at the tag matching its version

#### Scenario: Development build falls back

- **WHEN** a `dev` build runs bootstrap with no `--ref`
- **THEN** it uses the documented fallback reference rather than guessing

#### Scenario: A new version does not reuse stale sources

- **WHEN** bootstrap or bake runs from a binary whose resolved reference
  differs from a previously downloaded one in the default location
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
is already bootstrapped (the control-plane stack exists, via CloudFormation
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

- **WHEN** the control-plane stack already exists in the account and region
- **THEN** bootstrap notes it will update the control plane, and
  continues (the deploy is idempotent)

### Requirement: A Node package manager is selected, overridable, and logged

Bootstrap and `spinloop cloud bake` SHALL select the Node package manager they
drive the CDK project with. Absent an explicit choice, they SHALL auto-detect by
PATH lookup, preferring `pnpm` and falling back to `npm` when `pnpm` is not on
the path. The user MAY override
the selection with a `--package-manager` flag or an `SPINLOOP_CLOUD_PACKAGE_MANAGER`
environment variable, whose only accepted values are `pnpm` and `npm`; the flag
SHALL take precedence over the environment variable, which SHALL take precedence
over auto-detection. An unrecognised override value SHALL be rejected with an
error naming the accepted values. The selected manager SHALL be used consistently
for every Node step (install, `cdk`, `deploy:image`, `deploy`) and
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
  `SPINLOOP_CLOUD_PACKAGE_MANAGER=npm`) while `pnpm` is also present
- **THEN** bootstrap uses `npm` regardless of auto-detection, and the flag wins
  if both the flag and the environment variable are set

#### Scenario: An unrecognised override is rejected

- **WHEN** the override value is neither `pnpm` nor `npm`
- **THEN** bootstrap fails with an error naming the accepted values, before
  deploying anything

### Requirement: AMI bake is a separate command

The system SHALL provide `spinloop cloud bake`, which starts an AMI bake for
each runner named as a positional argument — `llamacpp` and `vllm` — defaulting
to both when none are named. It SHALL drive the same CDK project that
bootstrap orchestrates, with the same version-matched source download into the
same ref-keyed default location, the same package-manager selection and
override, and `--ref` and `--dir` flags matching bootstrap's. Bake SHALL NOT
deploy any stack; when the control-plane stack is not deployed, it SHALL fail
before starting any bake, naming `spinloop cloud bootstrap` as the step to run
first. Bake SHALL block until every requested runner's AMI is available; a
`--no-wait` flag SHALL return as soon as the bakes are queued, reporting how to
check on them, rather than blocking for the bake duration.

#### Scenario: Default bake covers both runners

- **WHEN** the user runs `spinloop cloud bake` with no arguments
- **THEN** a bake is started for both `llamacpp` and `vllm`

#### Scenario: A single runner is baked

- **WHEN** the user runs `spinloop cloud bake llamacpp`
- **THEN** only the `llamacpp` AMI bake is started

#### Scenario: An unknown runner is rejected

- **WHEN** the user names a runner that is neither `llamacpp` nor `vllm`
- **THEN** bake fails before starting any bake, naming the accepted runners

#### Scenario: No control plane

- **WHEN** the control-plane stack is not deployed and bake runs
- **THEN** it fails before starting any bake, saying to run
  `spinloop cloud bootstrap` first

#### Scenario: Bake waits by default

- **WHEN** the user runs `spinloop cloud bake` without `--no-wait`
- **THEN** the command blocks until the requested runners' AMIs are available
  before finishing

#### Scenario: Handing off with --no-wait

- **WHEN** the user passes `--no-wait`
- **THEN** the command returns as soon as the bakes are queued, reporting how
  to check on them, rather than blocking for the bake duration

### Requirement: Idempotent bootstrap

Bootstrap SHALL be safe to re-run: it SHALL skip the package-manager install when
dependencies are present and `cdk bootstrap` when the account and region are already
bootstrapped, and SHALL not redeploy a control-plane stack that is unchanged. Because it
touches only control plane and never a live instance, re-running SHALL NOT
require any override.

#### Scenario: Re-running skips satisfied steps

- **WHEN** bootstrap is re-run
- **THEN** it skips installation and CDK bootstrap that are already done and
  no-ops the unchanged control-plane stack, without requiring an override

### Requirement: Bootstrap collects only the shared-secret token

Bootstrap SHALL collect the one control-plane setting the CDK has no default
for and write it where the CDK reads it: an optional Hugging Face token for
the shared secret used when seeding gated weights. Which runner AMIs to bake
is not a bootstrap setting — the engine is a per-environment choice made at
`deploy`, and the runners are named by `spinloop cloud bake` itself. The
allowed ingress CIDR is also per-environment and belongs to `deploy`, not here.

#### Scenario: Runners are not a bootstrap setting

- **WHEN** the user runs bootstrap
- **THEN** no runner selection is requested or written, since the runners are
  named at `spinloop cloud bake`

#### Scenario: The allowed CIDR is not a bootstrap setting

- **WHEN** the user runs bootstrap
- **THEN** no ingress CIDR is requested or written, since it is scoped per
  environment at `spinloop cloud deploy`

### Requirement: Bootstrap records the deploying CLI's version

`spinloop cloud bootstrap` SHALL pass the running binary's version to the control plane deploy, so every control plane Lambda reports it in the `x-spinloop-control-plane-version` response header. A binary built without a version SHALL record `dev`.

#### Scenario: A release build bootstraps

- **WHEN** a CLI at version `1.30.0` runs `spinloop cloud bootstrap`
- **THEN** the deployed Lambdas report `1.30.0` as the control plane version

#### Scenario: A development build bootstraps

- **WHEN** a CLI built without a version override runs `spinloop cloud bootstrap`
- **THEN** the deployed Lambdas report `dev`
