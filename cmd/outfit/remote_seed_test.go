package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/lucinate-ai/outfit/internal/remote"
)

// stubSeedSeams points the control-plane discovery at a test server, with the
// seed URL configured unless seedURL is empty — which is how the
// not-configured path is exercised.
//
// It pins the AWS credential chain too. The seed subcommands load an AWS config
// before reaching the (stubbed) discovery, so without this they resolve
// credentials for real: that quietly succeeds on a developer machine and fails
// on a CI runner with no IMDS, which is exactly how this arrived broken.
func stubSeedSeams(t *testing.T, serverURL, seedURL string) {
	t.Helper()
	stubAWSEnv(t)
	orig := deployDiscoverFn
	t.Cleanup(func() { deployDiscoverFn = orig })
	deployDiscoverFn = func(context.Context, aws.Config, string) (remote.ControlPlane, error) {
		return remote.ControlPlane{Config: remote.Config{
			StartURL: serverURL, StopURL: serverURL, DeployURL: serverURL,
			SeedURL: seedURL, Region: "us-east-1",
		}}, nil
	}
}

// seedServer replies with body for every seed request, recording what it got.
func seedServer(t *testing.T, status int, body string) (*httptest.Server, *struct {
	Method string
	ID     string
	Body   map[string]any
},
) {
	t.Helper()
	got := &struct {
		Method string
		ID     string
		Body   map[string]any
	}{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Method = r.Method
		got.ID = r.URL.Query().Get("id")
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &got.Body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server, got
}

func TestRemoteSeed_StartPostsWhatTheOutfitNames(t *testing.T) {
	server, got := seedServer(t, 200,
		`{"seedId":"llamacpp--m","instanceId":"i-1","started":true,"weightsPrefix":"models/llamacpp/m/"}`)
	stubSeedSeams(t, server.URL, server.URL)
	writeDeployEnvOutfit(t, "testenv")

	out := captureStdout(t, func() {
		if err := cmdRemoteSeed([]string{"start"}); err != nil {
			t.Fatalf("seed start: %v", err)
		}
	})

	if got.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.Method)
	}
	// What to seed comes from the Outfit, exactly as deploy resolves it.
	if got.Body["runner"] != "llamacpp" || got.Body["modelId"] != "unsloth/Qwen3.6-27B-MTP-GGUF" {
		t.Errorf("posted the wrong thing to seed: %+v", got.Body)
	}
	// A caller must never encode the S3 layout.
	if _, ok := got.Body["weightsPrefix"]; ok {
		t.Error("the request must not carry a weights prefix")
	}
	if !strings.Contains(out, "llamacpp--m") || !strings.Contains(out, "outfit remote seed status") {
		t.Errorf("start should name the seed and how to follow it, got:\n%s", out)
	}
}

func TestRemoteSeed_StartSaysWhenItJoinedRatherThanStarted(t *testing.T) {
	server, _ := seedServer(t, 200, `{"seedId":"llamacpp--m","started":false,"joined":true}`)
	stubSeedSeams(t, server.URL, server.URL)
	writeDeployEnvOutfit(t, "testenv")

	out := captureStdout(t, func() {
		if err := cmdRemoteSeed([]string{"start"}); err != nil {
			t.Fatalf("seed start: %v", err)
		}
	})
	// A repeat must not look like a fresh start.
	if !strings.Contains(out, "joined the seed already running") {
		t.Errorf("a repeated start should say it joined, got:\n%s", out)
	}
}

func TestRemoteSeed_StartReportsAlreadySeeded(t *testing.T) {
	server, _ := seedServer(t, 200, `{"seedId":"llamacpp--m","started":false,"alreadySeeded":true}`)
	stubSeedSeams(t, server.URL, server.URL)
	writeDeployEnvOutfit(t, "testenv")

	out := captureStdout(t, func() {
		if err := cmdRemoteSeed([]string{"start"}); err != nil {
			t.Fatalf("seed start: %v", err)
		}
	})
	if !strings.Contains(out, "--force") {
		t.Errorf("should point at --force, got:\n%s", out)
	}
}

func TestRemoteSeed_ForceAndRevisionTravel(t *testing.T) {
	server, got := seedServer(t, 200, `{"seedId":"llamacpp--m","started":true}`)
	stubSeedSeams(t, server.URL, server.URL)
	writeDeployEnvOutfit(t, "testenv")

	captureStdout(t, func() {
		if err := cmdRemoteSeed([]string{"start", "--force", "--revision", "abc123"}); err != nil {
			t.Fatalf("seed start: %v", err)
		}
	})
	if got.Body["force"] != true || got.Body["revision"] != "abc123" {
		t.Errorf("force/revision did not travel: %+v", got.Body)
	}
}

