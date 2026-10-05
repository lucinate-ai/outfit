package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/spinloop-ai/spinloop/internal/cloud"
	"github.com/spinloop-ai/spinloop/internal/inference"
)

// writeDeploySpinloop writes a Spinloop (and optionally a preset) into a temp dir
// and returns the Spinloop's path.
func writeDeploySpinloop(t *testing.T, spinloopBody, presetBody string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "Spinloop")
	if err := os.WriteFile(path, []byte(spinloopBody), 0o600); err != nil {
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

func deployConfigFrom(t *testing.T, spinloopBody, presetBody string) inference.DeployConfig {
	t.Helper()
	path := writeDeploySpinloop(t, spinloopBody, presetBody)
	sel, _, err := readSpinloop("test", path)
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
	// No CONTEXT and no MODEL in the Spinloop: both come from the preset, so one
	// preset can drive a local serve and a deploy.
	dc := deployConfigFrom(t, "PROVIDER llamacpp\nALIAS qwen3.6-27b\nPRESET ./preset.ini\n", qwenPreset)
	if dc.ContextSize != 131072 {
		t.Errorf("contextSize = %d, want the preset's ctx-size", dc.ContextSize)
	}
	if dc.ModelID != "unsloth/Qwen3.6-27B-MTP-GGUF" {
		t.Errorf("modelId = %q, want the preset's hf", dc.ModelID)
	}
}

func TestDeployConfigFor_SpinloopOverridesPreset(t *testing.T) {
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

// TestDeployConfigFor_Parallel checks PARALLEL is captured as its own field —
// not scaled here, since building the command from it is the daemon's job at
// start time (argvFromDeployConfig), matching how ContextSize is a stored,
// unscaled value too.
func TestDeployConfigFor_Parallel(t *testing.T) {
	dc := deployConfigFrom(t,
		"PROVIDER llamacpp\nALIAS qwen3.6-27b\nCONTEXT 131072\nPARALLEL 2\nPRESET ./preset.ini\n", qwenPreset)
	if dc.Parallel != 2 {
		t.Errorf("parallel = %d, want 2", dc.Parallel)
	}
	if dc.ContextSize != 131072 {
		t.Errorf("contextSize = %d, want the unscaled 131072 (scaling happens at start time)", dc.ContextSize)
	}
}

// TestDeployConfigFor_ParallelFallsBackToPreset checks a preset's own np is
// captured into dc.Parallel — it is dropped from serveArgs below (cloud-owned),
// so losing it there without this fallback would silently discard it.
func TestDeployConfigFor_ParallelFallsBackToPreset(t *testing.T) {
	preset := "[*]\nnp = 4\n\n[m]\nhf = org/model:Q4\nctx-size = 4096\n"
	dc := deployConfigFrom(t, "PROVIDER llamacpp\nALIAS m\nPRESET ./preset.ini\n", preset)
	if dc.Parallel != 4 {
		t.Errorf("parallel = %d, want 4 (from the preset's np)", dc.Parallel)
	}
}

// TestDeployConfigFor_ParallelDroppedFromServeArgs checks a preset's own
// np/parallel value does not survive into serveArgs once it has been
// captured into dc.Parallel — otherwise it would double-define the flag
// alongside the daemon's own computed one at start time.
func TestDeployConfigFor_ParallelDroppedFromServeArgs(t *testing.T) {
	preset := "[*]\nnp = 4\n\n[m]\nhf = org/model:Q4\nctx-size = 4096\n"
	dc := deployConfigFrom(t, "PROVIDER llamacpp\nALIAS m\nPARALLEL 2\nPRESET ./preset.ini\n", preset)
	if dc.Parallel != 2 {
		t.Errorf("parallel = %d, want the Spinloop's own PARALLEL (2), not the preset's np", dc.Parallel)
	}
	args := strings.Join(dc.ServeArgs, " ")
	if strings.Contains(args, "--parallel") || strings.Contains(args, "np") {
		t.Errorf("serveArgs %q should not carry the cloud-owned parallel flag", args)
	}
}

// TestDeployConfigFor_ParallelPresetKeyIsPerRunner is the guard on the
// regression that dropping every spelling from serveArgs invites: the deploy
// reads a preset's slot count back in the runner's *own* vocabulary. Reading
// only llama.cpp's would leave a vLLM preset's max-num-seqs dropped by
// dropOwned and captured by nothing — silently lost rather than passed
// through, which is what it did before parallelism was modelled at all.
func TestDeployConfigFor_ParallelPresetKeyIsPerRunner(t *testing.T) {
	dc := deployConfigFrom(t,
		"PROVIDER vllm\nALIAS m\nMODEL org/model\nCONTEXT 32k\nPRESET ./preset.ini\n",
		"[m]\nmax-num-seqs = 4\n")
	if dc.Parallel != 4 {
		t.Errorf("parallel = %d, want 4 (from the vLLM preset's max-num-seqs)", dc.Parallel)
	}
	// And it must not also survive as a raw arg, or the engine would be told
	// twice — once by the computed flag, once by the passthrough.
	if args := strings.Join(dc.ServeArgs, " "); strings.Contains(args, "max-num-seqs") {
		t.Errorf("serveArgs %q should not also carry the cloud-owned max-num-seqs", args)
	}
}

// TestDeployConfigFor_ParallelIgnoresAnotherRunnersKey is the negative half of
// the rule above: a key belonging to a different engine's vocabulary is not
// read as this runner's slot count. A llama.cpp preset carrying max-num-seqs
// is a mistake, and inventing a --parallel from it would launder that mistake
// into a real flag.
func TestDeployConfigFor_ParallelIgnoresAnotherRunnersKey(t *testing.T) {
	dc := deployConfigFrom(t,
		"PROVIDER llamacpp\nALIAS m\nPRESET ./preset.ini\n",
		"[m]\nhf = org/model:Q4\nctx-size = 4096\nmax-num-seqs = 4\n")
	if dc.Parallel != 0 {
		t.Errorf("parallel = %d, want 0: max-num-seqs is not llama.cpp's spelling", dc.Parallel)
	}
}

// TestParallelPresetKey pins the per-engine vocabulary itself. Each runner
// spells its slot count differently, and reading the wrong spelling is
// invisible — the value is dropped from the passthrough args either way, so a
// wrong key here loses the setting silently rather than failing.
func TestParallelPresetKey(t *testing.T) {
	for runner, want := range map[string]string{
		"llamacpp": "parallel",
		"vllm":     "max-num-seqs",
		"omlx":     "max-concurrent-requests",
		// A runner with no known spelling must yield "", not a key that would
		// match a param whose canonical form is also empty.
		"":        "",
		"unknown": "",
	} {
		if got := parallelPresetKey(runner); got != want {
			t.Errorf("parallelPresetKey(%q) = %q, want %q", runner, got, want)
		}
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
		name, spinloopBody, presetBody, want string
	}{
		{
			name:         "a provider that is not a self-hosted engine",
			spinloopBody: "PROVIDER openai-compatible\nMODEL org/m\nCONTEXT 32k\n",
			want:         "cannot be deployed",
		},
		{
			name:         "a local gguf path",
			spinloopBody: "PROVIDER llamacpp\nMODEL ./models/qwen.gguf\nCONTEXT 32k\n",
			want:         "cannot deploy the local model file",
		},
		{
			name:         "no model anywhere",
			spinloopBody: "PROVIDER llamacpp\nCONTEXT 32k\n",
			want:         "nothing to deploy",
		},
		{
			name:         "no context anywhere",
			spinloopBody: "PROVIDER llamacpp\nMODEL org/m:Q4\n",
			want:         "no context size",
		},
		{
			name:         "invalid parallel",
			spinloopBody: "PROVIDER llamacpp\nMODEL org/m:Q4\nCONTEXT 32k\nPARALLEL 0\n",
			want:         "invalid PARALLEL",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeDeploySpinloop(t, tc.spinloopBody, tc.presetBody)
			sel, _, err := readSpinloop("test", path)
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
	origDiscover, origStatus, origDetect := deployDiscoverFn, cloudStatusFn, detectPublicCIDRFn
	t.Cleanup(func() {
		deployDiscoverFn, cloudStatusFn, detectPublicCIDRFn = origDiscover, origStatus, origDetect
	})
	deployDiscoverFn = func(context.Context, aws.Config, string) (cloud.ControlPlane, error) {
		return cloud.ControlPlane{Config: cloud.Config{
			StartURL: serverURL, StopURL: serverURL, DeployURL: serverURL, Region: "us-east-1",
		}}, nil
	}
	cloudStatusFn = func(context.Context, cloud.Config) (*cloud.Response, error) {
		return &cloud.Response{StatusCode: 200, State: statusState}, nil
	}
	detectPublicCIDRFn = func(context.Context) (string, error) { return "203.0.113.7/32", nil }
}

// writeDeploySpinloopCwd writes the Spinloop a deploy reads for the engine,
// model and context + its preset, in a new working directory. The environment
// name travels as --env, not in the file.
func writeDeploySpinloopCwd(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	spinloopBody := "PROVIDER llamacpp\nALIAS qwen3.6-27b\nCONTEXT 131072\nPRESET ./preset.ini\n"
	if err := os.WriteFile("Spinloop", []byte(spinloopBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("preset.ini", []byte(qwenPreset), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCloudDeploy_PostsTheConfigAndRegisters(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)

	var got struct {
		inference.DeployConfig
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
		w.Write([]byte(`{"deployed":true,"environment":"testenv","base_url":"http://198.51.100.9:8000/v1","seeding":true,"seedId":"llamacpp--unsloth-Qwen3.6-27B-MTP-GGUF--UD-Q6_K_XL"}`))
	}))
	defer server.Close()
	stubDeploySeams(t, server.URL, "undeployed")
	writeDeploySpinloopCwd(t)

	out := captureStdout(t, func() {
		if err := cmdCloudDeploy([]string{"--env", "testenv"}); err != nil {
			t.Errorf("cmdCloudDeploy: %v", err)
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
	// The seed is named by its stable id, and the output states the command
	// that follows it rather than quoting an estimate to wait out.
	if !strings.Contains(out, "spinloop cloud seed status llamacpp--unsloth-Qwen3.6-27B-MTP-GGUF--UD-Q6_K_XL") {
		t.Errorf("seeding should name the follow-up command, got:\n%s", out)
	}

	// The environment is registered, owner-only, carrying the base URL and id.
	path := must1(cloud.EnvConfigPath("testenv"))
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("environment not registered: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("registered cloud.json mode = %v, want 0600", fi.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	var saved cloud.Config
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Environment != "testenv" || saved.BaseURL != "http://198.51.100.9:8000/v1" || saved.DeployURL != server.URL {
		t.Errorf("registered config wrong: %+v", saved)
	}
}

func TestCloudDeploy_NotBootstrapped(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	stubDeploySeams(t, "https://unused", "undeployed")
	deployDiscoverFn = func(context.Context, aws.Config, string) (cloud.ControlPlane, error) {
		return cloud.ControlPlane{}, fmt.Errorf("the control plane (stack %q) is not deployed in this account and region — run `spinloop cloud bootstrap` first", "cloud-vm-llm")
	}
	writeDeploySpinloopCwd(t)

	err := cmdCloudDeploy([]string{"--env", "testenv"})
	if err == nil || !strings.Contains(err.Error(), "spinloop cloud bootstrap") {
		t.Errorf("want a bootstrap-first error, got %v", err)
	}
	if _, statErr := os.Stat(must1(cloud.EnvConfigPath("testenv"))); !os.IsNotExist(statErr) {
		t.Error("nothing should be registered when the account is not bootstrapped")
	}
}

func TestCloudDeploy_RequiresEnvName(t *testing.T) {
	isolateConfig(t)
	t.Chdir(t.TempDir())
	if err := os.WriteFile("Spinloop", []byte(
		"PROVIDER vllm\nMODEL Qwen/Qwen3.6-27B-FP8\nCONTEXT 32k\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	// No --env: deploy cannot tell which environment to create.
	err := cmdCloudDeploy(nil)
	if err == nil || !strings.Contains(err.Error(), "deploy must name the environment it creates") {
		t.Errorf("want an error asking for --env, got %v", err)
	}
	// A path-shaped value is not an environment name.
	err = cmdCloudDeploy([]string{"--env", "./cloud.json"})
	if err == nil || !strings.Contains(err.Error(), "is not an environment name") {
		t.Errorf("want an environment-name error, got %v", err)
	}
}

func TestCloudDeploy_OverwriteGuard(t *testing.T) {
	deployBody := `{"deployed":true,"environment":"testenv","base_url":"http://198.51.100.9:8000/v1"}`

	t.Run("registered environment needs --overwrite", func(t *testing.T) {
		isolateConfig(t)
		stubAWSEnv(t)
		stubDeploySeams(t, "https://unused", "undeployed")
		if err := cloud.SaveEnvironment("testenv", cloud.Config{StartURL: "https://s", StopURL: "https://x", Region: "us-east-1"}); err != nil {
			t.Fatal(err)
		}
		writeDeploySpinloopCwd(t)
		err := cmdCloudDeploy([]string{"--env", "testenv"})
		if err == nil || !strings.Contains(err.Error(), "--overwrite") {
			t.Errorf("want an overwrite refusal, got %v", err)
		}
	})

	t.Run("live environment needs --overwrite", func(t *testing.T) {
		isolateConfig(t)
		stubAWSEnv(t)
		stubDeploySeams(t, "https://unused", "running")
		writeDeploySpinloopCwd(t)
		err := cmdCloudDeploy([]string{"--env", "testenv"})
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
		writeDeploySpinloopCwd(t)
		out := captureStdout(t, func() {
			if err := cmdCloudDeploy([]string{"--env", "testenv", "--overwrite"}); err != nil {
				t.Errorf("cmdCloudDeploy --overwrite: %v", err)
			}
		})
		if !strings.Contains(out, "deployed: environment testenv") {
			t.Errorf("expected a deploy report, got:\n%s", out)
		}
	})
}

func TestCloudDeploy_RejectsBadCIDR(t *testing.T) {
	isolateConfig(t)
	stubDeploySeams(t, "https://unused", "undeployed")
	writeDeploySpinloopCwd(t)
	err := cmdCloudDeploy([]string{"--env", "testenv", "--allowed-cidr", "bogus"})
	if err == nil || !strings.Contains(err.Error(), "IPv4 CIDR") {
		t.Errorf("want a CIDR validation error, got %v", err)
	}
}

func TestCloudDeploy_DryRunSendsNothing(t *testing.T) {
	isolateConfig(t)
	called := false
	origDiscover := deployDiscoverFn
	t.Cleanup(func() { deployDiscoverFn = origDiscover })
	deployDiscoverFn = func(context.Context, aws.Config, string) (cloud.ControlPlane, error) {
		called = true
		return cloud.ControlPlane{}, fmt.Errorf("must not be called")
	}
	writeDeploySpinloopCwd(t)

	out := captureStdout(t, func() {
		if err := cmdCloudDeploy([]string{"--env", "testenv", "--dry-run"}); err != nil {
			t.Errorf("cmdCloudDeploy --dry-run: %v", err)
		}
	})
	if called {
		t.Error("--dry-run must touch nothing — not even discovery")
	}
	if !strings.Contains(out, "unsloth/Qwen3.6-27B-MTP-GGUF") || !strings.Contains(out, "environment: testenv") {
		t.Errorf("--dry-run should print the config and environment, got:\n%s", out)
	}
	// No pin means the plan promises latest, so a dry run says what a fresh
	// boot would install rather than leaving it unstated.
	if !strings.Contains(out, "spinloop:  latest") {
		t.Errorf("--dry-run should promise the default spinloop, got:\n%s", out)
	}
}

func TestCloudDeploy_SpinloopVersion(t *testing.T) {
	t.Run("the pin reaches the deploy body and the plan", func(t *testing.T) {
		isolateConfig(t)
		stubAWSEnv(t)

		var got inference.DeployConfig
		var gotRaw []byte
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotRaw, _ = io.ReadAll(r.Body)
			_ = json.Unmarshal(gotRaw, &got)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"deployed":true,"environment":"testenv","base_url":"http://198.51.100.9:8000/v1"}`))
		}))
		defer server.Close()
		stubDeploySeams(t, server.URL, "undeployed")
		writeDeploySpinloopCwd(t)

		out := captureStdout(t, func() {
			if err := cmdCloudDeploy([]string{"--env", "testenv", "--spinloop-version", "v1.26.1"}); err != nil {
				t.Errorf("cmdCloudDeploy: %v", err)
			}
		})

		// The pin is normalised — the v a tag carries is not part of the
		// version the binary reports — and sent to the control plane.
		if got.SpinloopVersion != "1.26.1" {
			t.Errorf("posted spinloopVersion = %q, want the pin with the v stripped", got.SpinloopVersion)
		}
		if !strings.Contains(string(gotRaw), `"spinloopVersion":"1.26.1"`) {
			t.Errorf("spinloopVersion did not reach the body: %s", gotRaw)
		}
		if !strings.Contains(out, "spinloop:  1.26.1") {
			t.Errorf("plan should name the pin, got:\n%s", out)
		}
	})

	t.Run("an unpinned deploy omits the field entirely", func(t *testing.T) {
		isolateConfig(t)
		stubAWSEnv(t)

		var gotRaw []byte
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotRaw, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"deployed":true,"environment":"testenv","base_url":"http://198.51.100.9:8000/v1"}`))
		}))
		defer server.Close()
		stubDeploySeams(t, server.URL, "undeployed")
		writeDeploySpinloopCwd(t)

		if err := cmdCloudDeploy([]string{"--env", "testenv"}); err != nil {
			t.Errorf("cmdCloudDeploy: %v", err)
		}
		// Absent, not null and not "latest": an unpinned deploy sends exactly
		// what a control plane predating the pin expects.
		if strings.Contains(string(gotRaw), "spinloopVersion") {
			t.Errorf("spinloopVersion should be omitted when unpinned: %s", gotRaw)
		}
	})

	t.Run("latest and a bare v are no pins", func(t *testing.T) {
		isolateConfig(t)
		for _, flag := range []string{"latest", "v"} {
			writeDeploySpinloopCwd(t)
			out := captureStdout(t, func() {
				if err := cmdCloudDeploy([]string{"--env", "testenv", "--spinloop-version", flag, "--dry-run"}); err != nil {
					t.Errorf("cmdCloudDeploy --spinloop-version %s: %v", flag, err)
				}
			})
			if !strings.Contains(out, "spinloop:  latest") {
				t.Errorf("--spinloop-version %s should plan as latest, got:\n%s", flag, out)
			}
		}
	})

	t.Run("a value outside the charset is refused before anything is sent", func(t *testing.T) {
		isolateConfig(t)
		stubDeploySeams(t, "https://unused", "undeployed")
		writeDeploySpinloopCwd(t)
		err := cmdCloudDeploy([]string{"--env", "testenv", "--spinloop-version", "1.2.6 beta"})
		if err == nil || !strings.Contains(err.Error(), "--spinloop-version") {
			t.Errorf("want a --spinloop-version validation error, got %v", err)
		}
	})
}

// An instance type is recorded on the environment: it reaches the signed body
// so the deploy Lambda persists it, and the plan names the machine the
// environment will launch as — a promise when named, the default otherwise.
func TestCloudDeploy_InstanceType(t *testing.T) {
	t.Run("the type reaches the deploy body and the plan", func(t *testing.T) {
		isolateConfig(t)
		stubAWSEnv(t)

		var got inference.DeployConfig
		var gotRaw []byte
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotRaw, _ = io.ReadAll(r.Body)
			_ = json.Unmarshal(gotRaw, &got)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"deployed":true,"environment":"testenv","base_url":"http://198.51.100.9:8000/v1"}`))
		}))
		defer server.Close()
		stubDeploySeams(t, server.URL, "undeployed")
		writeDeploySpinloopCwd(t)

		out := captureStdout(t, func() {
			if err := cmdCloudDeploy([]string{"--env", "testenv", "--instance-type", "g6e.2xlarge"}); err != nil {
				t.Errorf("cmdCloudDeploy: %v", err)
			}
		})

		if got.InstanceType != "g6e.2xlarge" {
			t.Errorf("posted instanceType = %q, want g6e.2xlarge", got.InstanceType)
		}
		if !strings.Contains(string(gotRaw), `"instanceType":"g6e.2xlarge"`) {
			t.Errorf("instanceType did not reach the body: %s", gotRaw)
		}
		if !strings.Contains(out, "instance:  g6e.2xlarge") {
			t.Errorf("plan should name the type, got:\n%s", out)
		}
	})

	t.Run("an untyped deploy omits the field entirely", func(t *testing.T) {
		isolateConfig(t)
		stubAWSEnv(t)

		var gotRaw []byte
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotRaw, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"deployed":true,"environment":"testenv","base_url":"http://198.51.100.9:8000/v1"}`))
		}))
		defer server.Close()
		stubDeploySeams(t, server.URL, "undeployed")
		writeDeploySpinloopCwd(t)

		if err := cmdCloudDeploy([]string{"--env", "testenv"}); err != nil {
			t.Errorf("cmdCloudDeploy: %v", err)
		}
		// Absent, not null and not empty: an untyped deploy sends exactly what
		// a control plane predating the field expects.
		if strings.Contains(string(gotRaw), "instanceType") {
			t.Errorf("instanceType should be omitted when untyped: %s", gotRaw)
		}
	})

	t.Run("a dry run names the default machine when untyped", func(t *testing.T) {
		isolateConfig(t)
		writeDeploySpinloopCwd(t)
		out := captureStdout(t, func() {
			if err := cmdCloudDeploy([]string{"--env", "testenv", "--dry-run"}); err != nil {
				t.Errorf("cmdCloudDeploy --dry-run: %v", err)
			}
		})
		if !strings.Contains(out, "instance:  the control plane's default") {
			t.Errorf("--dry-run should state the default machine, got:\n%s", out)
		}
	})

	t.Run("a value outside the shape is refused before anything is sent", func(t *testing.T) {
		isolateConfig(t)
		stubDeploySeams(t, "https://unused", "undeployed")
		writeDeploySpinloopCwd(t)
		err := cmdCloudDeploy([]string{"--env", "testenv", "--instance-type", "g6exlarge"})
		if err == nil || !strings.Contains(err.Error(), "--instance-type") {
			t.Errorf("want a --instance-type validation error, got %v", err)
		}
	})
}

