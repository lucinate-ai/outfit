package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readPiModels reads ~/.pi/agent/models.json (HOME must be set) for assertions.
func readPiModels(t *testing.T, home string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".pi", "agent", "models.json"))
	if err != nil {
		t.Fatalf("read models.json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("models.json not valid JSON: %v", err)
	}
	return m
}

// isolateConfig points HOME and XDG_CONFIG_HOME at fresh temp dirs and clears
// OUTFIT_HARNESS, so harness resolution and the Pi/opencode/preference files are
// all sandboxed.
func isolateConfig(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("OUTFIT_HARNESS", "")
	return home
}

func TestHarness_GetAndSet(t *testing.T) {
	isolateConfig(t)

	// Default before any preference is stored. --get reports the active harness
	// rather than launching it (a bare `harness` execs the agent binary, which
	// would hang an interactive TUI under test).
	out := captureStdout(t, func() {
		if err := cmdHarness([]string{"--get"}); err != nil {
			t.Fatalf("cmdHarness --get: %v", err)
		}
	})
	if !strings.Contains(out, "Active harness: opencode") || !strings.Contains(out, "Stored preference: none") {
		t.Errorf("unexpected default harness output:\n%s", out)
	}

	// Set a preference.
	out = captureStdout(t, func() {
		if err := cmdHarness([]string{"--set", "pi"}); err != nil {
			t.Fatalf("cmdHarness --set: %v", err)
		}
	})
	if !strings.Contains(out, `Default harness set to "pi"`) {
		t.Errorf("unexpected --set output:\n%s", out)
	}

	// It is now the active harness.
	out = captureStdout(t, func() {
		if err := cmdHarness([]string{"--get"}); err != nil {
			t.Fatalf("cmdHarness --get: %v", err)
		}
	})
	if !strings.Contains(out, "Active harness: pi") || !strings.Contains(out, "Stored preference: pi") {
		t.Errorf("preference not reflected:\n%s", out)
	}

	// Unknown harness is rejected.
	if err := cmdHarness([]string{"--set", "bogus"}); err == nil {
		t.Error("expected error setting an unknown harness")
	}
}

func TestCmdShow(t *testing.T) {
	isolateConfig(t)
	t.Setenv("DEEPSEEK_API_KEY", "sk-or-v1-test")

	// Nothing configured yet.
	out := captureStdout(t, func() {
		if err := cmdShow(nil); err != nil {
			t.Fatalf("cmdShow: %v", err)
		}
	})
	if !strings.Contains(out, "Harness: opencode (from default)") {
		t.Errorf("missing harness header:\n%s", out)
	}
	if !strings.Contains(out, "No providers configured") {
		t.Errorf("expected empty-config notice:\n%s", out)
	}

	// After an add, show lists the provider, its models, their limits, and the
	// default model.
	captureStdout(t, func() {
		if err := cmdAdd([]string{"-p", "openrouter", "-f", "deepseek-v4", "-c", "128k"}); err != nil {
			t.Fatalf("cmdAdd: %v", err)
		}
	})
	out = captureStdout(t, func() {
		if err := cmdShow(nil); err != nil {
			t.Fatalf("cmdShow: %v", err)
		}
	})
	if !strings.Contains(out, "Configured providers:") || !strings.Contains(out, "openrouter") {
		t.Errorf("provider not shown:\n%s", out)
	}
	if !strings.Contains(out, "context 128000") || !strings.Contains(out, "output 32000") {
		t.Errorf("model limits not shown:\n%s", out)
	}
	if !strings.Contains(out, "Default model: openrouter/") {
		t.Errorf("default model not shown:\n%s", out)
	}
}

// writeOpencodeConfig writes raw JSON to the opencode config under HOME so a
// test can stage a config `show` then reads back — including shapes that `add`
// would never produce, like a provider with no models.
func writeOpencodeConfig(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir opencode config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "opencode.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write opencode.json: %v", err)
	}
}

