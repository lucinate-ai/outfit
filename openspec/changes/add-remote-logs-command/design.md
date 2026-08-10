## Context

The shipping half of this already exists (`remote-log-shipping`). Every running
instance runs the CloudWatch agent from its baked image and delivers two files:

- the engine log, to a per-engine group `/cloud-vm-llm/<runner>` where runner is
  `vllm` or `llamacpp` (`remote/lib/llm-stack.ts`);
- `/var/log/cloud-init-output.log`, to `/cloud-vm-llm/boot`.

Both use the stream name `<environment>/{instance_id}`
(`remote/lambda/start/index.ts:285`). The groups are CDK-managed with explicit
retention, and the instance role may only create streams and put events.

Nothing on the read side exists. The CLI already talks to AWS directly with the
caller's credentials — `internal/remote/aws.go` uses CloudFormation, EC2,
pricing and STS, and every control Lambda call is SigV4-signed
(`internal/remote/remote.go:403`) — so a user of `outfit remote` always holds
credentials in the account.

Environment resolution is already settled and shared: `resolveRemoteConfig`
(`cmd/outfit/remote.go:96`) turns an optional Outfit path into a
`remote.Config`, whose `Environment` field names the environment and whose
`Region` is resolved from config, `OUTFIT_REMOTE_REGION`, `AWS_REGION`, or the
Function URL host.

## Goals / Non-Goals

**Goals:**

- Read an environment's engine and boot logs from the CLI, with the same
  environment resolution as the other remote subcommands.
- Work when the instance is stopped or terminated, and when the control Lambdas
  are unreachable.
- Keep the fetch bounded by default, and make the bounds explicit.
- Turn the predictable failures (no environment name, no group, no permission,
  no events) into messages that say what to fix.
- No change to `remote/`: read what the existing infrastructure already writes.

**Non-Goals:**

- Log search, filtering by pattern, or aggregation — CloudWatch Logs Insights
  already does that, and a `--filter` can be added later without reshaping this.
- Shipping any new log source, changing retention, or touching the CDK stacks.
- A control Lambda for logs. Reading through the shared layer would make logs
  unavailable exactly when the layer is the thing that is broken.
- Local (`outfit serve`) and `outfit fleet` logs. This is about remote
  environments. Fleet reads each node's daemon over HTTP rather than
  CloudWatch, and no daemon endpoint serves log content today, so it needs its
  own change (`add-fleet-logs-command`); it reuses this change's rendering,
  flags and follow loop rather than its fetch.

## Decisions

### Read CloudWatch Logs directly with the caller's credentials

`FilterLogEvents` against the group, with `logStreamNamePrefix` set to
`<environment>/`, using an `aws.Config` from the existing
`remote.LoadAWSConfig(ctx, cfg.Region)`.

Considered and rejected: a new logs Lambda alongside start/stop/stats. It would
need a redeploy of the shared layer before anyone could read logs, it adds a
component that can itself fail, and it buys nothing — anyone entitled to call
the control Lambdas already has AWS credentials in the account. Direct reads
also keep working while the shared layer is broken or mid-deploy, which is one
of the moments logs matter most.

The cost is a new dependency, `aws-sdk-go-v2/service/cloudwatchlogs`, and a new
IAM expectation on the human operator (`logs:FilterLogEvents` on the groups).
Both are acceptable; the operator already needs CloudFormation and EC2 read
access for `outfit remote bootstrap`.

### Derive group names by convention, do not discover them

Group names are computed in Go: `/cloud-vm-llm/vllm`, `/cloud-vm-llm/llamacpp`,
`/cloud-vm-llm/boot`, built from the same `cloud-vm-llm` prefix already hard
coded as `sharedStackName` in `cmd/outfit/remote_bootstrap.go:24`, and from the
same runner names the deploy path already validates.

Considered: reading the names from CloudFormation stack outputs. The stack does
not export them today, so this would mean changing `remote/` — which the
proposal deliberately avoids — plus an extra API call on every invocation. The
convention is already relied on elsewhere (`internal/remote/bake.go` selects
AMIs by the `cloud-vm-llm:*` tag convention), so this is consistent with how the
CLI already treats the shared layer's naming as a contract.

A single Go constant will hold the prefix so the two hard-codings stay together.

### Query both engine groups rather than resolving the runner

For `--source engine`, query the group for every supported runner and merge.

The alternative is to learn the environment's runner and query one group. The
only local sources of the runner are the stats Lambda (which needs a running
instance — precisely what we cannot assume) and the Outfit's own model
selection (which describes intent, not what the last instance actually ran).
Querying both is one extra API call against a group that will simply return
nothing, and it is correct across a runner switch, where the environment's
history genuinely spans two groups.

`ResourceNotFoundException` from one engine group is therefore not fatal on its
own: only when *every* group asked for is missing does the command report the
shared layer as predating log shipping.

### Merge and cap after fetching, in-process

Each `(group, source)` fetch is paged with `FilterLogEvents`; events carry
`timestamp`, `logStreamName` and `eventId`. The results are merged by timestamp,
with `eventId` as the tiebreak so ordering is stable, then the last `--limit`
events are kept.

