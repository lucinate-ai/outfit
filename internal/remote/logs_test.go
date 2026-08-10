package remote

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/smithy-go"
)

// fakeLogs is a substituted CloudWatch Logs API: pages holds each group's
// responses in order, errs the error a group fails with. calls records the
// inputs so the tests can assert on the window and stream prefix asked for.
type fakeLogs struct {
	pages map[string][]*cloudwatchlogs.FilterLogEventsOutput
	errs  map[string]error
	calls []*cloudwatchlogs.FilterLogEventsInput
	seen  map[string]int
}

func (f *fakeLogs) FilterLogEvents(_ context.Context, in *cloudwatchlogs.FilterLogEventsInput,
	_ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.FilterLogEventsOutput, error) {
	group := aws.ToString(in.LogGroupName)
	cp := *in
	f.calls = append(f.calls, &cp)
	if err, ok := f.errs[group]; ok {
		return nil, err
	}
	if f.seen == nil {
		f.seen = map[string]int{}
	}
	i := f.seen[group]
	f.seen[group]++
	pages := f.pages[group]
	if i >= len(pages) {
		return &cloudwatchlogs.FilterLogEventsOutput{}, nil
	}
	return pages[i], nil
}

// event builds one CloudWatch event at the given millisecond.
func event(id string, ms int64, stream, message string) cwltypes.FilteredLogEvent {
	return cwltypes.FilteredLogEvent{
		EventId:       aws.String(id),
		Timestamp:     aws.Int64(ms),
		LogStreamName: aws.String(stream),
		Message:       aws.String(message),
	}
}

// page wraps events into one response, optionally with a continuation token.
func page(next string, events ...cwltypes.FilteredLogEvent) *cloudwatchlogs.FilterLogEventsOutput {
	out := &cloudwatchlogs.FilterLogEventsOutput{Events: events}
	if next != "" {
		out.NextToken = aws.String(next)
	}
	return out
}

func TestLogGroupNamesFollowTheSharedConvention(t *testing.T) {
	if got := EngineLogGroup("vllm"); got != "/cloud-vm-llm/vllm" {
		t.Errorf("engine group = %q, want /cloud-vm-llm/vllm", got)
	}
	if got := BootLogGroup(); got != "/cloud-vm-llm/boot" {
		t.Errorf("boot group = %q, want /cloud-vm-llm/boot", got)
	}
	if ControlPlaneStackName != "cloud-vm-llm" {
		t.Errorf("ControlPlaneStackName = %q, want cloud-vm-llm", ControlPlaneStackName)
	}
}

func TestLogGroupsForSelectsBySource(t *testing.T) {
	engine, err := logGroupsFor(LogSourceEngine)
	if err != nil {
		t.Fatal(err)
	}
	if len(engine) != len(Runners) {
		t.Fatalf("engine selected %d groups, want one per runner (%d)", len(engine), len(Runners))
	}
	for _, g := range engine {
		if g.source != LogSourceEngine {
			t.Errorf("group %s labelled %q, want engine", g.name, g.source)
		}
	}

	boot, err := logGroupsFor(LogSourceBoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(boot) != 1 || boot[0].name != BootLogGroup() {
		t.Fatalf("boot selected %v, want just the boot group", groupNames(boot))
	}

	all, err := logGroupsFor(LogSourceAll)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != len(Runners)+1 {
		t.Fatalf("all selected %v, want every engine group plus boot", groupNames(all))
	}

	if _, err := logGroupsFor("nonsense"); err == nil {
		t.Error("an unknown source should be rejected")
	}
}

func TestStreamInstanceTakesTheLastSegment(t *testing.T) {
	if got := streamInstance("prod/i-0abc123"); got != "i-0abc123" {
		t.Errorf("instance = %q, want i-0abc123", got)
	}
	if got := streamInstance("no-slash"); got != "no-slash" {
		t.Errorf("instance = %q, want the whole stream when it has no slash", got)
	}
}

func TestFetchLogsMergesGroupsInTimeOrder(t *testing.T) {
	api := &fakeLogs{pages: map[string][]*cloudwatchlogs.FilterLogEventsOutput{
		EngineLogGroup("llamacpp"): {page("", event("b", 2000, "prod/i-1", "engine two"))},
		EngineLogGroup("vllm"):     {page("", event("a", 1000, "prod/i-1", "engine one"))},
		BootLogGroup():             {page("", event("c", 1500, "prod/i-1", "boot line"))},
	}}

	got, err := fetchLogs(context.Background(), api, LogQuery{Environment: "prod", Source: LogSourceAll})
	if err != nil {
		t.Fatal(err)
	}
	var messages []string
	for _, e := range got.Events {
		messages = append(messages, fmt.Sprintf("%s:%s", e.Source, e.Message))
	}
	want := "engine:engine one boot:boot line engine:engine two"
	if strings.Join(messages, " ") != want {
		t.Errorf("events = %v, want oldest first: %s", messages, want)
	}
	if got.Omitted != 0 {
		t.Errorf("omitted = %d, want 0", got.Omitted)
	}
}

func TestFetchLogsOrdersEventsSharingAMillisecondByEventID(t *testing.T) {
	api := &fakeLogs{pages: map[string][]*cloudwatchlogs.FilterLogEventsOutput{
		EngineLogGroup("llamacpp"): {page("", event("z", 1000, "prod/i-1", "second"))},
		EngineLogGroup("vllm"):     {page("", event("a", 1000, "prod/i-2", "first"))},
	}}

	got, err := fetchLogs(context.Background(), api, LogQuery{Environment: "prod", Source: LogSourceEngine})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Events) != 2 {
		t.Fatalf("got %d events, want 2", len(got.Events))
	}
	if got.Events[0].Message != "first" || got.Events[1].Message != "second" {
		t.Errorf("order = %q, %q; want a stable tiebreak on the event id",
			got.Events[0].Message, got.Events[1].Message)
	}
}

