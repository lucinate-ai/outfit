## Why

When a remote GPU instance terminates, its engine logs vanish with it: the
`llama-server`/vLLM systemd unit writes only to journald, which lives on the
instance's ephemeral disk. A crash that happens before termination therefore
leaves no trace once the box is gone. This just blocked root-causing a real
`llama-server` crash on `dev-1` — the instance was healthy, crashed mid-session,
was restarted into a re-loading state (the `503 "Loading model"` the user hit),
and was then idle-swept and terminated, taking its only crash log with it.

## What Changes

- The engine's systemd unit writes stdout/stderr to a log **file** on disk
  (`/var/log/llm/<engine>.log`) instead of only to journald, so a durable
  record exists that a log shipper can tail.
- The **CloudWatch Agent** is baked into the runner AMIs and, on each boot,
  tails that file to CloudWatch Logs. Its environment-specific configuration
  (which carries the environment name and region) is written by the start
  Lambda's user-data, alongside the existing weights sync and API-key fetch.
- Engine logs land in a **shared log group per engine** (`/cloud-vm-llm/llamacpp`,
  `/cloud-vm-llm/vllm`) with a **stream per instance** named `<env>/<instance-id>`,
  so a terminated instance's logs outlive it and are grouped by environment.
- The **boot log** (`/var/log/cloud-init-output.log`, where the user-data script
  runs) is shipped to a `/cloud-vm-llm/boot` group with the same
  `<env>/<instance-id>` stream, so failures *before* the engine starts — the S3
  weights pull, swap setup, API-key fetch — are visible even though the engine
  log would be empty. The `aws s3 sync` gets `--no-progress` so the boot log is
  signal rather than thousands of progress lines.
- The log group is **pre-created in CDK** with a short retention period (1 day)
  and removal policy (rather than agent auto-creation), and the instance role
  gains the log permissions the agent needs.
- The on-disk log file is **size-bounded by rotation** (logrotate with
  `copytruncate` + a size cap on a short timer), so a chatty or crash-looping
  engine cannot fill the root volume; CloudWatch is the durable store, the file
  is only a short buffer.
- All operator guidance that currently reaches for `journalctl -u llama-server`
  (in `remote/README.md`) is redirected to the on-disk log file, since that is
  now where the engine's output lives.

## Capabilities

### New Capabilities
- `remote-log-shipping`: engine (`llama-server`/vLLM) stdout and stderr are
  written to a durable on-disk file, and both the engine log and the boot
  (cloud-init) log are shipped to CloudWatch log groups with a per-instance
  stream, so logs survive instance termination; covers the log destinations, the
  shipping agent, the log group/stream naming and retention, on-disk size
  bounding, and the IAM the instance needs.

### Modified Capabilities
<!-- No existing spec's requirements change: engine selection and command
     building (inference-runners), provisioning (endpoint-provisioning) and
     lifecycle (endpoint-lifecycle) keep their current requirements; the new
     capability owns the log destination and shipping behaviour. -->

## Impact

- `remote/` CDK (`remote/lib/llm-stack.ts`): pre-created CloudWatch log groups
  with retention + removal policy; instance-role log permissions
  (`CloudWatchAgentServerPolicy` or scoped `logs:CreateLogStream`/`logs:PutLogEvents`).
- Runner AMI recipes (EC2 Image Builder components in `remote/lib`): install the
  CloudWatch Agent into both runner images.
- Start Lambda user-data (`remote/lambda/start/index.ts`, `buildUserData` and the
  `llamacppUnit`/`vllmUnit` builders): serve unit logs to a file; write the
  agent config (engine log + `cloud-init-output.log`) with the environment name
  and start the agent; add `--no-progress` to the weights `aws s3 sync`.
- Docs (`remote/README.md`): `journalctl -u llama-server` guidance redirected to
  the on-disk log file.
- New AWS cost: CloudWatch Logs ingestion/storage (small; bounded by retention).