// A supplied key is resolved from the environment the Spinloop's local
// environment populated, reaches the signed body as a request-scoped field,
// and the report says what happened to it — the action, never the value.
func TestCloudDeploy_APIKeyEnvReachesTheRequest(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)

	var gotAPIKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var got struct {
			APIKey string `json:"apiKey"`
		}
		_ = json.Unmarshal(body, &got)
		gotAPIKey = got.APIKey
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"deployed":true,"environment":"testenv","base_url":"http://198.51.100.9:8000/v1","apiKeyAction":"rotated"}`))
	}))
	defer server.Close()
	stubDeploySeams(t, server.URL, "undeployed")
	writeDeploySpinloopCwd(t)
	t.Setenv("SHARED_KEY", "sk-supplied")

	out := captureStdout(t, func() {
		if err := cmdCloudDeploy([]string{"--env", "testenv", "--api-key-env", "SHARED_KEY"}); err != nil {
			t.Fatalf("cmdCloudDeploy: %v", err)
		}
	})

	if gotAPIKey != "sk-supplied" {
		t.Errorf("the request carried apiKey %q, want the resolved value", gotAPIKey)
	}
	// The report names the action and never the value.
	if !strings.Contains(out, "api key: rotated") {
		t.Errorf("the report should say the key was rotated, got:\n%s", out)
	}
	if strings.Contains(out, "sk-supplied") {
		t.Errorf("the report printed the key value:\n%s", out)
	}
}

// A named variable that is set nowhere fails before anything is sent.
func TestCloudDeploy_APIKeyEnvUnsetFailsBeforeSending(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)

	sent := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = true
		w.Write([]byte(`{"deployed":true}`))
	}))
	defer server.Close()
	stubDeploySeams(t, server.URL, "undeployed")
	writeDeploySpinloopCwd(t)
	os.Unsetenv("NOWHERE_DEPLOY_KEY")

	err := cmdCloudDeploy([]string{"--env", "testenv", "--api-key-env", "NOWHERE_DEPLOY_KEY"})
	if err == nil || !strings.Contains(err.Error(), "NOWHERE_DEPLOY_KEY") {
		t.Errorf("want an error naming the variable, got %v", err)
	}
	if sent {
		t.Error("the deploy was sent despite the key failing to resolve")
	}
}

// A dry run with a key names the variable it will store from — never the
// value — and still touches nothing.
func TestCloudDeploy_APIKeyEnvDryRunNamesTheVariable(t *testing.T) {
	isolateConfig(t)
	called := false
	origDiscover := deployDiscoverFn
	t.Cleanup(func() { deployDiscoverFn = origDiscover })
	deployDiscoverFn = func(context.Context, aws.Config, string) (cloud.ControlPlane, error) {
		called = true
		return cloud.ControlPlane{}, fmt.Errorf("must not be called")
	}
	writeDeploySpinloopCwd(t)
	t.Setenv("SHARED_KEY", "sk-supplied")

	out := captureStdout(t, func() {
		if err := cmdCloudDeploy([]string{"--env", "testenv", "--dry-run", "--api-key-env", "SHARED_KEY"}); err != nil {
			t.Fatalf("cmdCloudDeploy --dry-run: %v", err)
		}
	})
	if called {
		t.Error("--dry-run must touch nothing — not even discovery")
	}
	if !strings.Contains(out, "api key:  stored from SHARED_KEY") {
		t.Errorf("--dry-run should name the key's variable, got:\n%s", out)
	}
	if strings.Contains(out, "sk-supplied") {
		t.Errorf("--dry-run printed the key value:\n%s", out)
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

// providerForRunner is runnerFor's reverse, for a launch that auto-configures
// from a deployed environment's reported runner rather than a Spinloop's
// PROVIDER. Since runnerFor only ever accepts llamacpp/vllm, no other runner
// value can ever reach a deploy-config — but the reverse mapping still has to
// reject one, rather than trust an unrecognised string as a provider name.
func TestProviderForRunner(t *testing.T) {
	for _, runner := range []string{"llamacpp", "vllm"} {
		if got, err := providerForRunner(runner); err != nil || got != runner {
			t.Errorf("providerForRunner(%q) = %q, %v", runner, got, err)
		}
	}
	for _, runner := range []string{"mtplx", "openrouter", ""} {
		if _, err := providerForRunner(runner); err == nil {
			t.Errorf("providerForRunner(%q) should error", runner)
		}
	}
}

// A cold start blocks in one request for minutes, so the command must say what
// it is doing rather than sit silent — and must say it on stderr, so piping the
// exports still works.
func TestCloudStart_ReportsProgressWhileWaiting(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(120 * time.Millisecond) // stand in for a cold start
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"ready","base_url":"http://198.51.100.1:8000/v1","api_key":"sk-test"}`))
	}))
	defer server.Close()
	writeCloudConfig(t, server.URL)

	stderr := captureStderr(t, func() {
		if err := cmdCloudStart([]string{"--env", "default"}); err != nil {
			t.Errorf("cmdCloudStart: %v", err)
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
	// Before any poll has reported, the start is one attempt in flight.
	if !strings.Contains(stderr, "waking the instance") {
		t.Errorf("expected a heartbeat while waiting, got:\n%s", stderr)
	}
	// Closing must stop it: 40ms of 10ms ticks after close would add ~4 more.
	if got := strings.Count(stderr, "waking the instance"); got > 8 {
		t.Errorf("heartbeat kept running after close (%d lines):\n%s", got, stderr)
	}
}

// The heartbeat must describe what is really happening: while the endpoint
// reports no capacity, nothing is booting, so it must not claim the instance is
// coming up. It renders the phase the start is in, so the wording is the tile's
// wording and the numbers in it move as the heartbeat repeats.
func TestStartProgress_HeartbeatReflectsPhase(t *testing.T) {
	// driveStart feeds one start's callbacks the way cloud.Start would.
	driveStart := func(p *startProgress, do func(progress, onState func(string))) {
		progress, onState := p.callbacks()
		do(progress, onState)
	}

	t.Run("no-capacity says waiting for capacity", func(t *testing.T) {
		stderr := captureStderr(t, func() {
			p := newStartProgress(10 * time.Millisecond)
			driveStart(p, func(progress, onState func(string)) {
				onState("no-capacity")
				progress("instance no-capacity; retrying in 120s")
			})
			time.Sleep(45 * time.Millisecond)
			p.close()
		})
		if !strings.Contains(stderr, "waiting for capacity") {
			t.Errorf("expected a capacity-wait heartbeat, got:\n%s", stderr)
		}
		if !strings.Contains(stderr, "retrying in") {
			t.Errorf("the wait does not say when the next attempt is due, got:\n%s", stderr)
		}
		if strings.Contains(stderr, "booting") {
			t.Errorf("must not claim it is booting while out of capacity, got:\n%s", stderr)
		}
	})

	t.Run("a booting state says booting", func(t *testing.T) {
		stderr := captureStderr(t, func() {
			p := newStartProgress(10 * time.Millisecond)
			driveStart(p, func(progress, onState func(string)) { onState("starting") })
			time.Sleep(45 * time.Millisecond)
			p.close()
		})
		if !strings.Contains(stderr, "booting") {
			t.Errorf("expected a booting heartbeat, got:\n%s", stderr)
		}
		if strings.Contains(stderr, "waiting for capacity") {
			t.Errorf("a booting instance is not a capacity wait, got:\n%s", stderr)
		}
	})

	// The heartbeat tracks the latest phase: once capacity is found and the
	// instance starts booting, it must stop reporting a capacity wait.
	t.Run("the latest phase wins after a transition", func(t *testing.T) {
		p := newStartProgress(time.Hour) // no ticks; drive the line directly
		progress, onState := p.callbacks()
		onState("no-capacity")
		progress("instance no-capacity; retrying in 120s")
		if got := p.heartbeat(); !strings.Contains(got, "waiting for capacity") {
			t.Errorf("after no-capacity, heartbeat = %q, want a capacity wait", got)
		}
		onState(cloud.StateInFlight)
		onState("starting")
		if got := p.heartbeat(); !strings.Contains(got, "booting") {
			t.Errorf("after booting, heartbeat = %q, want a booting line", got)
		}
		p.close()
	})

	// The attempt that finds capacity reports no state until it is ready — its
	// in-flight report is what retires the capacity wait.
	t.Run("in-flight after a capacity wait retires it", func(t *testing.T) {
		p := newStartProgress(time.Hour) // no ticks; drive the line directly
		progress, onState := p.callbacks()
		onState("no-capacity")
		progress("instance no-capacity; retrying in 120s")
		if got := p.heartbeat(); !strings.Contains(got, "waiting for capacity") {
			t.Errorf("after no-capacity, heartbeat = %q, want a capacity wait", got)
		}
		onState(cloud.StateInFlight)
		got := p.heartbeat()
		if !strings.Contains(got, "waking the instance") {
			t.Errorf("in-flight after a capacity wait, heartbeat = %q, want the attempt", got)
		}
		if strings.Contains(got, "waiting for capacity") {
			t.Errorf("an in-flight attempt supersedes the capacity wait, got:\n%q", got)
		}
		p.close()
	})
}

// The end-to-end shape of the capacity-wait bug: the first attempt is refused
// for lack of capacity, the retry finds capacity and holds its request while
// the instance boots. The output must say it is waiting for capacity during the
// wait and stop saying so once the booting attempt is in flight — the refused
// attempt's report must not outlive the attempt it described.
func TestCloudStart_HeartbeatTracksTheCapacityWaitEnding(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)

	origEvery := heartbeatEvery
	heartbeatEvery = 20 * time.Millisecond
	t.Cleanup(func() { heartbeatEvery = origEvery })

	origProbe := cloud.ProbeTimeout
	cloud.ProbeTimeout = 100 * time.Millisecond
	t.Cleanup(func() { cloud.ProbeTimeout = origProbe })

	// A reachable endpoint, so the post-ready probe adds no noise.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	defer l.Close()
	baseURL := fmt.Sprintf("http://127.0.0.1:%d/v1", l.Addr().(*net.TCPAddr).Port)

	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"state":"no-capacity","retry_after_seconds":0}`))
			return
		}
		// The attempt that finds capacity holds its request open while the
		// instance boots and answers only when it is ready.
		time.Sleep(300 * time.Millisecond)
		w.Write([]byte(fmt.Sprintf(`{"state":"ready","base_url":"%s","api_key":"sk-test"}`, baseURL)))
	}))
	defer server.Close()
	writeCloudConfig(t, server.URL)

	stderr := captureStderr(t, func() {
		if err := cmdCloudStart([]string{"--env", "default"}); err != nil {
			t.Fatalf("cmdCloudStart: %v", err)
		}
	})

	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	lastWaiting, attemptAfterWaiting := -1, false
	for i, line := range lines {
		switch {
		case strings.Contains(line, "waiting for capacity"):
			lastWaiting = i
		case lastWaiting != -1 && strings.Contains(line, "waking the instance"):
			attemptAfterWaiting = true
		}
	}
	if lastWaiting == -1 {
		t.Fatalf("no capacity wait in the output, so the wait was not observed:\n%s", stderr)
	}
	if !attemptAfterWaiting {
		t.Errorf("nothing after the last capacity-wait line says a further attempt went out; the booting attempt still reads as a capacity wait:\n%s", stderr)
	}
	if calls != 2 {
		t.Errorf("expected the start to be attempted twice, got %d", calls)
	}
}

// -t is a shorthand for --timeout: a very short one makes a never-ready start
// give up promptly rather than block for the 15m default.
func TestCloudStart_TimeoutShorthand(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	// Never returns ready, so only the timeout ends the wait.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"state":"starting","retry_after_seconds":0}`))
	}))
	defer server.Close()
	writeCloudConfig(t, server.URL)

	// Silence the progress line this writes to stderr; the wait is what matters.
	oldStderr := os.Stderr
	_, w, _ := os.Pipe()
	os.Stderr = w
	defer func() { os.Stderr = oldStderr; w.Close() }()

	done := make(chan error, 1)
	go func() { done <- cmdCloudStart([]string{"--env", "default", "-t", "80ms"}) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected a timeout error, got nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("-t 80ms did not bound the wait; command is still blocking")
	}
}

