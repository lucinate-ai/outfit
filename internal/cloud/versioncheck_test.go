package cloud

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// captureVersionWarning points the warning at a buffer, sets the CLI version,
// and re-arms the once-per-process guard, restoring all three afterwards.
func captureVersionWarning(t *testing.T, cli string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	origVersion, origWriter := cliVersion, versionWarnWriter
	t.Cleanup(func() {
		cliVersion, versionWarnWriter = origVersion, origWriter
		versionWarnOnce = sync.Once{}
	})
	cliVersion, versionWarnWriter = cli, &buf
	versionWarnOnce = sync.Once{}
	return &buf
}

func headerWith(v string) http.Header {
	h := http.Header{}
	if v != "" {
		h.Set(ControlPlaneVersionHeader, v)
	}
	return h
}

func TestVersionsDiffer(t *testing.T) {
	cases := []struct {
		name, cli, controlPlane string
		want                    bool
	}{
		{"different", "1.30.0", "1.28.0", true},
		{"same", "1.30.0", "1.30.0", false},
		{"v prefix on the CLI", "v1.30.0", "1.30.0", false},
		{"v prefix on the control plane", "1.30.0", "v1.30.0", false},
		{"header absent", "1.30.0", "", false},
		{"control plane dev", "1.30.0", "dev", false},
		{"cli dev", "dev", "1.30.0", false},
		{"cli empty", "", "1.30.0", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := versionsDiffer(c.cli, c.controlPlane); got != c.want {
				t.Errorf("versionsDiffer(%q, %q) = %v, want %v", c.cli, c.controlPlane, got, c.want)
			}
		})
	}
}

func TestCheckControlPlaneVersion_WarnsOnceOnMismatch(t *testing.T) {
	buf := captureVersionWarning(t, "1.30.0")
	checkControlPlaneVersion(headerWith("1.28.0"))
	checkControlPlaneVersion(headerWith("1.28.0"))

	out := buf.String()
	if strings.Count(out, "Warning:") != 1 {
		t.Errorf("expected exactly one warning, got %q", out)
	}
	for _, want := range []string{"1.28.0", "1.30.0", "spinloop cloud bootstrap"} {
		if !strings.Contains(out, want) {
			t.Errorf("warning %q does not mention %q", out, want)
		}
	}
}

func TestCheckControlPlaneVersion_SilentWhenNotComparable(t *testing.T) {
	for _, h := range []string{"1.30.0", "v1.30.0", "", "dev"} {
		buf := captureVersionWarning(t, "1.30.0")
		checkControlPlaneVersion(headerWith(h))
		if buf.Len() != 0 {
			t.Errorf("header %q: expected no warning, got %q", h, buf.String())
		}
	}
}

// Every control plane call passes through send or callStats; each reads the
// header off the response to the call itself.
func TestControlPlaneCalls_WarnFromTheirOwnResponse(t *testing.T) {
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set(ControlPlaneVersionHeader, "1.28.0")
		w.Write([]byte(`{"state":"stopped"}`))
	}))
	defer server.Close()
	cfg := Config{StartURL: server.URL, StatsURL: server.URL, Region: "eu-west-1"}

	t.Run("status", func(t *testing.T) {
		buf := captureVersionWarning(t, "1.30.0")
		if _, err := Status(context.Background(), cfg); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(buf.String(), "1.28.0") {
			t.Errorf("expected a warning naming the control plane version, got %q", buf.String())
		}
	})

	t.Run("stats", func(t *testing.T) {
		buf := captureVersionWarning(t, "1.30.0")
		if _, err := Stats(context.Background(), cfg); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(buf.String(), "1.28.0") {
			t.Errorf("expected a warning naming the control plane version, got %q", buf.String())
		}
	})
}

func TestControlPlaneCalls_WarnOnErrorResponses(t *testing.T) {
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set(ControlPlaneVersionHeader, "1.28.0")
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"message":"boom"}`))
	}))
	defer server.Close()

	buf := captureVersionWarning(t, "1.30.0")
	if _, err := Status(context.Background(), Config{StartURL: server.URL, Region: "eu-west-1"}); err == nil {
		t.Fatal("expected the 502 to be an error")
	}
	if !strings.Contains(buf.String(), "1.28.0") {
		t.Errorf("expected a warning on an error response, got %q", buf.String())
	}
}

func TestControlPlaneCalls_NoHeaderNoWarning(t *testing.T) {
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"stopped"}`))
	}))
	defer server.Close()

	buf := captureVersionWarning(t, "1.30.0")
	if _, err := Status(context.Background(), Config{StartURL: server.URL, Region: "eu-west-1"}); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no warning without the header, got %q", buf.String())
	}
}