func TestCmdShow_BaseURLAndEmptyProvider(t *testing.T) {
	home := isolateConfig(t)

	// A provider carrying a base URL override, alongside one configured with no
	// models at all — a shape `add` won't produce but a hand-edited config can.
	writeOpencodeConfig(t, home, `{
	  "provider": {
	    "llamacpp": {
	      "options": {"baseURL": "http://127.0.0.1:9999/v1"},
	      "models": {"my-model": {"limit": {"context": 128000, "output": 32000}}}
	    },
	    "bare": {
	      "models": {}
	    }
	  }
	}`)

	out := captureStdout(t, func() {
		if err := cmdShow(nil); err != nil {
			t.Fatalf("cmdShow: %v", err)
		}
	})
	if !strings.Contains(out, "base url: http://127.0.0.1:9999/v1") {
		t.Errorf("base URL not shown:\n%s", out)
	}
	if !strings.Contains(out, "model my-model (context 128000, output 32000)") {
		t.Errorf("model line not shown:\n%s", out)
	}
	if !strings.Contains(out, "(no models)") {
		t.Errorf("empty provider not flagged:\n%s", out)
	}
}

func TestCmdShow_Errors(t *testing.T) {
	home := isolateConfig(t)

	// An unrecognised flag is surfaced rather than silently ignored.
	if err := cmdShow([]string{"--nope"}); err == nil {
		t.Error("expected error for an unknown flag")
	}

	// A malformed harness config is reported, not swallowed.
	writeOpencodeConfig(t, home, "{ this is not json")
	if err := cmdShow(nil); err == nil {
		t.Error("expected error reading a malformed config")
	}
}

func TestCmdShow_HarnessOverride(t *testing.T) {
	isolateConfig(t)

	// The opencode default is configured...
	t.Setenv("DEEPSEEK_API_KEY", "sk-or-v1-test")
	captureStdout(t, func() {
		if err := cmdAdd([]string{"-p", "openrouter", "-f", "deepseek-v4"}); err != nil {
			t.Fatalf("cmdAdd: %v", err)
		}
	})

	// ...but -H pi reads the (empty) Pi config instead, naming the flag as the
	// source, without disturbing the stored default.
	out := captureStdout(t, func() {
		if err := cmdShow([]string{"-H", "pi"}); err != nil {
			t.Fatalf("cmdShow -H pi: %v", err)
		}
	})
	if !strings.Contains(out, "Harness: pi (from --harness flag)") {
		t.Errorf("harness override not honoured:\n%s", out)
	}
	if !strings.Contains(out, "No providers configured") {
		t.Errorf("Pi config should be empty:\n%s", out)
	}

	// An unknown harness is rejected.
	if err := cmdShow([]string{"-H", "bogus"}); err == nil {
		t.Error("expected error for an unknown harness")
	}
}

func TestCmdShow_PiPopulated(t *testing.T) {
	isolateConfig(t)

	// Configure a provider on Pi, which — unlike opencode — has no default-model
	// setting, so `show` must list the provider and its models without inventing
	// a "Default model:" line.
	captureStdout(t, func() {
		if err := cmdAdd([]string{"-H", "pi", "-p", "ollama", "-f", "llama", "-c", "128k"}); err != nil {
			t.Fatalf("cmdAdd -H pi: %v", err)
		}
	})

	out := captureStdout(t, func() {
		if err := cmdShow([]string{"-H", "pi"}); err != nil {
			t.Fatalf("cmdShow -H pi: %v", err)
		}
	})
	if !strings.Contains(out, "Configured providers:") || !strings.Contains(out, "ollama") {
		t.Errorf("Pi provider not shown:\n%s", out)
	}
	if !strings.Contains(out, "context 128000") {
		t.Errorf("model context not shown:\n%s", out)
	}
	if strings.Contains(out, "Default model:") {
		t.Errorf("Pi has no default model; the line should be omitted:\n%s", out)
	}
}