func TestFetchLogsAsksForTheEnvironmentsStreamsAndWindow(t *testing.T) {
	api := &fakeLogs{}
	start := time.UnixMilli(1_700_000_000_000)
	end := start.Add(time.Hour)
	if _, err := fetchLogs(context.Background(), api, LogQuery{
		Environment: "prod", Source: LogSourceBoot, Start: start, End: end,
	}); err != nil {
		t.Fatal(err)
	}
	if len(api.calls) != 1 {
		t.Fatalf("made %d calls, want 1", len(api.calls))
	}
	in := api.calls[0]
	if got := aws.ToString(in.LogStreamNamePrefix); got != "prod/" {
		t.Errorf("stream prefix = %q, want prod/", got)
	}
	if aws.ToInt64(in.StartTime) != start.UnixMilli() || aws.ToInt64(in.EndTime) != end.UnixMilli() {
		t.Errorf("window = [%d,%d], want [%d,%d]",
			aws.ToInt64(in.StartTime), aws.ToInt64(in.EndTime), start.UnixMilli(), end.UnixMilli())
	}
}

func TestFetchLogsPagesAndKeepsTheMostRecentWithinTheLimit(t *testing.T) {
	var first, second []cwltypes.FilteredLogEvent
	for i := 0; i < 6; i++ {
		first = append(first, event(fmt.Sprintf("a%d", i), int64(1000+i), "prod/i-1", fmt.Sprintf("old %d", i)))
	}
	for i := 0; i < 6; i++ {
		second = append(second, event(fmt.Sprintf("b%d", i), int64(2000+i), "prod/i-1", fmt.Sprintf("new %d", i)))
	}
	api := &fakeLogs{pages: map[string][]*cloudwatchlogs.FilterLogEventsOutput{
		BootLogGroup(): {page("more", first...), page("", second...)},
	}}

	got, err := fetchLogs(context.Background(), api, LogQuery{
		Environment: "prod", Source: LogSourceBoot, Limit: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Events) != 3 {
		t.Fatalf("kept %d events, want 3", len(got.Events))
	}
	if got.Events[0].Message != "new 3" || got.Events[2].Message != "new 5" {
		t.Errorf("kept %q..%q, want the most recent three",
			got.Events[0].Message, got.Events[2].Message)
	}
	if got.Omitted != 9 {
		t.Errorf("omitted = %d, want the 9 earlier events reported", got.Omitted)
	}
}

func TestFetchLogsFiltersToOneInstance(t *testing.T) {
	api := &fakeLogs{pages: map[string][]*cloudwatchlogs.FilterLogEventsOutput{
		BootLogGroup(): {page("",
			event("a", 1000, "prod/i-1", "from one"),
			event("b", 2000, "prod/i-2", "from two"),
		)},
	}}

	got, err := fetchLogs(context.Background(), api, LogQuery{
		Environment: "prod", Source: LogSourceBoot, Instance: "i-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Events) != 1 || got.Events[0].Instance != "i-2" {
		t.Fatalf("events = %+v, want only i-2's", got.Events)
	}
}

func TestFetchLogsToleratesAGroupThatDoesNotExist(t *testing.T) {
	api := &fakeLogs{
		pages: map[string][]*cloudwatchlogs.FilterLogEventsOutput{
			EngineLogGroup("vllm"): {page("", event("a", 1000, "prod/i-1", "served"))},
		},
		errs: map[string]error{
			EngineLogGroup("llamacpp"): &cwltypes.ResourceNotFoundException{},
		},
	}

	got, err := fetchLogs(context.Background(), api, LogQuery{Environment: "prod", Source: LogSourceEngine})
	if err != nil {
		t.Fatalf("a missing group for the other engine should not fail the read: %v", err)
	}
	if len(got.Events) != 1 || got.Events[0].Message != "served" {
		t.Errorf("events = %+v, want the group that does exist", got.Events)
	}
}

func TestFetchLogsReportsWhenNoGroupExistsAtAll(t *testing.T) {
	api := &fakeLogs{errs: map[string]error{
		EngineLogGroup("llamacpp"): &cwltypes.ResourceNotFoundException{},
		EngineLogGroup("vllm"):     &cwltypes.ResourceNotFoundException{},
	}}

	_, err := fetchLogs(context.Background(), api, LogQuery{Environment: "prod", Source: LogSourceEngine})
	if err == nil {
		t.Fatal("every group missing should be an error, not an empty result")
	}
	if !strings.Contains(err.Error(), "outfit remote bootstrap") {
		t.Errorf("error = %q, want it to name the fix", err)
	}
}

// deniedErr is an IAM refusal as the SDK surfaces it.
type deniedErr struct{}

func (deniedErr) Error() string                 { return "AccessDeniedException: not authorized" }
func (deniedErr) ErrorCode() string             { return "AccessDeniedException" }
func (deniedErr) ErrorMessage() string          { return "not authorized" }
func (deniedErr) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

func TestFetchLogsExplainsAccessDenied(t *testing.T) {
	api := &fakeLogs{errs: map[string]error{BootLogGroup(): deniedErr{}}}

	_, err := fetchLogs(context.Background(), api, LogQuery{Environment: "prod", Source: LogSourceBoot})
	if err == nil {
		t.Fatal("expected the denial to be reported")
	}
	if !strings.Contains(err.Error(), "logs:FilterLogEvents") {
		t.Errorf("error = %q, want it to name the permission needed", err)
	}
}

func TestFetchLogsExplainsExpiredCredentials(t *testing.T) {
	api := &fakeLogs{errs: map[string]error{BootLogGroup(): errors.New("ExpiredToken: the token has expired")}}

	_, err := fetchLogs(context.Background(), api, LogQuery{Environment: "prod", Source: LogSourceBoot})
	if err == nil {
		t.Fatal("expected the expiry to be reported")
	}
	if !strings.Contains(err.Error(), refreshCredsHint) {
		t.Errorf("error = %q, want the refresh hint", err)
	}
}

func TestFetchLogsReturnsNoEventsWithoutError(t *testing.T) {
	api := &fakeLogs{pages: map[string][]*cloudwatchlogs.FilterLogEventsOutput{
		BootLogGroup(): {page("")},
	}}

	got, err := fetchLogs(context.Background(), api, LogQuery{Environment: "prod", Source: LogSourceBoot})
	if err != nil {
		t.Fatalf("an empty window is not an error: %v", err)
	}
	if len(got.Events) != 0 {
		t.Errorf("events = %+v, want none", got.Events)
	}
}

func TestFetchLogsStopsPagingAnUnboundedWindow(t *testing.T) {
	// Every page offers another token, so only the page cap ends the walk.
	endless := make([]*cloudwatchlogs.FilterLogEventsOutput, 0, maxLogPages+1)
	for i := 0; i <= maxLogPages; i++ {
		endless = append(endless, page("more", event(fmt.Sprintf("e%d", i), int64(1000+i), "prod/i-1", "line")))
	}
	api := &fakeLogs{pages: map[string][]*cloudwatchlogs.FilterLogEventsOutput{BootLogGroup(): endless}}

	_, err := fetchLogs(context.Background(), api, LogQuery{Environment: "prod", Source: LogSourceBoot})
	if err == nil {
		t.Fatal("an endlessly paging window should be reported, not truncated silently")
	}
	if !strings.Contains(err.Error(), "--since") {
		t.Errorf("error = %q, want it to suggest narrowing the window", err)
	}
	if len(api.calls) != maxLogPages {
		t.Errorf("made %d requests, want the cap of %d", len(api.calls), maxLogPages)
	}
}

func TestFetchLogsRequiresAnEnvironmentName(t *testing.T) {
	_, err := FetchLogs(context.Background(), Config{Region: "eu-west-2"}, LogQuery{Source: LogSourceEngine})
	if err == nil {
		t.Fatal("a config with no environment cannot identify its streams")
	}
	if !strings.Contains(err.Error(), "outfit remote deploy") {
		t.Errorf("error = %q, want it to say how to re-register", err)
	}
}

func TestLogEventTrimsTheShippedNewline(t *testing.T) {
	got := logEventFrom(event("a", 1000, "prod/i-1", "a line\n"), LogSourceEngine)
	if got.Message != "a line" {
		t.Errorf("message = %q, want the trailing newline trimmed", got.Message)
	}
	if got.Instance != "i-1" || got.Source != LogSourceEngine || got.ID != "a" {
		t.Errorf("event = %+v, want it attributed to the source and instance", got)
	}
	if !got.Timestamp.Equal(time.UnixMilli(1000)) {
		t.Errorf("timestamp = %s, want the event's own", got.Timestamp)
	}
}