`FilterLogEvents` returns oldest-first and offers no "last N" mode, so a cap has
to be applied while paging. Each group's events are accumulated and the front is
trimmed once the buffer reaches twice `--limit`, so memory stays bounded by
`--limit` however large the window is, the trimming costs amortised constant
work, and what survives is genuinely the most recent — with the dropped count
reported, as the spec requires.

An earlier version of this design stopped paging at a ceiling above `--limit`.
That was wrong: because pages arrive oldest-first, stopping early leaves the
*oldest* part of the window, so "the most recent events" would have come from
its middle. Paging therefore runs to the end of the window, and the guard
against an unbounded `--since` is a separate cap on the number of requests
(`maxLogPages`). Hitting that cap is an error asking for a narrower window
rather than a silent truncation — the pages read by then are the wrong end of
the window to present as the tail.

Instance id comes from the stream name (`<env>/<instance-id>`), so `--instance`
is a post-filter on the parsed stream rather than a second API concept, and it
also gives each printed line its instance label.

### Flags

```
outfit remote logs [flags] [path]
  --source engine|boot|all   default engine
  --since <duration>         default 1h, Go duration syntax (30m, 2h)
  --limit <n>                default 200
  --instance <id>            restrict to one instance
  --follow, -f               keep printing new events
  --format text|json         default text
```

`--since`/`--format` mirror what `outfit remote metrics` already does, and
`sortFlagsBeforeArgs` plus `outfitArg(fs)` give the same "flags anywhere,
optional Outfit path last" behaviour as the other subcommands.

Implementing this exposed a bug in that shared helper: it classified any token
starting with `-` as a flag and everything else as positional, then moved the
flags to the front, which separates a flag from its value. With one
value-taking flag the order happens to survive, which is why no existing
subcommand hit it; `logs` has four, so `--source boot --limit 5` became
`--source --limit boot 5` and `--source` swallowed `--limit`.

`outfit fleet` hit the same bug and fixed it on main first, so this change
carries no fix of its own — it adopts main's, which takes the `*flag.FlagSet`,
keeps each flag with its value, and is the better of the two: it preserves the
`--` terminator in the flag section (dropping it, as this change's version did,
would leave the flag package parsing what follows `--` as flags) and treats an
unknown flag as taking no value rather than letting it swallow a positional.

`--follow` polls rather than using the newer live-tail API: live tail is a
separate, differently-priced API with its own IAM action, and polling
`FilterLogEvents` reuses everything already built. Each poll starts from the
last seen timestamp minus a small overlap to tolerate late-arriving events, and
a bounded set of recently seen `eventId`s suppresses the duplicates that
overlap produces. Interrupt handling follows `runMetricsWatch`
(`cmd/outfit/remote.go:542`): signal handler cancels the context, a cancelled
context exits nil.

### Layering and testability

`internal/remote/logs.go` owns group naming, the fetch, stream parsing, merge
and cap, exposing a small `LogEvent` type. The CloudWatch call sits behind an
interface with the client injected, matching how `cmd/outfit/remote.go` already
substitutes `deployDiscoverFn` and friends in tests, so the whole command is
testable without AWS. `cmd/outfit/remote_logs.go` owns flags, rendering and the
follow loop.

## Risks / Trade-offs

- [The `/cloud-vm-llm/...` naming drifts in `remote/` and the CLI silently finds
  nothing] → the prefix lives in one Go constant next to the existing
  `sharedStackName`, and the "no group at all" path reports a diagnosable error
  rather than an empty result.
- [A new runner is added to `RUNNERS` in TypeScript and not to the Go list, so
  its logs become unreachable] → the Go runner list sits beside the existing
  runner validation, and a test asserts the two agree with what deploy accepts.
- [Operators whose credentials can call the control Lambdas but cannot read
  logs] → detected as an access-denied error and reported with the missing
  permission named, so it is a documentation and IAM fix rather than a mystery.
- [`--follow` polling costs one `FilterLogEvents` call per interval per group,
  and CloudWatch charges per scanned data] → the poll window is small (only
  since the last event), the interval is a few seconds, and `--follow` is opt-in.
- [Duplicate or missing events at the poll boundary] → overlap on the start time
  plus `eventId` de-duplication; the trade is a bounded memory of recent ids.
- [Late-arriving events can appear out of order relative to already-printed
  ones] → accepted. The agent's delivery lag is seconds; a strict global
  ordering would mean buffering, which defeats the point of following.

## Migration Plan

None needed. This is an additive read-only subcommand: no state, no config
schema change, no infrastructure change. Environments deployed before this
release are readable as long as their shared layer ships logs; those whose layer
predates log shipping get the explicit "re-deploy the shared layer" message.
Rollback is removing the subcommand.

## Open Questions

- Should `--source all` be the default instead of `engine`? Engine is chosen on
  the grounds that most reads happen after a deploy is up; a boot failure is the
  case where the operator is already suspicious enough to ask. Revisit if the
  empty-engine-log case turns out to be the common one — the fix would be to
  fall back to boot automatically when the engine log is empty.
- Whether `--limit` should default to more than 200 for `--source all`, where
  two log sources share the cap.