func TestCmdAdd_PiHarnessViaFlag(t *testing.T) {
	home := isolateConfig(t)
	t.Setenv("DEEPSEEK_API_KEY", "sk-or-v1-test")

	out := captureStdout(t, func() {
		if err := cmdAdd([]string{"-H", "pi", "-p", "openrouter", "-f", "deepseek-v4", "-c", "128k"}); err != nil {
			t.Fatalf("cmdAdd: %v", err)
		}
	})
	if !strings.Contains(out, "models.json") || !strings.Contains(out, "Run 'pi'") {
		t.Errorf("expected Pi-flavoured output:\n%s", out)
	}

	prov := readPiModels(t, home)["providers"].(map[string]any)["openrouter"].(map[string]any)
	if prov["api"] != "openai-completions" {
		t.Errorf("api = %v", prov["api"])
	}
	if prov["baseUrl"] != "https://openrouter.ai/api/v1" {
		t.Errorf("baseUrl = %v", prov["baseUrl"])
	}
	// API key is an env interpolation; the resolved secret must not be written.
	if prov["apiKey"] != "$DEEPSEEK_API_KEY" {
		t.Errorf("apiKey = %v, want $DEEPSEEK_API_KEY", prov["apiKey"])
	}
	for _, m := range prov["models"].([]any) {
		if m.(map[string]any)["contextWindow"] != float64(128000) {
			t.Errorf("model %v missing context window", m)
		}
	}

	// opencode must be untouched (no opencode.json written under HOME/.config).
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "opencode.json")); !os.IsNotExist(err) {
		t.Error("opencode config should not have been written for a Pi add")
	}
}

func TestCmdAdd_PiHarnessViaEnvAndPreference(t *testing.T) {
	home := isolateConfig(t)

	// Via OUTFIT_HARNESS.
	t.Setenv("OUTFIT_HARNESS", "pi")
	captureStdout(t, func() {
		if err := cmdAdd([]string{"-p", "ollama", "-f", "llama"}); err != nil {
			t.Fatalf("cmdAdd via env: %v", err)
		}
	})
	if _, ok := readPiModels(t, home)["providers"].(map[string]any)["ollama"]; !ok {
		t.Error("ollama not written to Pi config via OUTFIT_HARNESS")
	}

	// Via stored preference (env cleared).
	t.Setenv("OUTFIT_HARNESS", "")
	if err := cmdHarness([]string{"--set", "pi"}); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() {
		if err := cmdAdd([]string{"-p", "llamacpp", "-m", "local-model"}); err != nil {
			t.Fatalf("cmdAdd via preference: %v", err)
		}
	})
	if _, ok := readPiModels(t, home)["providers"].(map[string]any)["llamacpp"]; !ok {
		t.Error("llamacpp not written to Pi config via stored preference")
	}
}

func TestCmdAdd_PiUnsupportedProvider(t *testing.T) {
	isolateConfig(t)
	if err := cmdAdd([]string{"-H", "pi", "-p", "amazon-bedrock", "-f", "claude"}); err == nil {
		t.Error("expected error adding a Pi-unsupported provider")
	}
}

func TestCmdExportRemove_PiRoundTrip(t *testing.T) {
	home := isolateConfig(t)
	t.Setenv("OUTFIT_HARNESS", "pi")

	captureStdout(t, func() {
		if err := cmdAdd([]string{"-p", "ollama", "-f", "llama", "-c", "200000"}); err != nil {
			t.Fatalf("cmdAdd: %v", err)
		}
	})

	out := captureStdout(t, func() {
		if err := cmdExport(nil); err != nil {
			t.Fatalf("cmdExport: %v", err)
		}
	})
	if !strings.Contains(out, "PROVIDER ollama") || !strings.Contains(out, "FAMILY   llama") {
		t.Errorf("unexpected Pi export:\n%s", out)
	}
	if !strings.Contains(out, "CONTEXT  200000") {
		t.Errorf("export did not recover the context window:\n%s", out)
	}

	// Remove the whole provider from the Pi config.
	out = captureStdout(t, func() {
		if err := cmdRemove([]string{"-p", "ollama"}); err != nil {
			t.Fatalf("cmdRemove: %v", err)
		}
	})
	if !strings.Contains(out, "Removed provider") {
		t.Errorf("unexpected remove output:\n%s", out)
	}
	if _, ok := readPiModels(t, home)["providers"].(map[string]any)["ollama"]; ok {
		t.Error("ollama should have been removed from the Pi config")
	}
}