func TestRemoteSeed_StatusReportsAFailedSeed(t *testing.T) {
	server, got := seedServer(t, 200,
		`{"seedId":"vllm--m","state":"failed","modelId":"org/m","error":"checksum mismatch","progressPercent":41}`)
	stubSeedSeams(t, server.URL, server.URL)

	out := captureStdout(t, func() {
		if err := cmdRemoteSeed([]string{"status", "vllm--m"}); err != nil {
			t.Fatalf("seed status: %v", err)
		}
	})
	if got.ID != "vllm--m" {
		t.Errorf("id = %q, want the seed id on the query", got.ID)
	}
	if !strings.Contains(out, "failed") || !strings.Contains(out, "checksum mismatch") {
		t.Errorf("status should report the failure, got:\n%s", out)
	}
}

func TestRemoteSeed_StatusShowsTheFileOnlyWhileItIsStillWorking(t *testing.T) {
	// While transferring, the current file is what you want to see.
	server, _ := seedServer(t, 200,
		`{"seedId":"vllm--m","state":"transferring","currentFile":"model-00009.safetensors","filesTotal":17,"filesDone":8,"bytesTotal":2048,"bytesDone":1024,"progressPercent":50}`)
	stubSeedSeams(t, server.URL, server.URL)

	out := captureStdout(t, func() {
		if err := cmdRemoteSeed([]string{"status", "vllm--m"}); err != nil {
			t.Fatalf("seed status: %v", err)
		}
	})
	if !strings.Contains(out, "model-00009.safetensors") {
		t.Errorf("a running seed should show its current file, got:\n%s", out)
	}
	if !strings.Contains(out, "8/17 files") || !strings.Contains(out, "1.0 KiB of 2.0 KiB") {
		t.Errorf("progress should show files and bytes, got:\n%s", out)
	}
}

func TestRemoteSeed_StatusHidesTheFileOnceFinished(t *testing.T) {
	// Once terminal, the last file it happened to touch is noise.
	server, _ := seedServer(t, 200,
		`{"seedId":"vllm--m","state":"succeeded","currentFile":"model-00017.safetensors","durationSeconds":312}`)
	stubSeedSeams(t, server.URL, server.URL)

	out := captureStdout(t, func() {
		if err := cmdRemoteSeed([]string{"status", "vllm--m"}); err != nil {
			t.Fatalf("seed status: %v", err)
		}
	})
	if strings.Contains(out, "model-00017.safetensors") {
		t.Errorf("a finished seed should not show a current file, got:\n%s", out)
	}
	if !strings.Contains(out, "312s") {
		t.Errorf("a finished seed should report how long it took, got:\n%s", out)
	}
}

func TestRemoteSeed_StatusRejectsAnUnknownSeed(t *testing.T) {
	server, _ := seedServer(t, 404, `{"seedId":"nope","state":"unknown","error":"no seed known"}`)
	stubSeedSeams(t, server.URL, server.URL)

	err := cmdRemoteSeed([]string{"status", "nope"})
	if err == nil || !strings.Contains(err.Error(), `no seed "nope" is known`) {
		t.Errorf("want an unknown-seed error, got %v", err)
	}
}

func TestRemoteSeed_ListStatesPlainlyWhenEmpty(t *testing.T) {
	server, _ := seedServer(t, 200, `{"seeds":[],"count":0}`)
	stubSeedSeams(t, server.URL, server.URL)

	out := captureStdout(t, func() {
		if err := cmdRemoteSeed([]string{"ls"}); err != nil {
			t.Fatalf("seed ls: %v", err)
		}
	})
	// "none running" must be distinguishable from a command that failed quietly.
	if !strings.Contains(out, "No seeds are running.") {
		t.Errorf("an empty list should say so, got:\n%q", out)
	}
}

func TestRemoteSeed_ListShowsEachSeed(t *testing.T) {
	server, _ := seedServer(t, 200,
		`{"seeds":[{"seedId":"vllm--m","modelId":"org/m","state":"transferring","ageSeconds":125}],"count":1}`)
	stubSeedSeams(t, server.URL, server.URL)

	out := captureStdout(t, func() {
		if err := cmdRemoteSeed([]string{"ls"}); err != nil {
			t.Fatalf("seed ls: %v", err)
		}
	})
	for _, want := range []string{"vllm--m", "transferring", "org/m", "2m"} {
		if !strings.Contains(out, want) {
			t.Errorf("listing should contain %q, got:\n%s", want, out)
		}
	}
}

