# Endpoint Provisioning Specification

## Purpose

Define how shared, account-level AWS infrastructure for remote inference
endpoints is provisioned through `outfit remote bootstrap`.

## Requirements

### Requirement: Bootstrap deploys the shared account infrastructure

The system SHALL provide `outfit remote bootstrap`, which deploys the shared,
account-level infrastructure that every remote environment reuses — the EC2
Image Builder pipelines and baked AMIs, the environment-aware lifecycle Lambdas
and their IAM, and the shared S3 weights bucket, IAM roles and VPC — by obtaining
the CDK project shipped in `remote/` and driving its deploy of the shared stack.
Bootstrap SHALL NOT create any Elastic IP or EC2 instance, and SHALL NOT register
an environment; those belong to `outfit remote deploy`. Bootstrap SHALL NOT
reimplement the infrastructure; it SHALL orchestrate the existing CDK project.

#### Scenario: A successful bootstrap yields the shared layer

- **WHEN** `outfit remote bootstrap` completes
- **THEN** the shared stack is deployed — Image Builder, the lifecycle Lambdas,
  and the shared bucket/roles/VPC — with no Elastic IP or instance created

#### Scenario: Orchestration stops on a failed step

- **WHEN** any step in the sequence fails
- **THEN** bootstrap stops and reports which step failed rather than continuing

### Requirement: Shared infrastructure is discoverable

The shared stack SHALL publish, as CloudFormation stack outputs under a
well-known stack name, the values a later `outfit remote deploy` needs to create
and drive environments: the lifecycle Lambda URLs, the weights bucket, the shared
roles, and the region. Discovery SHALL be from those outputs rather than a file
bootstrap writes, so it reflects what is actually deployed and works from any
machine with account access.

#### Scenario: Deploy can discover the shared layer

- **WHEN** the shared stack is deployed and `outfit remote deploy` runs later
- **THEN** it reads the Lambda URLs, bucket, roles and region from the stack's
  outputs, without a local file having to carry them

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
- **THEN** the plan is printed and no `pnpm`, `cdk`, or AWS-mutating command runs

#### Scenario: Declining stops the run

- **WHEN** the plan is shown and the user does not confirm
- **THEN** bootstrap exits without creating any resources

### Requirement: Version-matched CDK sources

Bootstrap SHALL obtain the CDK project by downloading the `remote/` tree from the
project repository at a reference matching the running binary's version, so the
infrastructure matches the CLI driving it. A `--ref` flag SHALL override the
reference, and a `--dir` flag SHALL override where the sources are placed
(defaulting under the user config directory). For a development build with no
release version, bootstrap SHALL fall back to a documented default reference. The
CDK sources SHALL NOT be embedded in the binary, since a `pnpm install` is
required at runtime regardless.

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
early with actionable guidance when one is missing: a suitable Node runtime and
`pnpm` on the path, and resolvable AWS credentials. It SHALL report the resolved
AWS account and region for the plan, and SHALL note when the account is already
bootstrapped (the shared stack exists, via CloudFormation `DescribeStacks`). It
SHALL surface the GPU vCPU quota as a warning when it cannot be confirmed, without
attempting to raise it.

#### Scenario: Missing tooling fails early

- **WHEN** `pnpm` is not on the path
- **THEN** bootstrap fails before deploying, naming the missing prerequisite

#### Scenario: Unresolvable credentials fail early

- **WHEN** AWS credentials cannot be resolved
- **THEN** bootstrap fails before deploying, explaining how to provide them

#### Scenario: Already bootstrapped is noted, not fatal

- **WHEN** the shared stack already exists in the account and region
- **THEN** bootstrap notes it will update the shared infrastructure, and
  continues (the deploy is idempotent)

### Requirement: Shared settings are collected

Bootstrap SHALL collect the shared settings the CDK has no default for and write
them where the CDK reads them: which runner AMIs to bake (`--runners`, defaulting
to both `llamacpp` and `vllm`) so any environment can pick its engine at deploy
time, and an optional Hugging Face token for the shared secret used when seeding
gated weights. The engine is a per-environment choice made at `deploy`, so a
single runner is not a bootstrap setting; the allowed ingress CIDR is also
per-environment and belongs to `deploy`, not here.

#### Scenario: Both runner AMIs are baked by default

- **WHEN** the user runs bootstrap without selecting runners
- **THEN** AMIs for both `llamacpp` and `vllm` are baked

#### Scenario: The allowed CIDR is not a bootstrap setting

- **WHEN** the user runs bootstrap
- **THEN** no ingress CIDR is requested or written, since it is scoped per
  environment at `outfit remote deploy`

### Requirement: Idempotent bootstrap with an asynchronous bake

Bootstrap SHALL be safe to re-run: it SHALL skip `pnpm install` when dependencies
are present and `cdk bootstrap` when the account and region are already
bootstrapped, and SHALL not redeploy a shared stack that is unchanged. Because it
touches only shared infrastructure and never a live instance, re-running SHALL NOT
require any override. Because the AMI bake is slow, by default bootstrap SHALL
start the bake and hand off, telling the user how to wait, rather than blocking. A
`--wait` flag SHALL block until the bake completes.

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
