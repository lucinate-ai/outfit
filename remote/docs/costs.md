# What this stack costs

The short version: **~$7/month at rest, plus $1.86 for every hour the GPU
instance is actually running.** Nothing scales with request volume — only with
hours up. Because the instance is terminated (not stopped) when idle, there is
no idle EBS volume, so the resting cost is low.

## Fixed costs

These accrue whether or not the instance is ever launched:

| Item | Monthly (approx.) |
|---|---|
| Elastic IP (billed while allocated) | $3.70 |
| S3 weights (~$0.023/GB — $0.60 for the 26 GB GGUF, ~$1.30 if both engines' weights are kept) | $0.60–1.30 |
| AMI EBS snapshots (~20 GB used each @ ~$0.05/GB; one per engine baked) | $1–2 |
| Secrets Manager (the endpoint's API key) | $0.40 |
| Lambdas, EventBridge, SSM, Parameter Store, CloudWatch logs | ~$0 |
| VPC, subnets, internet gateway | $0 |
| **Total** | **~$6–7** |

The orchestration layer is effectively free: the idle check runs every
5 minutes but sits comfortably inside the Lambda and EventBridge free tiers,
and the stack deliberately has no NAT gateway (~$36/month avoided — the
Lambdas live outside the VPC and reach the instance via SSM instead).

There is **no persistent EBS volume**: the weights live in S3, and the running
instance's root volume is created from the engine's AMI and deleted on
termination. So while running you also pay for that ephemeral ~80 GB gp3 root
(~$0.25/day pro-rated while up), but nothing for it at rest. Moving the weights
out of the AMI into S3 also shrank the snapshot from ~113 GB to ~20 GB.

## Variable cost: GPU hours

A g6e.xlarge costs **$1.86/hour on-demand in us-east-1** (for comparison:
$1.97 in Stockholm, $2.33 in Frankfurt — and none at all in Ireland or
London, which is how the stack ended up in us-east-1). Billing is per-second
with a 60-second minimum.

Worked examples (GPU hours + the ~$6 fixed floor):

| Usage pattern | GPU hours/month | Total/month |
|---|---|---|
| Never launched | 0 | ~$6 |
| ~2 h/day on weekdays | ~44 | ~$90 |
| 4-hour cap hit every weekday | ~88 | ~$170 |
| Always on, 24/7 (what this design avoids) | 730 | ~$1,370 |

The bake and the weight seed are one-offs: each runs a cheap `m5.xlarge` for
~20–40 minutes, so a few pennies. Bakes are only needed when the engine version
or driver changes — swapping model is just `outfit remote deploy`, and the seed
it starts is the only cost.

Need more headroom? The 27B checkpoint sits close to the 32 GB of RAM
on a `g6e.xlarge`. A `g6e.2xlarge` doubles RAM to 64 GB (same single L40S) at
**~$3.72/hour**, but needs the G-series vCPU quota raised to 8 (from the 4 a
`g6e.xlarge` uses). On `g6e.xlarge` a boot-time swapfile covers the shortfall.

## What bounds the bill

Two mechanisms keep the variable column honest:

- **Idle terminate** — the instance is terminated 15–20 minutes after the last
  request (`idleThresholdMinutes`, default 15, plus up to one 5-minute check
  tick). Forgetting to stop it costs about $0.60, not a day of GPU time.
- **Maximum runtime** — `maxRuntimeMinutes` (default 4 hours) terminates the
  instance that long after boot even if requests are still flowing, so a
  runaway session is bounded at ~$8. Each launch resets the clock.

Data transfer is negligible: the weights sync from S3 in-region (free), so a
wake pulls no billable bytes, and streamed completions outbound are text.

## Reducing the fixed floor

- **`pnpm cdk destroy cloud-vm-llm cloud-vm-llm-image`** drops everything to
  $0. Deregister the AMIs/snapshots too if you want that storage back. The
  next setup pays a bake (~30 min) again.
- **Tailscale** (planned, see [tailscale-plan.md](tailscale-plan.md)) removes
  the Elastic IP, shaving $3.70/month off the fixed floor and closing the
  public endpoint at the same time.
- A smaller `imageVolumeGb` shrinks the snapshot line, but leave headroom for
  the OS + engine (~20 GB), the ~16 GB boot-time swapfile, and the weights
  synced from S3 at boot (~26 GB for the default GGUF).
- Only bake the engine you actually run; each baked AMI carries its own
  snapshot. Keeping both is what buys the one-command cut-back.
