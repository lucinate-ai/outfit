package remote

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/smithy-go"
)

// ControlPlaneStackName is the CloudFormation stack holding the account-level
// control plane, and the namespace its log groups are named under. The stack
// name and the log group naming are one convention, so they live together: the
// bootstrap command looks the stack up by this name, and the log reader derives
// the group names below from it rather than discovering them, the same way
// BakedRunners selects images by the matching tag convention.
const ControlPlaneStackName = "cloud-vm-llm"

// logGroupPrefix is the namespace every control-plane log group sits under. It
// must track the group names in remote/lib/llm-stack.ts.
const logGroupPrefix = "/" + ControlPlaneStackName

// Runners are the inference engines an environment can run, and so the engine
// log groups its instances can ship to. An environment's runner is not knowable
// locally once its instance is gone — the stats Lambda only answers for a
// running one, and an Outfit states intent rather than what last ran — so the
// reader asks every engine group and merges. That is also what an environment
// whose runner changed needs, since its history spans two groups. It must track
// RUNNERS in remote/lambda/shared/deploy-config.ts, and runnerFor's switch.
var Runners = []string{"llamacpp", "vllm"}

// The log sources an instance ships: the inference engine's own output, and the
// boot (user-data) output covering the steps that run before the engine starts.
const (
	LogSourceEngine = "engine"
	LogSourceBoot   = "boot"
	LogSourceAll    = "all"
)

// LogSources lists the accepted --source values, in the order they are offered.
var LogSources = []string{LogSourceEngine, LogSourceBoot, LogSourceAll}

// EngineLogGroup is the CloudWatch log group one engine's instances ship their
// engine output to, across every environment.
func EngineLogGroup(runner string) string {
	return logGroupPrefix + "/" + runner
}

// BootLogGroup is the CloudWatch log group every instance ships its boot output
// to, whichever engine it runs.
func BootLogGroup() string {
	return logGroupPrefix + "/boot"
}

// LogQuery describes which of an environment's shipped logs to read. Start and
// End bound the window (a zero End means now); Limit caps how many events are
// returned, keeping the most recent; Instance, when set, narrows to one
// instance's streams.
type LogQuery struct {
	Environment string
	Source      string
	Start       time.Time
	End         time.Time
	Limit       int
	Instance    string
}

// LogEvent is one shipped log line, attributed to the source and instance that
// produced it. ID is CloudWatch's own event id, which orders events that share
// a millisecond and lets a follow suppress an event it has already printed.
type LogEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source"`
	Instance  string    `json:"instance"`
	Message   string    `json:"message"`
	ID        string    `json:"id"`
}

// LogResult is what a query found: the events, oldest first, and how many
// matching events Limit dropped, so a capped view is never presented as a
// complete one.
type LogResult struct {
	Events  []LogEvent
	Omitted int
}

// logsAPI is the slice of the CloudWatch Logs API the reader uses, so tests can
// substitute it for the real client.
type logsAPI interface {
	FilterLogEvents(ctx context.Context, in *cloudwatchlogs.FilterLogEventsInput,
		opts ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.FilterLogEventsOutput, error)
}

// maxLogPages bounds how many requests one group's window may take. The cap is
// a guard against an unbounded --since over a chatty engine, not a normal
// limit: hitting it is reported as an error asking for a narrower window,
// because the pages read so far are the window's oldest, and silently returning
// those as "the most recent" would be a lie.
const maxLogPages = 200

// logGroup pairs a group name with the source label its events carry.
type logGroup struct {
	name   string
	source string
}

// logGroupsFor returns the groups a source selects: every engine group for
// engine (the runner cannot be resolved locally — see Runners), the boot group
// for boot, and both for all.
func logGroupsFor(source string) ([]logGroup, error) {
	var groups []logGroup
	switch source {
	case LogSourceEngine, LogSourceAll:
		for _, runner := range Runners {
			groups = append(groups, logGroup{name: EngineLogGroup(runner), source: LogSourceEngine})
		}
		if source == LogSourceEngine {
			return groups, nil
		}
		fallthrough
	case LogSourceBoot:
		groups = append(groups, logGroup{name: BootLogGroup(), source: LogSourceBoot})
		return groups, nil
	default:
		return nil, fmt.Errorf("unknown log source %q (expected %s)", source, strings.Join(LogSources, ", "))
	}
}

// FetchLogs reads an environment's shipped logs from CloudWatch Logs with the
// caller's own AWS credentials — the same ones that sign the control calls — so
// the logs are readable whether or not the instance still exists, and without
// the control plane having to answer.
func FetchLogs(ctx context.Context, cfg Config, q LogQuery) (LogResult, error) {
	if q.Environment == "" {
		return LogResult{}, fmt.Errorf(
			"this remote config names no environment, so its log streams cannot be identified: " +
				"re-register it with `outfit remote deploy` (which writes the environment name)")
	}
	awsCfg, err := LoadAWSConfig(ctx, cfg.Region)
	if err != nil {
		return LogResult{}, err
	}
	return fetchLogs(ctx, cloudwatchlogs.NewFromConfig(awsCfg), q)
}

