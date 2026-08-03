package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/lucinate-ai/outfit/internal/remote"
)

// writeDeployOutfit writes an Outfit (and optionally a preset) into a temp dir
// and returns the Outfit's path.
func writeDeployOutfit(t *testing.T, outfitBody, presetBody string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "Outfit")
	if err := os.WriteFile(path, []byte(outfitBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if presetBody != "" {
		if err := os.WriteFile(filepath.Join(dir, "preset.ini"), []byte(presetBody), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

const qwenPreset = `[*]
host  = 127.0.0.1
port  = 8080
jinja = 1
ngl   = 99

[qwen3.6-27b]
hf               = unsloth/Qwen3.6-27B-MTP-GGUF:UD-Q6_K_XL
ctx-size         = 131072
fa               = on
spec-type        = draft-mtp
spec-draft-n-max = 2
`

func deployConfigFrom(t *testing.T, outfitBody, presetBody string) remote.DeployConfig {
	t.Helper()
	path := writeDeployOutfit(t, outfitBody, presetBody)
	sel, _, err := readOutfit("test", path)
	if err != nil {
		t.Fatal(err)
	}
	dc, err := deployConfigFor(sel, path)
	if err != nil {
		t.Fatalf("deployConfigFor: %v", err)
	}
	return dc
}

func TestDeployConfigFor_FromPreset(t *testing.T) {
	dc := deployConfigFrom(t,
		"PROVIDER llamacpp\nALIAS qwen3.6-27b\nCONTEXT 131072\nPRESET ./preset.ini\n", qwenPreset)

	if dc.Runner != "llamacpp" {
		t.Errorf("runner = %q, want llamacpp", dc.Runner)
	}
	if dc.ModelID != "unsloth/Qwen3.6-27B-MTP-GGUF" || dc.Quant != "UD-Q6_K_XL" {
		t.Errorf("model = %q quant = %q, want the repo and quant split", dc.ModelID, dc.Quant)
	}
	if dc.ContextSize != 131072 {
		t.Errorf("contextSize = %d, want 131072", dc.ContextSize)
	}
	if dc.ServedModelName != "qwen3.6-27b" {
		t.Errorf("servedModelName = %q, want the ALIAS", dc.ServedModelName)
	}

	args := strings.Join(dc.ServeArgs, " ")
	// From the [*] globals: losing these would serve on CPU, and without tool
	// calling, which is the whole point of the deployment.
	for _, want := range []string{"--jinja", "--n-gpu-layers 99"} {
		if !strings.Contains(args, want) {
			t.Errorf("serveArgs %q is missing the [*] global %q", args, want)
		}
	}
	// From the section.
	for _, want := range []string{"--flash-attn on", "--spec-type draft-mtp", "--spec-draft-n-max 2"} {
		if !strings.Contains(args, want) {
			t.Errorf("serveArgs %q is missing %q", args, want)
		}
	}
	// Set by the cloud, so they must not be echoed back.
	for _, unwanted := range []string{"--host", "--port", "--ctx-size", "--alias", "--hf-repo", "127.0.0.1", "8080"} {
		if strings.Contains(args, unwanted) {
			t.Errorf("serveArgs %q should not carry the cloud-owned %q", args, unwanted)
		}
	}
}

func TestDeployConfigFor_ContextAndModelFallBackToPreset(t *testing.T) {
	// No CONTEXT and no MODEL in the Outfit: both come from the preset, so one
	// preset can drive a local serve and a deploy.
	dc := deployConfigFrom(t, "PROVIDER llamacpp\nALIAS qwen3.6-27b\nPRESET ./preset.ini\n", qwenPreset)
	if dc.ContextSize != 131072 {
		t.Errorf("contextSize = %d, want the preset's ctx-size", dc.ContextSize)
	}
	if dc.ModelID != "unsloth/Qwen3.6-27B-MTP-GGUF" {
		t.Errorf("modelId = %q, want the preset's hf", dc.ModelID)
	}
}

func TestDeployConfigFor_OutfitOverridesPreset(t *testing.T) {
	dc := deployConfigFrom(t,
		"PROVIDER llamacpp\nALIAS qwen3.6-27b\nMODEL unsloth/Other-GGUF:Q4_K_M\nCONTEXT 32768\nPRESET ./preset.ini\n",
		qwenPreset)
	if dc.ModelID != "unsloth/Other-GGUF" || dc.Quant != "Q4_K_M" {
		t.Errorf("MODEL should win over the preset's hf, got %q:%q", dc.ModelID, dc.Quant)
	}
	if dc.ContextSize != 32768 {
		t.Errorf("CONTEXT should win over the preset's ctx-size, got %d", dc.ContextSize)
	}
}

func TestDeployConfigFor_SectionOverridesGlobals(t *testing.T) {
	preset := "[*]\nngl = 10\n\n[m]\nhf = org/model:Q4\nctx-size = 4096\nngl = 99\n"
	dc := deployConfigFrom(t, "PROVIDER llamacpp\nALIAS m\nPRESET ./preset.ini\n", preset)
	args := strings.Join(dc.ServeArgs, " ")
	if !strings.Contains(args, "--n-gpu-layers 99") || strings.Contains(args, "--n-gpu-layers 10") {
		t.Errorf("the section should override the [*] global, got %q", args)
	}
}

func TestDeployConfigFor_VllmNeedsNoPreset(t *testing.T) {
	dc := deployConfigFrom(t, "PROVIDER vllm\nMODEL Qwen/Qwen3.6-27B-FP8\nCONTEXT 32k\n", "")
	if dc.Runner != "vllm" {
		t.Errorf("runner = %q, want vllm", dc.Runner)
	}
	if dc.Quant != "" {
		t.Errorf("quant = %q, want empty for a safetensors repo", dc.Quant)
	}
	if dc.ServedModelName != "Qwen/Qwen3.6-27B-FP8" {
		t.Errorf("servedModelName = %q, want the model id when there is no ALIAS", dc.ServedModelName)
	}
	if dc.ServeArgs == nil {
		t.Error("serveArgs should marshal as [] rather than null")
	}
}

func TestDeployConfigFor_Rejects(t *testing.T) {
	cases := []struct {
		name, outfitBody, presetBody, want string
	}{
		{
			name:       "a provider that is not a self-hosted engine",
			outfitBody: "PROVIDER openai-compatible\nMODEL org/m\nCONTEXT 32k\n",
			want:       "cannot be deployed",
		},
		{
			name:       "a local gguf path",
			outfitBody: "PROVIDER llamacpp\nMODEL ./models/qwen.gguf\nCONTEXT 32k\n",
			want:       "cannot deploy the local model file",
		},
		{
			name:       "no model anywhere",
			outfitBody: "PROVIDER llamacpp\nCONTEXT 32k\n",
			want:       "nothing to deploy",
		},
		{
			name:       "no context anywhere",
			outfitBody: "PROVIDER llamacpp\nMODEL org/m:Q4\n",
			want:       "no context size",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeDeployOutfit(t, tc.outfitBody, tc.presetBody)
			sel, _, err := readOutfit("test", path)
			if err != nil {
				t.Fatal(err)
			}
			_, err = deployConfigFor(sel, path)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("want an error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestSplitModelQuant(t *testing.T) {
	for _, tc := range []struct{ in, repo, quant string }{
		{"org/model:Q4_K_M", "org/model", "Q4_K_M"},
		{"org/model", "org/model", ""},
		{"unsloth/Qwen3.6-27B-MTP-GGUF:UD-Q6_K_XL", "unsloth/Qwen3.6-27B-MTP-GGUF", "UD-Q6_K_XL"},
	} {
		repo, quant := splitModelQuant(tc.in)
		if repo != tc.repo || quant != tc.quant {
			t.Errorf("splitModelQuant(%q) = %q,%q; want %q,%q", tc.in, repo, quant, tc.repo, tc.quant)
		}
	}
}

// stubDeploySeams wires the deploy flow's seams: discovery returns a layer
// whose control URLs are serverURL, the environment reports the given state,
// and the public-IP probe is fixed. Restores on cleanup.
func stubDeploySeams(t *testing.T, serverURL, statusState string) {
	t.Helper()
	origDiscover, origStatus, origDetect := deployDiscoverFn, remoteStatusFn, detectPublicCIDRFn
	t.Cleanup(func() {
		deployDiscoverFn, remoteStatusFn, detectPublicCIDRFn = origDiscover, origStatus, origDetect
	})
	deployDiscoverFn = func(context.Context, aws.Config, string) (remote.SharedLayer, error) {
		return remote.SharedLayer{Config: remote.Config{
			StartURL: serverURL, StopURL: serverURL, DeployURL: serverURL, Region: "us-east-1",
		}}, nil
	}
	remoteStatusFn = func(context.Context, remote.Config) (*remote.Response, error) {
		return &remote.Response{StatusCode: 200, State: statusState}, nil
	}
	detectPublicCIDRFn = func(context.Context) (string, error) { return "203.0.113.7/32", nil }
}

// writeDeployEnvOutfit writes an Outfit (REMOTE names the environment) + preset.
func writeDeployEnvOutfit(t *testing.T, env string) {
	t.Helper()
	t.Chdir(t.TempDir())
	outfitBody := "PROVIDER llamacpp\nALIAS qwen3.6-27b\nCONTEXT 131072\nPRESET ./preset.ini\nREMOTE " + env + "\n"
	if err := os.WriteFile("Outfit", []byte(outfitBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("preset.ini", []byte(qwenPreset), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteDeploy_PostsTheConfigAndRegisters(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)

	var got struct {
		remote.DeployConfig
		AllowedCidr string `json:"allowedCidr"`
	}
	var gotMethod, gotAuth, gotEnv string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		gotEnv = r.URL.Query().Get("env")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"deployed":true,"environment":"testenv","base_url":"http://198.51.100.9:8000/v1","seeding":true,"seedInstanceId":"i-123"}`))
	}))
	defer server.Close()
	stubDeploySeams(t, server.URL, "undeployed")
	writeDeployEnvOutfit(t, "testenv")

	out := captureStdout(t, func() {
		if err := cmdRemoteDeploy(nil); err != nil {
			t.Errorf("cmdRemoteDeploy: %v", err)
		}
	})

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	// A body-carrying request must still be signed, with the payload hash.
	if !strings.Contains(gotAuth, "AWS4-HMAC-SHA256") {
		t.Errorf("request was not SigV4-signed: %q", gotAuth)
	}
	// The environment travels on the call; the Lambdas require it.
	if gotEnv != "testenv" {
		t.Errorf("env query = %q, want testenv", gotEnv)
	}
	if got.Runner != "llamacpp" || got.ModelID != "unsloth/Qwen3.6-27B-MTP-GGUF" || got.ContextSize != 131072 {
		t.Errorf("posted config is wrong: %+v", got.DeployConfig)
	}
	// A fresh environment gets the detected CIDR.
	if got.AllowedCidr != "203.0.113.7/32" {
		t.Errorf("allowedCidr = %q, want the detected /32", got.AllowedCidr)
	}
	if !strings.Contains(out, "seeding the weights on i-123") {
		t.Errorf("seeding should be reported with the instance id, got:\n%s", out)
	}

	// The environment is registered, owner-only, carrying the base URL and id.
	path := remote.EnvConfigPath("testenv")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("environment not registered: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("registered remote.json mode = %v, want 0600", fi.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	var saved remote.Config
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Environment != "testenv" || saved.BaseURL != "http://198.51.100.9:8000/v1" || saved.DeployURL != server.URL {
		t.Errorf("registered config wrong: %+v", saved)
	}
}

func TestRemoteDeploy_NotBootstrapped(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	stubDeploySeams(t, "https://unused", "undeployed")
	deployDiscoverFn = func(context.Context, aws.Config, string) (remote.SharedLayer, error) {
		return remote.SharedLayer{}, fmt.Errorf("the shared infrastructure (stack %q) is not deployed in this account and region — run `outfit remote bootstrap` first", "cloud-vm-llm")
	}
	writeDeployEnvOutfit(t, "testenv")

	err := cmdRemoteDeploy(nil)
	if err == nil || !strings.Contains(err.Error(), "outfit remote bootstrap") {
		t.Errorf("want a bootstrap-first error, got %v", err)
	}
	if _, statErr := os.Stat(remote.EnvConfigPath("testenv")); !os.IsNotExist(statErr) {
		t.Error("nothing should be registered when the account is not bootstrapped")
	}
}

func TestRemoteDeploy_RequiresEnvName(t *testing.T) {
	isolateConfig(t)
	t.Chdir(t.TempDir())
	// A path-form REMOTE is not an environment name.
	if err := os.WriteFile("Outfit", []byte(
		"PROVIDER vllm\nMODEL Qwen/Qwen3.6-27B-FP8\nCONTEXT 32k\nREMOTE ./remote.json\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	err := cmdRemoteDeploy(nil)
	if err == nil || !strings.Contains(err.Error(), "REMOTE <name>") {
		t.Errorf("want an error asking for REMOTE <name>, got %v", err)
	}
}

func TestRemoteDeploy_OverwriteGuard(t *testing.T) {
	deployBody := `{"deployed":true,"environment":"testenv","base_url":"http://198.51.100.9:8000/v1"}`

	t.Run("registered environment needs --overwrite", func(t *testing.T) {
		isolateConfig(t)
		stubAWSEnv(t)
		stubDeploySeams(t, "https://unused", "undeployed")
		if err := remote.SaveEnvironment("testenv", remote.Config{StartURL: "https://s", StopURL: "https://x", Region: "us-east-1"}); err != nil {
			t.Fatal(err)
		}
		writeDeployEnvOutfit(t, "testenv")
		err := cmdRemoteDeploy(nil)
		if err == nil || !strings.Contains(err.Error(), "--overwrite") {
			t.Errorf("want an overwrite refusal, got %v", err)
		}
	})

	t.Run("live environment needs --overwrite", func(t *testing.T) {
		isolateConfig(t)
		stubAWSEnv(t)
		stubDeploySeams(t, "https://unused", "running")
		writeDeployEnvOutfit(t, "testenv")
		err := cmdRemoteDeploy(nil)
		if err == nil || !strings.Contains(err.Error(), "--overwrite") {
			t.Errorf("want an overwrite refusal for a live instance, got %v", err)
		}
	})

	t.Run("--overwrite proceeds", func(t *testing.T) {
		isolateConfig(t)
		stubAWSEnv(t)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(deployBody))
		}))
		defer server.Close()
		stubDeploySeams(t, server.URL, "running")
		writeDeployEnvOutfit(t, "testenv")
		out := captureStdout(t, func() {
			if err := cmdRemoteDeploy([]string{"--overwrite"}); err != nil {
				t.Errorf("cmdRemoteDeploy --overwrite: %v", err)
			}
		})
		if !strings.Contains(out, "deployed: environment testenv") {
			t.Errorf("expected a deploy report, got:\n%s", out)
		}
	})
}

func TestRemoteDeploy_RejectsBadCIDR(t *testing.T) {
	isolateConfig(t)
	stubDeploySeams(t, "https://unused", "undeployed")
	writeDeployEnvOutfit(t, "testenv")
	err := cmdRemoteDeploy([]string{"--allowed-cidr", "bogus"})
	if err == nil || !strings.Contains(err.Error(), "IPv4 CIDR") {
		t.Errorf("want a CIDR validation error, got %v", err)
	}
}

func TestRemoteDeploy_DryRunSendsNothing(t *testing.T) {
	isolateConfig(t)
	called := false
	origDiscover := deployDiscoverFn
	t.Cleanup(func() { deployDiscoverFn = origDiscover })
	deployDiscoverFn = func(context.Context, aws.Config, string) (remote.SharedLayer, error) {
		called = true
		return remote.SharedLayer{}, fmt.Errorf("must not be called")
	}
	writeDeployEnvOutfit(t, "testenv")

	out := captureStdout(t, func() {
		if err := cmdRemoteDeploy([]string{"--dry-run"}); err != nil {
			t.Errorf("cmdRemoteDeploy --dry-run: %v", err)
		}
	})
	if called {
		t.Error("--dry-run must touch nothing — not even discovery")
	}
	if !strings.Contains(out, "unsloth/Qwen3.6-27B-MTP-GGUF") || !strings.Contains(out, "environment: testenv") {
		t.Errorf("--dry-run should print the config and environment, got:\n%s", out)
	}
}

// Guard the assumption deployConfigFor relies on: PROVIDER names the engine.
func TestRunnerFor(t *testing.T) {
	for _, provider := range []string{"llamacpp", "vllm"} {
		if got, err := runnerFor(provider); err != nil || got != provider {
			t.Errorf("runnerFor(%q) = %q, %v", provider, got, err)
		}
	}
	for _, provider := range []string{"openai-compatible", "ollama", "openrouter", ""} {
		if _, err := runnerFor(provider); err == nil {
			t.Errorf("runnerFor(%q) should error", provider)
		}
	}
}

// A cold start blocks in one request for minutes, so the command must say what
// it is doing rather than sit silent — and must say it on stderr, so piping the
// exports still works.
func TestRemoteStart_ReportsProgressWhileWaiting(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(120 * time.Millisecond) // stand in for a cold start
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"ready","base_url":"http://198.51.100.1:8000/v1","api_key":"sk-test"}`))
	}))
	defer server.Close()
	writeRemoteConfig(t, server.URL)

	stderr := captureStderr(t, func() {
		if err := cmdRemoteStart(nil); err != nil {
			t.Errorf("cmdRemoteStart: %v", err)
		}
	})

	if !strings.Contains(stderr, "Starting the endpoint") {
		t.Errorf("no opening explanation on stderr, got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "ready after") {
		t.Errorf("no completion line on stderr, got:\n%s", stderr)
	}
}

func TestStartProgress_HeartbeatsAndStops(t *testing.T) {
	stderr := captureStderr(t, func() {
		p := newStartProgress(10 * time.Millisecond)
		time.Sleep(60 * time.Millisecond)
		p.close()
		time.Sleep(40 * time.Millisecond)
	})
	if !strings.Contains(stderr, "still starting") {
		t.Errorf("expected a heartbeat while waiting, got:\n%s", stderr)
	}
	// Closing must stop it: 40ms of 10ms ticks after close would add ~4 more.
	if got := strings.Count(stderr, "still starting"); got > 8 {
		t.Errorf("heartbeat kept running after close (%d lines):\n%s", got, stderr)
	}
}

// The heartbeat must describe what is really happening: while the endpoint
// reports no capacity, nothing is booting, so it must not claim the instance is
// still starting.
func TestStartProgress_HeartbeatReflectsState(t *testing.T) {
	t.Run("no-capacity says waiting for capacity", func(t *testing.T) {
		stderr := captureStderr(t, func() {
			p := newStartProgress(10 * time.Millisecond)
			p.setState("no-capacity")
			time.Sleep(45 * time.Millisecond)
			p.close()
		})
		if !strings.Contains(stderr, "waiting for capacity") {
			t.Errorf("expected a capacity-wait heartbeat, got:\n%s", stderr)
		}
		if strings.Contains(stderr, "still starting") {
			t.Errorf("must not claim it is starting while out of capacity, got:\n%s", stderr)
		}
	})

	t.Run("a booting state says still starting", func(t *testing.T) {
		stderr := captureStderr(t, func() {
			p := newStartProgress(10 * time.Millisecond)
			p.setState("starting")
			time.Sleep(45 * time.Millisecond)
			p.close()
		})
		if !strings.Contains(stderr, "still starting") {
			t.Errorf("expected a starting heartbeat while booting, got:\n%s", stderr)
		}
		if strings.Contains(stderr, "waiting for capacity") {
			t.Errorf("a booting instance is not a capacity wait, got:\n%s", stderr)
		}
	})

	// The line tracks the latest poll: once capacity is found and the instance
	// starts booting, the heartbeat must stop reporting a capacity wait.
	t.Run("the latest state wins after a transition", func(t *testing.T) {
		p := newStartProgress(time.Hour) // no ticks; drive the line directly
		p.setState("no-capacity")
		if got := p.heartbeat(); !strings.Contains(got, "waiting for capacity") {
			t.Errorf("after no-capacity, heartbeat = %q, want a capacity wait", got)
		}
		p.setState("starting")
		if got := p.heartbeat(); !strings.Contains(got, "still starting") {
			t.Errorf("after booting, heartbeat = %q, want a starting line", got)
		}
		p.close()
	})
}

// -t is a shorthand for --timeout: a very short one makes a never-ready start
// give up promptly rather than block for the 15m default.
func TestRemoteStart_TimeoutShorthand(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	// Never returns ready, so only the timeout ends the wait.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"state":"starting","retry_after_seconds":0}`))
	}))
	defer server.Close()
	writeRemoteConfig(t, server.URL)

	// Silence the progress line this writes to stderr; the wait is what matters.
	oldStderr := os.Stderr
	_, w, _ := os.Pipe()
	os.Stderr = w
	defer func() { os.Stderr = oldStderr; w.Close() }()

	done := make(chan error, 1)
	go func() { done <- cmdRemoteStart([]string{"-t", "80ms"}) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected a timeout error, got nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("-t 80ms did not bound the wait; command is still blocking")
	}
}

// Progress must not land on stdout, or `outfit remote start | grep '^export '`
// would pick it up.
func TestRemoteStart_StdoutCarriesOnlyTheResult(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"ready","base_url":"http://198.51.100.1:8000/v1","api_key":"sk-test"}`))
	}))
	defer server.Close()
	writeRemoteConfig(t, server.URL)

	out := captureStdout(t, func() {
		if err := cmdRemoteStart(nil); err != nil {
			t.Errorf("cmdRemoteStart: %v", err)
		}
	})
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("stdout should be exactly the two exports, got:\n%s", out)
	}
	for i, want := range []string{"export OPENAI_BASE_URL=", "export OPENAI_API_KEY="} {
		if !strings.HasPrefix(lines[i], want) {
			t.Errorf("stdout line %d = %q, want it to start with %q", i, lines[i], want)
		}
	}
}
