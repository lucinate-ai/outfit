package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucinate-ai/outfit/internal/catalog"
	"github.com/lucinate-ai/outfit/internal/opencode"
	"github.com/lucinate-ai/outfit/internal/outfit"
)

// mustWrite writes content to path or fails the test.
func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestCmdApply_ContextAndBaseURL checks that CONTEXT and BASEURL in an Outfit
// land as limit.context on the model and options.baseURL on the provider.
func TestCmdApply_ContextAndBaseURL(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	outfitFile := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitFile, "PROVIDER llamacpp\nMODEL gemma\nCONTEXT 128k\nBASEURL http://127.0.0.1:9090/v1\n")
	captureStdout(t, func() {
		if err := cmdApply([]string{outfitFile}); err != nil {
			t.Fatalf("cmdApply: %v", err)
		}
	})

	m := readConfigMap(t, filepath.Join(dir, "opencode", "opencode.json"))
	llamacpp := m["provider"].(map[string]any)["llamacpp"].(map[string]any)
	if got := llamacpp["options"].(map[string]any)["baseURL"]; got != "http://127.0.0.1:9090/v1" {
		t.Errorf("baseURL = %v", got)
	}
	model := llamacpp["models"].(map[string]any)["gemma"].(map[string]any)
	if got := model["limit"].(map[string]any)["context"]; got != float64(128000) {
		t.Errorf("limit.context = %v, want 128000", got)
	}
}

// TestCmdExport_ContextAndBaseURL checks the export side of the round-trip: a
// non-default base URL and a context window are both recovered.
func TestCmdExport_ContextAndBaseURL(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	outfitFile := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitFile, "PROVIDER llamacpp\nMODEL gemma\nCONTEXT 200000\nBASEURL http://127.0.0.1:9090/v1\n")
	captureStdout(t, func() {
		if err := cmdApply([]string{outfitFile}); err != nil {
			t.Fatalf("cmdApply: %v", err)
		}
	})

	out := captureStdout(t, func() {
		if err := cmdExport(nil); err != nil {
			t.Fatalf("cmdExport: %v", err)
		}
	})
	for _, want := range []string{"PROVIDER llamacpp", "MODEL    gemma", "CONTEXT  200000", "BASEURL  http://127.0.0.1:9090/v1"} {
		if !strings.Contains(out, want) {
			t.Errorf("export missing %q:\n%s", want, out)
		}
	}
}

func TestCmdApply_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("DEEPSEEK_API_KEY", "sk-or-v1-test")

	outfitFile := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitFile, "PROVIDER openrouter\nFAMILY deepseek-v4\n")

	out := captureStdout(t, func() {
		if err := cmdApply([]string{outfitFile}); err != nil {
			t.Fatalf("cmdApply: %v", err)
		}
	})
	if !strings.Contains(out, "Default model:") {
		t.Errorf("missing summary in output:\n%s", out)
	}

	m := readConfigMap(t, filepath.Join(dir, "opencode", "opencode.json"))
	if _, ok := m["provider"].(map[string]any)["openrouter"]; !ok {
		t.Error("openrouter provider not written")
	}
	if m["model"] != "openrouter/deepseek/deepseek-v4-flash" {
		t.Errorf("model = %v", m["model"])
	}
}

func TestCmdApply_DefaultFileMissing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir()) // a directory with no Outfit

	if err := cmdApply(nil); err == nil {
		t.Error("expected error when ./Outfit is missing")
	}
}

func TestCmdExport_RoundTripsWithApply(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("DEEPSEEK_API_KEY", "sk-or-v1-test")

	// Seed config via apply, then export it back out.
	outfitFile := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitFile, "PROVIDER openrouter\nFAMILY deepseek-v4\n")
	captureStdout(t, func() {
		if err := cmdApply([]string{outfitFile}); err != nil {
			t.Fatalf("cmdApply: %v", err)
		}
	})

	out := captureStdout(t, func() {
		if err := cmdExport(nil); err != nil {
			t.Fatalf("cmdExport: %v", err)
		}
	})
	// The family's models match deepseek-v4, so export should name the family.
	if !strings.Contains(out, "PROVIDER openrouter") || !strings.Contains(out, "FAMILY   deepseek-v4") {
		t.Errorf("unexpected export:\n%s", out)
	}

	// And the exported Outfit must parse cleanly.
	if _, err := outfit.Parse([]byte(out)); err != nil {
		t.Errorf("exported Outfit does not parse: %v", err)
	}
}

