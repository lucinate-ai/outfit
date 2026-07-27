package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestRemoteDeploy_PostsTheConfig(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)

	var got remote.DeployConfig
	var gotMethod, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"deployed":true,"seeding":true,"seedInstanceId":"i-123"}`))
	}))
	defer server.Close()

	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile("Outfit", []byte(
		"PROVIDER llamacpp\nALIAS qwen3.6-27b\nCONTEXT 131072\nPRESET ./preset.ini\nREMOTE ./remote.json\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("preset.ini", []byte(qwenPreset), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := json.Marshal(remote.Config{
		StartURL:  server.URL,
		StopURL:   server.URL,
		DeployURL: server.URL,
		Region:    "us-east-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("remote.json", cfg, 0o600); err != nil {
		t.Fatal(err)
	}

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
	if got.Runner != "llamacpp" || got.ModelID != "unsloth/Qwen3.6-27B-MTP-GGUF" || got.ContextSize != 131072 {
		t.Errorf("posted config is wrong: %+v", got)
	}
	// The Lambda derives the prefix, so the body must not pin one.
	if strings.Contains(strings.ToLower(out), "weightsprefix") {
		t.Errorf("deploy should not mention a weights prefix, got:\n%s", out)
	}
	if !strings.Contains(out, "seeding the weights on i-123") {
		t.Errorf("seeding should be reported with the instance id, got:\n%s", out)
	}
}

func TestRemoteDeploy_NeedsDeployURL(t *testing.T) {
	isolateConfig(t)
	stubAWSEnv(t)
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile("Outfit", []byte(
		"PROVIDER llamacpp\nMODEL org/m:Q4\nCONTEXT 32k\nREMOTE ./remote.json\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	// A config written before `remote deploy` existed: start/stop still work, so
	// only deploy should complain, and it should say what to add.
	cfg, _ := json.Marshal(remote.Config{StartURL: "https://x.lambda-url.us-east-1.on.aws/", StopURL: "https://y.lambda-url.us-east-1.on.aws/", Region: "us-east-1"})
	if err := os.WriteFile("remote.json", cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	err := cmdRemoteDeploy(nil)
	if err == nil || !strings.Contains(err.Error(), "deploy_url") {
		t.Errorf("want an error naming deploy_url, got %v", err)
	}
}

func TestRemoteDeploy_DryRunSendsNothing(t *testing.T) {
	isolateConfig(t)
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer server.Close()

	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile("Outfit", []byte(
		"PROVIDER llamacpp\nALIAS qwen3.6-27b\nPRESET ./preset.ini\nREMOTE ./remote.json\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("preset.ini", []byte(qwenPreset), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := json.Marshal(remote.Config{StartURL: server.URL, StopURL: server.URL, DeployURL: server.URL, Region: "us-east-1"})
	if err := os.WriteFile("remote.json", cfg, 0o600); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := cmdRemoteDeploy([]string{"--dry-run"}); err != nil {
			t.Errorf("cmdRemoteDeploy --dry-run: %v", err)
		}
	})
	if called {
		t.Error("--dry-run must not call the deploy Lambda")
	}
	if !strings.Contains(out, "unsloth/Qwen3.6-27B-MTP-GGUF") {
		t.Errorf("--dry-run should print the config, got:\n%s", out)
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
