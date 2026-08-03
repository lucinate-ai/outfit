## Context

The idle Lambda already scrapes `/metrics` on the instance every 5 minutes via SSM RunCommand, parsing token counters and request counts to decide whether to terminate. That data is consumed once and discarded. The instance also runs `nvidia-smi`, `free`, and `vmstat`-queryable system stats that are never exposed to the user.

The CLI today has no way to read live metrics. `outfit remote status` only reports instance state and health.

## Goals / Non-Goals

**Goals:**
- User can run `outfit remote stats` and see token counts, resource usage, GPU info, and instance details
- Works for both llama.cpp and vLLM backends
- Runner (llama.cpp vs vLLM) is auto-detected from the Outfit — no manual configuration
- GPU info shows per-GPU stats (`nvidia-smi -L`), with aggregated totals for multi-GPU
- Optional `--cost` flag computes estimated session cost via AWS Price List API

**Non-Goals:**
- Historical metrics or time-series (that's a different feature)
- Streaming/continuous monitoring
- CPU/GPU metrics in the idle decision (the idle Lambda stays unchanged)
- Direct HTTP scraping of `/metrics` from the CLI (all goes through Lambda)

## Decisions

**All data flows through a Lambda relay, not direct HTTP.**

The CLI has the instance's public URL from `remote.json`, but scraping `/metrics` directly has two problems: llama.cpp gates it behind the API key (which the CLI doesn't persist), and direct HTTP can't read system stats (CPU, RAM, GPU). A Lambda relay solves both — it reads the API key from Secrets Manager and runs shell commands via SSM. The latency cost is ~3-5 seconds (SSM roundtrip), which is acceptable for an occasional `stats` command.

**Reuse the existing metrics parsing.**

`remote/lambda/shared/idle.ts` already has `parseMetrics` and `metricsGrepPattern` that handle both runners. The stats Lambda will reuse these to parse token/request metrics from `/metrics`, and add new parsing for `vmstat`, `free`, and `nvidia-smi`.

**Per-GPU display with aggregate row for multi-GPU.**

`nvidia-smi --query-gpu=index,utilization.gpu,memory.used,memory.total --format=csv,noheader` returns one line per GPU. For a single GPU (current `g6e.xlarge`), it's a simple display. For multi-GPU (`g6e.4xlarge` etc.), show each GPU's line plus an average utilization and summed memory.

**Runner discovered from the Outfit's PROVIDER.**

The Outfit is resolved using the same logic as `start`/`stop`/`deploy` — `readOutfit()` — which carries `PROVIDER llamacpp` or `PROVIDER vllm`. No probing, no infra change. This also means `stats` requires an Outfit with a `REMOTE` instruction, matching the pattern of `deploy`.

**Cost is optional and fetched live.**

The on-demand price for `g6e.xlarge` varies by region and AZ. Rather than hard-coding prices, the `--cost` flag invokes the AWS Price List API (`GetProducts`) with the instance type and region. The CLI already has `internal/remote/aws.go` for AWS config. The cost is computed as `onDemandPricePerHour * hoursRunning`. Without `--cost`, no AWS Price List API call is made.

**Tabular output, one key-value per line.**

Consistent with `outfit remote status`. No JSON output for now (can be added later with `--json`).

## Risks / Trade-offs

- **SSM latency.** Three sequential shell commands (`curl /metrics`, `nvidia-smi`, `free`/`vmstat`) each take ~2-3 seconds. Total: ~6-10 seconds. Mitigation: run `nvidia-smi` and `free`/`vmstat` in parallel SSM commands, or combine into a single shell command with `&&`.

- **Price List API rate limits.** `GetProducts` can return large responses. For a single instance type lookup with a filter, it's a small query. If the user calls `stats --cost` frequently, it could hit rate limits. Mitigation: cache the price for 5 minutes in the CLI process.

- **vmstat format consistency.** `vmstat 1 2 | tail -1` is more consistent than `top` across Ubuntu versions. The AMI is baked and controlled, so the format is stable.

- **Instance not running.** If the instance is stopped, the Lambda returns early with `state: stopped` and no metrics. This is the same behaviour as `status`.

## Output Format

```
environment    qwen3.6-27b
state          running
runner         llamacpp
model          unsloth/Qwen3.6-27B-MTP-GGUF:UD-Q6_K_XL
gpu 0          NVIDIA L40S (48 GB)      87%    38.2 / 48.0 GB
cpu            12%
ram            18.4 / 39.6 GB
uptime         1h 23m
cost           $1.74
prompt tokens  245,000
output tokens  128,000
requests       42
in-flight      0
```

For multi-GPU:

```
gpu 0          NVIDIA L40S (48 GB)      87%    38.2 / 48.0 GB
gpu 1          NVIDIA L40S (48 GB)      92%    40.1 / 48.0 GB
gpu (avg)                                  89%    78.3 / 96.0 GB
```

When stopped:

```
environment    qwen3.6-27b
state          stopped
```