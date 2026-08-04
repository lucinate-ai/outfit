## Context

Remote GPU instances run the inference engine (`llama-server` or vLLM) as a
systemd unit whose stdout/stderr go to journald only. Instances are ephemeral
and per-environment: the start Lambda launches one, the idle sweep terminates
it, and the root disk goes with it. So journald — the sole record of engine
output — is destroyed on termination. A `llama-server` crash on `dev-1` could
not be root-caused for exactly this reason: the instance was terminated by the
idle sweep before its crash log could be read.

The relevant anchors already exist:
- Instance role + profile: `remote/lib/llm-stack.ts` (`InstanceRole`, currently
  `AmazonSSMManagedInstanceCore` + secret read + weights read).
- Runner AMIs: `remote/lib/image-stack.ts` — per-runner EC2 Image Builder
  components (`vllmComponentDoc`/`llamacppComponentDoc`) plus a shared base that
  installs the Nvidia driver; `RUNNER_VERSION` gates a rebake.
- Boot: `remote/lambda/start/index.ts` `buildUserData` (weights sync, API-key
  fetch) and `llamacppUnit`/`vllmUnit` (the systemd unit text).

## Goals / Non-Goals

**Goals:**
- Engine logs survive instance termination and are readable from AWS.
- Logs are grouped by engine and addressable per environment + instance.
- Retention and cost are bounded and managed as infrastructure.
- No new per-boot package install; no new credential handling.

**Non-Goals:**
- Shipping host/GPU metrics (the agent can, but that is a separate change).
- Shipping the Lambdas' logs (already in CloudWatch). The boot/cloud-init log
  IS shipped (it holds the S3 pull and other pre-engine steps); other host log
  files (syslog, kernel) are out of scope.
- Changing the idle-sweep behaviour that terminated the crashed instance
  (tracked separately — this change only makes the crash observable).

## Decisions

### Path A: CloudWatch Agent tailing a file (not Fluent Bit / journald)

The CloudWatch Agent is the first-party EC2 agent and pairs with future host/GPU
metric collection using the same binary. It has no journald input, so the engine
must log to a **file** the agent tails.

- **Serve unit** gains `StandardOutput=append:/var/log/llm/<engine>.log` and
  `StandardError=append:/var/log/llm/<engine>.log` (systemd ≥ 240; Ubuntu 24.04
  qualifies). A `/var/log/llm` directory is created in user-data before the unit
  starts.
- _Alternative considered — Fluent Bit `systemd` input reading journald:_ keeps
  the unit unchanged and preserves `journalctl`, but adds a second agent lineage
  and does not help the metrics story. Rejected in favour of one agent; the
  journald view is replaced by tailing the file (see Risks).

### Naming: group per source, stream per instance

- Engine log groups: `/cloud-vm-llm/llamacpp` and `/cloud-vm-llm/vllm`.
- Boot log group: `/cloud-vm-llm/boot`.
- Log stream (all groups): `<env>/<instance-id>` (e.g. `dev-1/i-0abc…`).

This keeps one source's history in one place while making each instance's logs
individually addressable and attributable to their environment — precisely the
"whose logs, which run" question that came up on `dev-1`. Boot output goes to its
own group rather than the engine's because it is not engine-specific and its
volume/lifetime differ (one burst at boot vs. continuous serving); sharing the
same stream name lets you line up the same instance across both groups.
Per-environment groups were considered but fragment history and multiply managed
resources; the env is better as a stream-name dimension.

### Boot log: ship cloud-init-output.log, quieten the S3 sync

The user-data script's output is captured by cloud-init at
`/var/log/cloud-init-output.log` — it already exists, no unit change needed. It
covers everything before the engine starts: swap, the weights `aws s3 sync`, the
API-key fetch, unit creation. That is where an S3 pull failure or a bad AMI shows
up while the engine log is still empty, so the agent tails it into
`/cloud-vm-llm/boot`. The weights sync currently floods this file with thousands
of `Completed … MiB` progress lines (visible in the `dev-1` boot log), so
`aws s3 sync` gains `--no-progress`; it still logs completion and errors, just
not the per-chunk progress. This log is written once at boot and stays small, so
it is deliberately left out of logrotate — only the continuously-written engine
log needs rotation.

### Log group is CDK-managed, not agent-created

Create both groups in `llm-stack.ts` with an explicit `retention`
(`logs.RetentionDays.ONE_DAY`) and `removalPolicy`. Managed creation keeps
retention and teardown as infrastructure and lets the instance role omit
`logs:CreateLogGroup`. The retention window is a context value (default 1 day)
so it can be tuned without code change, matching how other `remote/` knobs work.
One day is deliberate: these logs exist to catch the crash of a short-lived
instance, not for long-term audit, so the window is kept small to bound cost.

