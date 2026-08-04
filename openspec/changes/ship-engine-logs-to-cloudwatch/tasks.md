## 1. Infrastructure: log groups + IAM (`remote/lib/llm-stack.ts`)

- [x] 1.1 Create a CloudWatch `logs.LogGroup` for each engine (`/cloud-vm-llm/llamacpp`, `/cloud-vm-llm/vllm`) and one for the boot log (`/cloud-vm-llm/boot`), each with an explicit `retention` and `removalPolicy`.
- [x] 1.2 Make the retention window a context value (default 1 day, `RetentionDays.ONE_DAY`), read the same way as other `remote/` context knobs.
- [x] 1.3 Add a scoped `logs:CreateLogStream` + `logs:PutLogEvents` policy statement on `InstanceRole`, targeting the three log-group ARNs (both engine groups + the boot group; no `logs:CreateLogGroup`).
- [x] 1.4 Export/pass the log-group names (engine + boot) to wherever the start Lambda needs them (env var on the Lambda, matching how `WEIGHTS_BUCKET`/`REGION` are threaded).

## 2. AMI: bake the CloudWatch Agent (`remote/lib/image-stack.ts`)

- [x] 2.1 In the shared base component, install the CloudWatch Agent (download the Ubuntu amd64 `.deb` from Amazon's bucket and `dpkg -i`), without enabling it at bake time.
- [x] 2.2 Bump `RUNNER_VERSION` for both `vllm` and `llamacpp` so the agent ships in a rebake.
- [x] 2.3 Verify the bake with a `which amazon-cloudwatch-agent` / package-metadata check, consistent with the existing driver/runner verification steps.
- [x] 2.4 Bake a logrotate config for `/var/log/llm/*.log` (`copytruncate`, `size 200M`, `rotate 2`, `compress`, `missingok`, `notifempty`) plus a systemd timer (or drop-in) that runs it every ~15 min, so size-based rotation is not gated on the default daily run. Leave `cloud-init-output.log` out — it is written once at boot and stays small with `--no-progress`.

## 3. Boot: engine logs to file + agent config (`remote/lambda/start/index.ts`)

- [x] 3.1 In `buildUserData`, create `/var/log/llm` before the unit starts, and add `--no-progress` to the weights `aws s3 sync` so the boot log isn't flooded with per-chunk progress lines.
- [x] 3.2 In `llamacppUnit` and `vllmUnit`, add `StandardOutput=append:/var/log/llm/<engine>.log` and `StandardError=…` to the `[Service]` section.
- [x] 3.3 In `buildUserData`, write the CloudWatch Agent config JSON with region and two file entries — the engine log file → the engine's log group, and `/var/log/cloud-init-output.log` → `/cloud-vm-llm/boot` — both on stream `<env>/{instance_id}` (env substituted at launch, instance id via the agent token).
- [x] 3.4 Start the agent from user-data (`amazon-cloudwatch-agent-ctl -a fetch-config … && systemctl enable --now amazon-cloudwatch-agent`), selecting the engine's file/group by the same runner branch that picks the unit builder.

## 4. Docs: redirect `journalctl` to the log file (`remote/README.md`)

- [x] 4.1 Replace the five `journalctl -u llama-server` references (follow logs, "why it won't start", MTP `draft acceptance` grep, troubleshooting rows) with the equivalent `/var/log/llm/llama-server.log` commands (and `vllm.log` for vLLM).
- [x] 4.2 Add a short note that the same engine logs are in the engine's CloudWatch log group (`/cloud-vm-llm/<engine>`, stream `<env>/<instance-id>`), which survives instance termination.

## 5. Verification

- [x] 5.1 `cd remote && pnpm test` (and typecheck/lint) pass; CDK synthesises without error.
- [x] 5.2 Bootstrap/re-bake in a test account, `deploy` + `start` an environment, and confirm engine logs appear in the engine's CloudWatch group under the `<env>/<instance-id>` stream while the instance runs.
- [x] 5.3 Stop/terminate the instance and confirm the shipped logs remain readable in CloudWatch afterwards.
- [x] 5.4 Confirm the boot log ships: `/cloud-vm-llm/boot` has the instance's stream showing the user-data steps (swap, weights sync, API-key fetch), and the sync no longer emits per-chunk progress lines.
- [x] 5.5 Confirm `tail -f /var/log/llm/llama-server.log` shows live engine output over SSM (the journald replacement path).
- [x] 5.6 Confirm rotation bounds the file: drive sustained engine output (or lower the size threshold), and verify the log file rotates, on-disk usage stays capped, and shipping continues across the rotation.
