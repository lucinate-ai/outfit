package cloud

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// scheduleServer records the request the client sends and replies with reply.
func scheduleServer(t *testing.T, status int, reply string) (*httptest.Server, *http.Request, *[]byte) {
	t.Helper()
	var got http.Request
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = *r
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(server.Close)
	return server, &got, &body
}

func TestSetSchedules(t *testing.T) {
	stubAWSEnv(t)
	server, got, body := scheduleServer(t, http.StatusOK,
		`{"environment":"dev","schedules":[{"action":"start","cron":"0 8 * * 1-5","timezone":"Europe/London"}],"next":{"start":"2026-10-06T07:00:00.000Z","stop":""}}`)
	cfg := Config{StartURL: server.URL, StopURL: server.URL, ScheduleURL: server.URL, Region: "eu-west-1", Environment: "dev"}

	list, err := SetSchedules(context.Background(), cfg, []Schedule{
		{Action: "start", Cron: "0 8 * * 1-5", Timezone: "Europe/London"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != http.MethodPut {
		t.Errorf("set should PUT, got %s", got.Method)
	}
	if got.URL.Query().Get("env") != "dev" {
		t.Errorf("set should carry the environment, got %q", got.URL.RawQuery)
	}
	var sent struct {
		Schedules []Schedule `json:"schedules"`
	}
	if err := json.Unmarshal(*body, &sent); err != nil {
		t.Fatal(err)
	}
	if len(sent.Schedules) != 1 || sent.Schedules[0].Cron != "0 8 * * 1-5" || sent.Schedules[0].Timezone != "Europe/London" {
		t.Errorf("unexpected body: %s", *body)
	}
	if len(list.Schedules) != 1 || list.Next.Start != "2026-10-06T07:00:00.000Z" || list.Next.Stop != "" {
		t.Errorf("unexpected reply: %+v", list)
	}
}

func TestSetSchedules_NilSendsAnEmptyList(t *testing.T) {
	stubAWSEnv(t)
	server, _, body := scheduleServer(t, http.StatusOK, `{"environment":"dev","schedules":[],"next":{}}`)
	cfg := Config{ScheduleURL: server.URL, Region: "eu-west-1", Environment: "dev"}
	if _, err := SetSchedules(context.Background(), cfg, nil); err != nil {
		t.Fatal(err)
	}
	if string(*body) != `{"schedules":[]}` {
		t.Errorf("a nil list should be sent as an empty one, got %s", *body)
	}
}

func TestGetSchedules(t *testing.T) {
	stubAWSEnv(t)
	server, got, _ := scheduleServer(t, http.StatusOK, `{"environment":"dev","schedules":[],"next":{"start":"","stop":""}}`)
	cfg := Config{ScheduleURL: server.URL, Region: "eu-west-1", Environment: "dev"}
	list, err := GetSchedules(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != http.MethodGet {
		t.Errorf("get should GET, got %s", got.Method)
	}
	if len(list.Schedules) != 0 {
		t.Errorf("expected no schedules, got %+v", list.Schedules)
	}
}

func TestClearSchedules(t *testing.T) {
	stubAWSEnv(t)
	server, got, _ := scheduleServer(t, http.StatusOK, `{"environment":"dev","schedules":[],"next":{}}`)
	cfg := Config{ScheduleURL: server.URL, Region: "eu-west-1", Environment: "dev"}
	if _, err := ClearSchedules(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if got.Method != http.MethodDelete {
		t.Errorf("clear should DELETE, got %s", got.Method)
	}
}

func TestSchedules_NoScheduleURL(t *testing.T) {
	stubAWSEnv(t)
	cfg := Config{StartURL: "https://start/", StopURL: "https://stop/", Region: "eu-west-1"}
	for name, call := range map[string]func() error{
		"set":   func() error { _, err := SetSchedules(context.Background(), cfg, nil); return err },
		"get":   func() error { _, err := GetSchedules(context.Background(), cfg); return err },
		"clear": func() error { _, err := ClearSchedules(context.Background(), cfg); return err },
	} {
		err := call()
		if err == nil || !strings.Contains(err.Error(), "spinloop cloud bootstrap") {
			t.Errorf("%s: expected an error naming bootstrap, got %v", name, err)
		}
	}
}

func TestSchedules_RejectionCarriesTheReplysDetail(t *testing.T) {
	stubAWSEnv(t)
	server, _, _ := scheduleServer(t, http.StatusBadRequest,
		`{"error":"invalid cron expression \"nope\": expected five fields"}`)
	cfg := Config{ScheduleURL: server.URL, Region: "eu-west-1", Environment: "dev"}
	_, err := SetSchedules(context.Background(), cfg, []Schedule{{Action: "start", Cron: "nope"}})
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") || !strings.Contains(err.Error(), "expected five fields") {
		t.Errorf("expected the reply's detail, got %v", err)
	}
}

func TestSchedules_ForbiddenSaysWhatToCheck(t *testing.T) {
	stubAWSEnv(t)
	server, _, _ := scheduleServer(t, http.StatusForbidden, `{"Message":"forbidden"}`)
	cfg := Config{ScheduleURL: server.URL, Region: "eu-west-1", Environment: "dev"}
	_, err := GetSchedules(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "lambda:InvokeFunctionUrl") {
		t.Errorf("expected the permission hint, got %v", err)
	}
}

func TestSchedules_UnreadableReply(t *testing.T) {
	stubAWSEnv(t)
	server, _, _ := scheduleServer(t, http.StatusOK, `not json`)
	cfg := Config{ScheduleURL: server.URL, Region: "eu-west-1", Environment: "dev"}
	if _, err := GetSchedules(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "unreadable") {
		t.Errorf("expected an unreadable-reply error, got %v", err)
	}
}