// Progress must not land on stdout, or `spinloop cloud start | grep '^export '`
// would pick it up.
func TestCloudStart_StdoutCarriesOnlyTheResult(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"ready","base_url":"http://198.51.100.1:8000/v1","api_key":"sk-test"}`))
	}))
	defer server.Close()
	writeCloudConfig(t, server.URL)

	out := captureStdout(t, func() {
		if err := cmdCloudStart([]string{"--env", "default", "--print-env"}); err != nil {
			t.Errorf("cmdCloudStart: %v", err)
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

// glimmerPreset mirrors examples/llamacpp/muse-glimmer-30b: repo and file set
// apart, a drafter at a local path, and --spec-type left to the user.
const glimmerPreset = `[*]
host  = 127.0.0.1
port  = 8080
jinja = 1

[muse-glimmer-30b]
hf               = meta-models/Muse-Glimmer-30B-GGUF
hff              = muse-glimmer-30B-kquant-dynamic.gguf
ctx-size         = 524288
np               = 4
spec-type        = draft-dflash
spec-draft-model = ./Muse-Glimmer-30B-GGUF/dflash-kquant.gguf
spec-draft-ngl   = 999
`

const glimmerSpinloop = "PROVIDER llamacpp\nALIAS muse-glimmer-30b\nCONTEXT 524288\nMODEL meta-models/Muse-Glimmer-30B-GGUF:kquant-dynamic\nPRESET ./preset.ini\n"

func TestDeployConfigFor_CarriesDrafterAsCompanion(t *testing.T) {
	dc := deployConfigFrom(t, glimmerSpinloop, glimmerPreset)

	if got := dc.Companions["draft"]; got != "dflash-kquant.gguf" {
		t.Errorf("companions[draft] = %q, want dflash-kquant.gguf", got)
	}
	// Only the filename travels: the local path is meaningless on the instance.
	for _, arg := range dc.ServeArgs {
		if strings.Contains(arg, "Muse-Glimmer-30B-GGUF/dflash") {
			t.Errorf("serveArgs leaked the local drafter path: %v", dc.ServeArgs)
		}
		if arg == "--spec-draft-model" {
			t.Errorf("serveArgs kept a cloud-owned flag: %v", dc.ServeArgs)
		}
	}
	// How the engine uses the drafter is still the user's to state.
	if !slices.Contains(dc.ServeArgs, "draft-dflash") {
		t.Errorf("--spec-type should pass through, got %v", dc.ServeArgs)
	}
	if !slices.Contains(dc.ServeArgs, "--spec-draft-ngl") {
		t.Errorf("--spec-draft-ngl should pass through, got %v", dc.ServeArgs)
	}
}

func TestDeployConfigFor_DrafterShorthandIsAlsoCloudOwned(t *testing.T) {
	// `md` is the same flag; if the drop-set missed it the local path would
	// reach the instance, where it does not exist.
	preset := strings.Replace(glimmerPreset,
		"spec-draft-model = ./Muse-Glimmer-30B-GGUF/dflash-kquant.gguf",
		"md               = ./Muse-Glimmer-30B-GGUF/dflash-kquant.gguf", 1)
	dc := deployConfigFrom(t, glimmerSpinloop, preset)

	if got := dc.Companions["draft"]; got != "dflash-kquant.gguf" {
		t.Errorf("companions[draft] = %q, want dflash-kquant.gguf", got)
	}
	if strings.Contains(strings.Join(dc.ServeArgs, " "), "dflash-kquant.gguf") {
		t.Errorf("serveArgs leaked the drafter path: %v", dc.ServeArgs)
	}
}

func TestDeployConfigFor_NoCompanionsWhenNoneNamed(t *testing.T) {
	dc := deployConfigFrom(t,
		"PROVIDER llamacpp\nALIAS qwen3.6-27b\nCONTEXT 131072\nPRESET ./preset.ini\n", qwenPreset)
	if len(dc.Companions) != 0 {
		t.Errorf("companions = %v, want none", dc.Companions)
	}
}