func TestRemoteSeed_StopIsSafeWhenNothingIsRunning(t *testing.T) {
	server, got := seedServer(t, 200, `{"seedId":"vllm--m","stopped":false,"message":"not running"}`)
	stubSeedSeams(t, server.URL, server.URL)

	out := captureStdout(t, func() {
		// Stopping twice must not be an error.
		if err := cmdRemoteSeed([]string{"stop", "vllm--m"}); err != nil {
			t.Fatalf("seed stop: %v", err)
		}
	})
	if got.Method != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", got.Method)
	}
	if !strings.Contains(out, "is not running") {
		t.Errorf("stop should say nothing was running, got:\n%s", out)
	}
}

func TestRemoteSeed_StopReportsWhatItStopped(t *testing.T) {
	server, _ := seedServer(t, 200, `{"seedId":"vllm--m","stopped":true,"instanceIds":["i-1"]}`)
	stubSeedSeams(t, server.URL, server.URL)

	out := captureStdout(t, func() {
		if err := cmdRemoteSeed([]string{"stop", "vllm--m"}); err != nil {
			t.Fatalf("seed stop: %v", err)
		}
	})
	if !strings.Contains(out, "stopped vllm--m") || !strings.Contains(out, "i-1") {
		t.Errorf("stop should name what it stopped, got:\n%s", out)
	}
}

func TestRemoteSeed_CapReachedIsNamed(t *testing.T) {
	server, _ := seedServer(t, 429, `{"error":"3 seeds are already running (cap 3) — wait for one to finish"}`)
	stubSeedSeams(t, server.URL, server.URL)
	writeDeployEnvOutfit(t, "testenv")

	err := cmdRemoteSeed([]string{"start"})
	if err == nil || !strings.Contains(err.Error(), "cap 3") {
		t.Errorf("a refusal at the cap should say so, got %v", err)
	}
}

func TestRemoteSeed_NoSeedURLNamesTheValueToAdd(t *testing.T) {
	server, _ := seedServer(t, 200, `{}`)
	// A remote config written before the seed Lambda existed.
	stubSeedSeams(t, server.URL, "")

	err := cmdRemoteSeed([]string{"ls"})
	if err == nil {
		t.Fatal("want an error when no seed endpoint is configured")
	}
	for _, want := range []string{"seed_url", "SeedUrl", "OUTFIT_REMOTE_SEED_URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should name %q, got: %v", want, err)
		}
	}
}

func TestRemoteSeed_UnknownSubcommand(t *testing.T) {
	err := cmdRemoteSeed([]string{"frobnicate"})
	if err == nil || !strings.Contains(err.Error(), "unknown seed subcommand") {
		t.Errorf("want an unknown-subcommand error, got %v", err)
	}
}

func TestRemoteSeed_NoSubcommandShowsUsage(t *testing.T) {
	err := cmdRemoteSeed([]string{})
	if err == nil || !strings.Contains(err.Error(), "start|status|ls|stop") {
		t.Errorf("want usage, got %v", err)
	}
}

func TestRemoteSeed_StatusNeedsASeedID(t *testing.T) {
	err := cmdRemoteSeed([]string{"status"})
	if err == nil || !strings.Contains(err.Error(), "seed ls") {
		t.Errorf("want usage pointing at ls, got %v", err)
	}
}

func TestHumanAgeAndBytes(t *testing.T) {
	if got := humanAge(45); got != "45s" {
		t.Errorf("humanAge(45) = %q", got)
	}
	if got := humanAge(125); got != "2m" {
		t.Errorf("humanAge(125) = %q", got)
	}
	if got := humanAge(7300); got != "2h1m" {
		t.Errorf("humanAge(7300) = %q", got)
	}
	if got := humanAge(0); got != "-" {
		t.Errorf("humanAge(0) = %q", got)
	}
	if got := humanBytes(512); got != "512 B" {
		t.Errorf("humanBytes(512) = %q", got)
	}
	if got := humanBytes(1536); got != "1.5 KiB" {
		t.Errorf("humanBytes(1536) = %q", got)
	}
	if got := humanBytes(32 * 1024 * 1024 * 1024); got != "32.0 GiB" {
		t.Errorf("humanBytes(32GiB) = %q", got)
	}
}