// fetchLogs queries every group the source selects and merges the results. A
// group that does not exist is skipped rather than failing the read — an
// environment only ever ships to the group for the engine it runs — but a read
// where every group is absent is the control plane predating log shipping, which
// is reported.
func fetchLogs(ctx context.Context, api logsAPI, q LogQuery) (LogResult, error) {
	groups, err := logGroupsFor(q.Source)
	if err != nil {
		return LogResult{}, err
	}
	var (
		events  []LogEvent
		omitted int
		missing int
	)
	for _, g := range groups {
		found, dropped, err := fetchGroup(ctx, api, g, q)
		if err != nil {
			var notFound *cwltypes.ResourceNotFoundException
			if errors.As(err, &notFound) {
				missing++
				continue
			}
			return LogResult{}, logsError(err)
		}
		events = append(events, found...)
		omitted += dropped
	}
	if missing == len(groups) {
		return LogResult{}, fmt.Errorf(
			"no log group exists for this environment's %s logs (looked for %s): "+
				"the control plane was deployed before log shipping — re-deploy it with `outfit remote bootstrap`",
			q.Source, strings.Join(groupNames(groups), ", "))
	}
	sortEvents(events)
	if q.Limit > 0 && len(events) > q.Limit {
		omitted += len(events) - q.Limit
		events = events[len(events)-q.Limit:]
	}
	return LogResult{Events: events, Omitted: omitted}, nil
}

// fetchGroup pages one group's window, keeping the most recent Limit events.
// CloudWatch returns events oldest first with no "last N" mode, so the cap is
// applied by trimming the front as the pages arrive: memory stays bounded by
// Limit however much the window holds, and what survives is genuinely the tail.
func fetchGroup(ctx context.Context, api logsAPI, g logGroup, q LogQuery) ([]LogEvent, int, error) {
	in := &cloudwatchlogs.FilterLogEventsInput{
		LogGroupName:        aws.String(g.name),
		LogStreamNamePrefix: aws.String(q.Environment + "/"),
	}
	if !q.Start.IsZero() {
		in.StartTime = aws.Int64(q.Start.UnixMilli())
	}
	if !q.End.IsZero() {
		in.EndTime = aws.Int64(q.End.UnixMilli())
	}

	var (
		kept    []LogEvent
		dropped int
	)
	for page := 1; ; page++ {
		out, err := api.FilterLogEvents(ctx, in)
		if err != nil {
			return nil, 0, err
		}
		for _, e := range out.Events {
			ev := logEventFrom(e, g.source)
			if q.Instance != "" && ev.Instance != q.Instance {
				continue
			}
			kept = append(kept, ev)
		}
		// Trim in blocks rather than per event, so the cap costs amortised
		// constant work over a long window.
		if q.Limit > 0 && len(kept) >= 2*q.Limit {
			excess := len(kept) - q.Limit
			kept = append(kept[:0], kept[excess:]...)
			dropped += excess
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		if page >= maxLogPages {
			return nil, 0, fmt.Errorf(
				"%s holds more output than can be paged through for this window (stopped after %d requests): "+
					"narrow it with --since, or --instance to a single instance",
				g.name, maxLogPages)
		}
		in.NextToken = out.NextToken
	}
	return kept, dropped, nil
}

// logEventFrom converts one CloudWatch event, taking the instance from its
// stream name and trimming the trailing newline the agent ships.
func logEventFrom(e cwltypes.FilteredLogEvent, source string) LogEvent {
	return LogEvent{
		Timestamp: time.UnixMilli(aws.ToInt64(e.Timestamp)),
		Source:    source,
		Instance:  streamInstance(aws.ToString(e.LogStreamName)),
		Message:   strings.TrimRight(aws.ToString(e.Message), "\r\n"),
		ID:        aws.ToString(e.EventId),
	}
}

// streamInstance takes the instance id out of an "<environment>/<instance-id>"
// stream name. An environment name cannot contain a slash (see IsEnvName), so
// the last segment is the instance.
func streamInstance(stream string) string {
	if i := strings.LastIndex(stream, "/"); i >= 0 {
		return stream[i+1:]
	}
	return stream
}

// sortEvents orders events oldest first, breaking ties on the event id so the
// order is stable across the groups a query merges.
func sortEvents(events []LogEvent) {
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].Timestamp.Equal(events[j].Timestamp) {
			return events[i].ID < events[j].ID
		}
		return events[i].Timestamp.Before(events[j].Timestamp)
	})
}

// groupNames lists the group names a query looked in, for error messages.
func groupNames(groups []logGroup) []string {
	names := make([]string, 0, len(groups))
	for _, g := range groups {
		names = append(names, g.name)
	}
	return names
}

// logsError turns the two AWS failures an operator can act on into guidance:
// credentials that have expired, and credentials that resolve but may not read
// the logs. Anything else is passed through as it came.
func logsError(err error) error {
	if credentialError(err) {
		return fmt.Errorf("reading logs failed: AWS credentials are expired or invalid — %s", refreshCredsHint)
	}
	if accessDenied(err) {
		return fmt.Errorf(
			"reading logs failed: these AWS credentials may not read the logs — they need logs:FilterLogEvents on %s/*",
			logGroupPrefix)
	}
	return err
}

// accessDenied reports whether err is AWS refusing for lack of permission. The
// typed code is checked first, with the message text as the fallback, since an
// IAM denial can arrive as a plain authorization error.
func accessDenied(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		if strings.Contains(code, "AccessDenied") || code == "AuthorizationError" {
			return true
		}
	}
	return strings.Contains(err.Error(), "not authorized to perform")
}
