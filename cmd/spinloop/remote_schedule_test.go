package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/spinloop-ai/spinloop/internal/remote"
)

// scheduleFixture serves the schedule Lambda's reply and registers a default
// environment whose schedule URL points at it. It returns what the last
// request carried.
type scheduleRequest struct {
	method string
	env    string
	body   string
}

func scheduleFixture(t *testing.T, status int, reply string) *scheduleRequest {
	t.Helper()
	isolateConfig(t)
	stubAWSEnv(t)
	got := &scheduleRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.method, got.env, got.body = r.Method, r.URL.Query().Get("env"), string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(server.Close)
	writeRemoteConfig(t, server.URL)
	path := must1(remote.EnvConfigPath("default"))
	var cfg remote.Config
	if err := json.Unmarshal(must1(os.ReadFile(path)), &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.ScheduleURL = server.URL
	cfg.Environment = "default"
	if err := os.WriteFile(path, must1(json.Marshal(cfg)), 0o600); err != nil {
		t.Fatal(err)
	}
	return got
}

const officeReply = `{"environment":"default","schedules":[` +
	`{"action":"start","cron":"0 8 * * 1-5","timezone":"Europe/London"},` +
	`{"action":"stop","cron":"0 18 * * 1-5","timezone":"Europe/London"}],` +
	`"next":{"start":"2026-10-06T07:00:00.000Z","stop":"2026-10-05T17:00:00.000Z"}}`

func TestRemoteSchedule_SetSendsTheListAndPrintsIt(t *testing.T) {
	got := scheduleFixture(t, http.StatusOK, officeReply)

	out := captureStdout(t, func() {
		err := cmdRemoteSchedule([]string{
			"set", "--env", "default",
			"--start", "0 8 * * 1-5", "--stop", "0 18 * * 1-5", "--timezone", "Europe/London",
		})
		if err != nil {
			t.Errorf("set: %v", err)
		}
	})

	if got.method != http.MethodPut || got.env != "default" {
		t.Errorf("unexpected request: %+v", got)
	}
	var sent struct {
		Schedules []remote.Schedule `json:"schedules"`
	}
	if err := json.Unmarshal([]byte(got.body), &sent); err != nil {
		t.Fatal(err)
	}
	want := []remote.Schedule{
		{Action: "start", Cron: "0 8 * * 1-5", Timezone: "Europe/London"},
		{Action: "stop", Cron: "0 18 * * 1-5", Timezone: "Europe/London"},
	}
	if len(sent.Schedules) != 2 || sent.Schedules[0] != want[0] || sent.Schedules[1] != want[1] {
		t.Errorf("sent %+v, want %+v", sent.Schedules, want)
	}
	for _, line := range []string{
		"start  0 8 * * 1-5  (Europe/London)",
		"stop   0 18 * * 1-5  (Europe/London)",
		"next start: 2026-10-06T07:00:00Z",
		"next stop: 2026-10-05T17:00:00Z",
	} {
		if !strings.Contains(out, line) {
			t.Errorf("output missing %q:\n%s", line, out)
		}
	}
}

func TestRemoteSchedule_SetAcceptsRepeatedFlags(t *testing.T) {
	got := scheduleFixture(t, http.StatusOK, officeReply)
	captureStdout(t, func() {
		err := cmdRemoteSchedule([]string{
			"set", "--env", "default", "--start", "0 8 * * 1-5", "--start", "0 13 * * 6",
		})
		if err != nil {
			t.Errorf("set: %v", err)
		}
	})
	if strings.Count(got.body, `"action":"start"`) != 2 {
		t.Errorf("expected two start schedules, got %s", got.body)
	}
}

func TestRemoteSchedule_SetWithNothingPointsAtClear(t *testing.T) {
	got := scheduleFixture(t, http.StatusOK, officeReply)
	err := cmdRemoteSchedule([]string{"set", "--env", "default"})
	if err == nil || !strings.Contains(err.Error(), "schedule clear") {
		t.Errorf("expected an error naming clear, got %v", err)
	}
	if got.method != "" {
		t.Errorf("nothing should be sent, got a %s", got.method)
	}
}

func TestRemoteSchedule_ShowListsSchedules(t *testing.T) {
	got := scheduleFixture(t, http.StatusOK, officeReply)
	out := captureStdout(t, func() {
		if err := cmdRemoteSchedule([]string{"show", "--env", "default"}); err != nil {
			t.Errorf("show: %v", err)
		}
	})
	if got.method != http.MethodGet {
		t.Errorf("show should GET, got %s", got.method)
	}
	if !strings.Contains(out, "next start:") || !strings.Contains(out, "next stop:") {
		t.Errorf("show should report the next runs:\n%s", out)
	}
}

func TestRemoteSchedule_ShowSaysWhenThereAreNone(t *testing.T) {
	scheduleFixture(t, http.StatusOK, `{"environment":"default","schedules":[],"next":{"start":"","stop":""}}`)
	out := captureStdout(t, func() {
		if err := cmdRemoteSchedule([]string{"show", "--env", "default"}); err != nil {
			t.Errorf("show: %v", err)
		}
	})
	if strings.TrimSpace(out) != "no schedules" {
		t.Errorf("expected %q, got %q", "no schedules", out)
	}
	if strings.Contains(out, "next start") || strings.Contains(out, "next stop") {
		t.Errorf("no next-run lines expected:\n%s", out)
	}
}

func TestRemoteSchedule_OmitsANextRunThatDoesNotExist(t *testing.T) {
	scheduleFixture(t, http.StatusOK, `{"environment":"default","schedules":[{"action":"start","cron":"0 8 * * *","timezone":"UTC"}],"next":{"start":"2026-10-06T08:00:00.000Z","stop":""}}`)
	out := captureStdout(t, func() {
		if err := cmdRemoteSchedule([]string{"show", "--env", "default"}); err != nil {
			t.Errorf("show: %v", err)
		}
	})
	if !strings.Contains(out, "next start:") || strings.Contains(out, "next stop") {
		t.Errorf("only the start should have a next run:\n%s", out)
	}
}

func TestRemoteSchedule_ClearDeletes(t *testing.T) {
	got := scheduleFixture(t, http.StatusOK, `{"environment":"default","schedules":[],"next":{}}`)
	out := captureStdout(t, func() {
		if err := cmdRemoteSchedule([]string{"clear", "--env", "default"}); err != nil {
			t.Errorf("clear: %v", err)
		}
	})
	if got.method != http.MethodDelete {
		t.Errorf("clear should DELETE, got %s", got.method)
	}
	if !strings.Contains(out, "no schedules") {
		t.Errorf("clear should say there are none:\n%s", out)
	}
}

func TestRemoteSchedule_ControlPlaneRejectionIsTheError(t *testing.T) {
	scheduleFixture(t, http.StatusBadRequest, `{"error":"invalid cron expression \"x\": expected five fields"}`)
	err := cmdRemoteSchedule([]string{"set", "--env", "default", "--start", "x"})
	if err == nil || !strings.Contains(err.Error(), "expected five fields") {
		t.Errorf("expected the control plane's reason, got %v", err)
	}
}

func TestRemoteSchedule_OlderControlPlaneNamesTheFix(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	writeRemoteConfig(t, "https://start.example/")
	err := cmdRemoteSchedule([]string{"show", "--env", "default"})
	if err == nil || !strings.Contains(err.Error(), "spinloop remote bootstrap") {
		t.Errorf("expected an error naming bootstrap, got %v", err)
	}
}

func TestRemoteSchedule_NeedsAnEnvironment(t *testing.T) {
	isolateConfig(t)
	err := cmdRemoteSchedule([]string{"show"})
	if err == nil || !strings.Contains(err.Error(), "--env") {
		t.Errorf("expected the no-environment error, got %v", err)
	}
}