### Bounding the on-instance log file

The CloudWatch Agent tails the file but never trims it, so without rotation a
crash-loop could fill the 80 GB root disk. A baked **logrotate** config for
`/var/log/llm/*.log` bounds it:

- `copytruncate`, because the serve unit holds the file open in append mode
  (`StandardOutput=append:`); a rename would leave the engine writing to the old
  inode, so logrotate copies then truncates in place. The agent detects the
  truncation and resumes from offset 0, so shipping continues.
- A **size** trigger (rotate at ~200 MB, `rotate 2`, `compress`, `missingok`),
  driven by a short-interval timer (every ~15 min) rather than only the default
  daily run — a crash-loop can emit GBs in an hour, which daily rotation would
  miss. Disk use is thereby bounded to a few hundred MB regardless of session
  length.

CloudWatch (1-day retention) is the durable store; the on-disk file is only a
short rotation buffer, so a small `rotate` count is fine.

### Agent baked into the AMI; config written at boot

- **Bake** the agent into both runner images via the shared Image Builder base
  component (download the Ubuntu `.deb` from Amazon's bucket and `dpkg -i`), and
  bump `RUNNER_VERSION` for both runners so a rebake ships it. Baking avoids an
  apt install on every cold start.
- **Config at boot**: the groups are fixed but the stream carries the
  environment, known only at launch. `buildUserData` writes the agent config JSON
  with the env and region substituted (the same way it already substitutes
  `REGION` and `WEIGHTS_BUCKET`), with **two file entries** — the engine log →
  the engine group, and `/var/log/cloud-init-output.log` → `/cloud-vm-llm/boot` —
  each on the `<env>/<instance-id>` stream (instance id via `{instance_id}` agent
  token), then `systemctl enable --now` the agent. The engine name selects which
  engine group and file path to use, mirroring the existing runner branch that
  picks `llamacppUnit` vs `vllmUnit`; the boot entry is the same for both.

### IAM: scoped log permissions on the existing instance role

Add to `InstanceRole` the minimal `logs:CreateLogStream` + `logs:PutLogEvents`
on the three group ARNs — the two engine groups and the boot group — (no
`CreateLogGroup`, since CDK pre-creates them).
Prefer the scoped statement over attaching `CloudWatchAgentServerPolicy`, whose
grants are broader than log shipping needs. The permission travels with the role
the instance already assumes — no new credential path.

### Docs: journalctl → the log file

`remote/README.md` has five `journalctl -u llama-server` references (follow
logs, "why it won't start", MTP-acceptance grep, troubleshooting). Redirect each
to the on-disk file — `tail -f /var/log/llm/llama-server.log`,
`grep 'draft acceptance' /var/log/llm/llama-server.log`, etc. — so operator
guidance matches where the output now lives, and add that the same logs are in
the engine's CloudWatch group for a terminated box.

## Risks / Trade-offs

- **Losing journald as the live view** → the serve unit no longer writes to the
  journal, so `journalctl -u llama-server` stops showing engine output. Mitigation:
  the file is equivalent for live tailing over SSM (`tail -f`), the docs are
  updated to it, and CloudWatch covers the post-mortem case journald never did.
- **Baking requires a rebake** → the agent only lands on instances launched from
  a re-baked AMI; older AMIs won't ship logs. Mitigation: bump `RUNNER_VERSION`
  and note that `outfit remote bootstrap` must re-bake before the change takes
  effect (consistent with how other AMI changes roll out).
- **Rotation vs. a tailing agent** → `copytruncate` has a brief race where lines
  written between the copy and the truncate could be missed. Mitigation: the
  agent tails continuously and has almost always shipped up to the tail before
  rotation runs; CloudWatch is the durable copy, so a rare lost line on the
  local buffer is acceptable.
- **Log loss window on abrupt termination** → the agent batches, so the last
  few seconds before a hard kill may not flush. Mitigation: acceptable for
  post-mortem; a short flush interval narrows it. Crash output precedes the kill
  and is what matters here.
- **CloudWatch cost** → ingestion + storage. Mitigation: bounded by retention;
  engine logs are low-volume.
- **`append:` needs systemd ≥ 240** → true on Ubuntu 24.04 (the base image);
  documented as an assumption rather than guarded.

## Migration Plan

1. Land CDK (log groups + IAM) and Image Builder change; `RUNNER_VERSION` bumped.
2. Re-bake both runner AMIs (`outfit remote bootstrap`) so images carry the agent.
3. New instance starts pick up the file-logging unit + agent config from
   user-data automatically; no per-environment redeploy needed for the boot path,
   though a fresh `deploy`/`start` is what exercises it.
4. Rollback: revert the stack + user-data; instances revert to journald-only. The
   log groups can be retained or removed per their removal policy.

## Open Questions

- None outstanding.