func TestCmdExport_NoProviders(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := cmdExport(nil); err == nil {
		t.Error("expected error when nothing is configured")
	}
}

// TestCmdExport_ModelOnlyFallsBackToModel covers a provider whose configured
// models match no known family (a bare llama.cpp label): export should still
// produce a valid Outfit naming the MODEL.
func TestCmdExport_ModelOnlyFallsBackToModel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	outfitFile := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitFile, "PROVIDER llamacpp\nMODEL my-local-model\n")
	captureStdout(t, func() {
		if err := cmdApply([]string{outfitFile}); err != nil {
			t.Fatalf("cmdApply: %v", err)
		}
	})

	out := captureStdout(t, func() {
		if err := cmdExport(nil); err != nil {
			t.Fatalf("cmdExport: %v", err)
		}
	})
	if !strings.Contains(out, "PROVIDER llamacpp") || !strings.Contains(out, "MODEL    my-local-model") {
		t.Errorf("unexpected export:\n%s", out)
	}
	if strings.Contains(out, "FAMILY") {
		t.Errorf("did not expect a FAMILY line for an unrecognised model:\n%s", out)
	}
	// The provider sits on its catalogue-default base URL, so export should not
	// record a redundant BASEURL line.
	if strings.Contains(out, "BASEURL") {
		t.Errorf("did not expect a BASEURL line for the default base URL:\n%s", out)
	}
}

// TestCmdExport_FamilyPlusNonDefaultModel checks that when the default model is
// not the family's own default, export keeps both the FAMILY and the MODEL.
func TestCmdExport_FamilyPlusNonDefaultModel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("DEEPSEEK_API_KEY", "sk-or-v1-test")

	cat, _ := catalog.Load()
	fam := cat.Providers["openrouter"].Families["deepseek-v4"]
	// Find a model in the family that is not its default.
	var nonDefault string
	for _, k := range fam.ModelKeys() {
		if k != fam.DefaultModel {
			nonDefault = k
			break
		}
	}
	if nonDefault == "" {
		t.Skip("family has no non-default model to exercise this path")
	}

	outfitFile := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitFile, "PROVIDER openrouter\nFAMILY deepseek-v4\nMODEL "+nonDefault+"\n")
	captureStdout(t, func() {
		if err := cmdApply([]string{outfitFile}); err != nil {
			t.Fatalf("cmdApply: %v", err)
		}
	})

	out := captureStdout(t, func() {
		if err := cmdExport(nil); err != nil {
			t.Fatalf("cmdExport: %v", err)
		}
	})
	if !strings.Contains(out, "FAMILY   deepseek-v4") || !strings.Contains(out, "MODEL    "+nonDefault) {
		t.Errorf("expected both FAMILY and the non-default MODEL:\n%s", out)
	}
}

// TestCmdExport_MultipleProviders covers provider selection when several are
// configured: without a hint it errors, with -p it exports the chosen one, and
// an unknown -p errors.
func TestCmdExport_MultipleProviders(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	// Seed two providers with no default model, so neither is implied.
	path, _ := opencode.ResolveConfigFile()
	cat, _ := catalog.Load()
	for _, id := range []string{"ollama", "llamacpp"} {
		block, _, err := catalog.BuildProviderBlock(id, cat.Providers[id], "", "label-"+id, "", noEnv)
		if err != nil {
			t.Fatal(err)
		}
		if err := opencode.WriteConfig(path, id, block, ""); err != nil {
			t.Fatal(err)
		}
	}

	if err := cmdExport(nil); err == nil {
		t.Error("expected error when several providers are configured and none is implied")
	}

	out := captureStdout(t, func() {
		if err := cmdExport([]string{"-p", "ollama"}); err != nil {
			t.Fatalf("cmdExport -p ollama: %v", err)
		}
	})
	if !strings.Contains(out, "PROVIDER ollama") {
		t.Errorf("export -p ollama gave:\n%s", out)
	}

	if err := cmdExport([]string{"-p", "nonesuch"}); err == nil {
		t.Error("expected error for a provider that is not configured")
	}
}

func TestCmdApply_BadOutfitContent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	bad := filepath.Join(dir, "Outfit")
	mustWrite(t, bad, "FAMILY llama\n") // no PROVIDER
	if err := cmdApply([]string{bad}); err == nil {
		t.Error("expected error for an Outfit without a PROVIDER")
	}
}

func TestCmdApply_MissingExplicitPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := cmdApply([]string{filepath.Join(t.TempDir(), "nope.outfit")}); err == nil {
		t.Error("expected error for a missing explicit path")
	}
}
