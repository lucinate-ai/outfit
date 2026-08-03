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

// TestCmdApply_ContextOutputAndBaseURL checks that CONTEXT, OUTPUT, and BASEURL
// in an Outfit land as limit.context/limit.output on the model and
// options.baseURL on the provider.
func TestCmdApply_ContextOutputAndBaseURL(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	outfitFile := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitFile, "PROVIDER llamacpp\nMODEL gemma\nCONTEXT 128k\nOUTPUT 32k\nBASEURL http://127.0.0.1:9090/v1\n")
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
	if got := model["limit"].(map[string]any)["output"]; got != float64(32000) {
		t.Errorf("limit.output = %v, want 32000", got)
	}
}

// TestCmdApply_AliasBecomesModelKey checks that ALIAS, not the provider-native
// MODEL, keys the model in the opencode config (and the default model).
func TestCmdApply_AliasBecomesModelKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	outfitFile := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitFile, "PROVIDER llamacpp\nMODEL unsloth/Qwen:Q4_K_M\nALIAS qwen\n")
	captureStdout(t, func() {
		if err := cmdApply([]string{outfitFile}); err != nil {
			t.Fatalf("cmdApply: %v", err)
		}
	})

	m := readConfigMap(t, filepath.Join(dir, "opencode", "opencode.json"))
	models := m["provider"].(map[string]any)["llamacpp"].(map[string]any)["models"].(map[string]any)
	if _, ok := models["qwen"]; !ok {
		t.Errorf("expected model keyed by the alias %q, got %v", "qwen", models)
	}
	if _, ok := models["unsloth/Qwen:Q4_K_M"]; ok {
		t.Error("the raw MODEL should not be a model key when an ALIAS is given")
	}
	if m["model"] != "llamacpp/qwen" {
		t.Errorf("default model = %v, want llamacpp/qwen", m["model"])
	}
}

// TestCmdApply_AliasOnly checks that an ALIAS alone is a valid selection for a
// llama.cpp server, whose model key is only a label.
func TestCmdApply_AliasOnly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	outfitFile := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitFile, "PROVIDER llamacpp\nALIAS my-model\n")
	captureStdout(t, func() {
		if err := cmdApply([]string{outfitFile}); err != nil {
			t.Fatalf("cmdApply: %v", err)
		}
	})

	m := readConfigMap(t, filepath.Join(dir, "opencode", "opencode.json"))
	if m["model"] != "llamacpp/my-model" {
		t.Errorf("default model = %v, want llamacpp/my-model", m["model"])
	}
}

// TestCmdApply_DirectoryPath checks that passing the directory that holds an
// Outfit works the same as passing the Outfit file itself.
func TestCmdApply_DirectoryPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	outfitDir := filepath.Join(dir, "cfg")
	if err := os.Mkdir(outfitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(outfitDir, outfit.DefaultFile), "PROVIDER llamacpp\nALIAS my-model\n")
	captureStdout(t, func() {
		if err := cmdApply([]string{outfitDir}); err != nil {
			t.Fatalf("cmdApply with a directory: %v", err)
		}
	})

	m := readConfigMap(t, filepath.Join(dir, "opencode", "opencode.json"))
	if m["model"] != "llamacpp/my-model" {
		t.Errorf("default model = %v, want llamacpp/my-model", m["model"])
	}
}

// TestCmdRemove_ByAlias checks that a model added under an ALIAS is removed by
// that same alias.
func TestCmdRemove_ByAlias(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	outfitFile := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitFile, "PROVIDER llamacpp\nMODEL unsloth/Qwen:Q4_K_M\nALIAS qwen\n")
	captureStdout(t, func() {
		if err := cmdApply([]string{outfitFile}); err != nil {
			t.Fatalf("cmdApply: %v", err)
		}
	})

	captureStdout(t, func() {
		if err := cmdRemove([]string{"-p", "llamacpp", "-a", "qwen"}); err != nil {
			t.Fatalf("cmdRemove -a: %v", err)
		}
	})

	m := readConfigMap(t, filepath.Join(dir, "opencode", "opencode.json"))
	prov, _ := m["provider"].(map[string]any)
	llamacpp, _ := prov["llamacpp"].(map[string]any)
	models, _ := llamacpp["models"].(map[string]any)
	if _, ok := models["qwen"]; ok {
		t.Error("the aliased model should have been removed")
	}
}

// TestCmdUnapply_RemovesWhatApplyAdded checks that unapply is the inverse of
// apply: applying an Outfit then unapplying the same file removes its models.
func TestCmdUnapply_RemovesWhatApplyAdded(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	outfitFile := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitFile, "PROVIDER llamacpp\nMODEL unsloth/Qwen:Q4_K_M\nALIAS qwen\n")
	captureStdout(t, func() {
		if err := cmdApply([]string{outfitFile}); err != nil {
			t.Fatalf("cmdApply: %v", err)
		}
	})

	captureStdout(t, func() {
		if err := cmdUnapply([]string{outfitFile}); err != nil {
			t.Fatalf("cmdUnapply: %v", err)
		}
	})

	m := readConfigMap(t, filepath.Join(dir, "opencode", "opencode.json"))
	prov, _ := m["provider"].(map[string]any)
	llamacpp, _ := prov["llamacpp"].(map[string]any)
	models, _ := llamacpp["models"].(map[string]any)
	if _, ok := models["qwen"]; ok {
		t.Error("unapply should have removed the aliased model")
	}
}

// TestCmdUnapply_DefaultFileMissing checks that a bare unapply errors when no
// ./Outfit is present, mirroring apply.
func TestCmdUnapply_DefaultFileMissing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir()) // a directory with no Outfit

	if err := cmdUnapply(nil); err == nil {
		t.Error("expected error when ./Outfit is missing")
	}
}

// TestCmdUnapply_RoundTrip checks the realest inverse case: applying an Outfit
// that names a MODEL then unapplying the same file clears the model it added.
func TestCmdUnapply_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("DEEPSEEK_API_KEY", "sk-or-v1-test")

	outfitFile := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitFile, "PROVIDER openrouter\nMODEL deepseek/deepseek-v4-flash\n")
	captureStdout(t, func() {
		if err := cmdApply([]string{outfitFile}); err != nil {
			t.Fatalf("cmdApply: %v", err)
		}
	})

	out := captureStdout(t, func() {
		if err := cmdUnapply([]string{outfitFile}); err != nil {
			t.Fatalf("cmdUnapply: %v", err)
		}
	})
	if !strings.Contains(out, "Removed") {
		t.Errorf("expected a removal summary, got:\n%s", out)
	}

	m := readConfigMap(t, filepath.Join(dir, "opencode", "opencode.json"))
	or, _ := m["provider"].(map[string]any)["openrouter"].(map[string]any)
	if models, ok := or["models"].(map[string]any); ok && len(models) != 0 {
		t.Errorf("model should have been removed, still have: %v", models)
	}
}

// TestCmdUnapply_NoOp checks that unapplying an Outfit whose provider was never
// configured is a harmless no-op rather than an error.
func TestCmdUnapply_NoOp(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	outfitFile := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitFile, "PROVIDER llamacpp\nMODEL gemma\nALIAS gem\n")
	out := captureStdout(t, func() {
		if err := cmdUnapply([]string{outfitFile}); err != nil {
			t.Fatalf("cmdUnapply: %v", err)
		}
	})
	if !strings.Contains(out, "Nothing to remove") {
		t.Errorf("expected a no-op message, got:\n%s", out)
	}
}

// TestRunDispatch_Unapply checks that the top-level dispatcher routes the
// unapply subcommand (the wiring this command added) and surfaces its errors.
func TestRunDispatch_Unapply(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir()) // no ./Outfit, so a bare unapply must error
	if err := run([]string{"unapply"}); err == nil {
		t.Error("expected run(unapply) to error when ./Outfit is missing")
	}
}

// TestCmdApply_OutputFlagOverride checks that a command-line --output overrides
// the Outfit's OUTPUT instruction.
func TestCmdApply_OutputFlagOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	outfitFile := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitFile, "PROVIDER llamacpp\nMODEL gemma\nCONTEXT 128k\nOUTPUT 32k\n")
	captureStdout(t, func() {
		if err := cmdApply([]string{"-o", "64k", outfitFile}); err != nil {
			t.Fatalf("cmdApply: %v", err)
		}
	})

	m := readConfigMap(t, filepath.Join(dir, "opencode", "opencode.json"))
	model := m["provider"].(map[string]any)["llamacpp"].(map[string]any)["models"].(map[string]any)["gemma"].(map[string]any)
	if got := model["limit"].(map[string]any)["output"]; got != float64(64000) {
		t.Errorf("limit.output = %v, want 64000 (flag should override OUTPUT 32k)", got)
	}
}

// TestCmdExport_ContextOutputAndBaseURL checks the export side of the
// round-trip: a non-default base URL, a context window, and an output limit are
// all recovered.
func TestCmdExport_ContextOutputAndBaseURL(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	outfitFile := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitFile, "PROVIDER llamacpp\nMODEL gemma\nCONTEXT 200000\nOUTPUT 50000\nBASEURL http://127.0.0.1:9090/v1\n")
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
	for _, want := range []string{"PROVIDER llamacpp", "MODEL    gemma", "CONTEXT  200000", "OUTPUT   50000", "BASEURL  http://127.0.0.1:9090/v1"} {
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
	mustWrite(t, outfitFile, "PROVIDER openrouter\nMODEL deepseek/deepseek-v4-flash\n")

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
	mustWrite(t, outfitFile, "PROVIDER openrouter\nMODEL deepseek/deepseek-v4-flash\n")
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
	if !strings.Contains(out, "PROVIDER openrouter") || !strings.Contains(out, "MODEL    deepseek/deepseek-v4-flash") {
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

// TestCmdExport_ModelOnly covers a provider configured with a bare model label
// (a llama.cpp model): export should produce a valid Outfit naming the MODEL.
func TestCmdExport_ModelOnly(t *testing.T) {
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
		block, _, err := catalog.BuildProviderBlock(id, cat.Providers[id], "label-"+id, "", noEnv)
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
	mustWrite(t, bad, "MODEL llama3.2\n") // no PROVIDER
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

// TestCmdApply_DirectoryWithoutOutfit checks that passing a directory that
// holds no Outfit file errors rather than silently succeeding.
func TestCmdApply_DirectoryWithoutOutfit(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := cmdApply([]string{t.TempDir()}); err == nil {
		t.Error("expected error for a directory with no Outfit")
	}
}

// TestCmdApply_BaseURLFromRemoteConfig checks that an Outfit with a REMOTE and
// no BASEURL takes the endpoint address from the remote config's base_url —
// the deployment writes that file, so the Outfit does not have to carry it.
func TestCmdApply_BaseURLFromRemoteConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	outfitDir := t.TempDir()
	mustWrite(t, filepath.Join(outfitDir, "remote.json"),
		`{"start_url":"https://start.example/","stop_url":"https://stop.example/","region":"us-east-1","base_url":"http://198.51.100.7:8000/v1"}`)
	outfitFile := filepath.Join(outfitDir, "Outfit")
	mustWrite(t, outfitFile, "PROVIDER llamacpp\nALIAS qwen\nREMOTE remote.json\n")

	out := captureStdout(t, func() {
		if err := cmdApply([]string{outfitFile}); err != nil {
			t.Fatalf("cmdApply: %v", err)
		}
	})
	if !strings.Contains(out, "http://198.51.100.7:8000/v1") {
		t.Errorf("expected the remote base URL to be reported:\n%s", out)
	}

	m := readConfigMap(t, filepath.Join(dir, "opencode", "opencode.json"))
	llamacpp := m["provider"].(map[string]any)["llamacpp"].(map[string]any)
	if got := llamacpp["options"].(map[string]any)["baseURL"]; got != "http://198.51.100.7:8000/v1" {
		t.Errorf("baseURL = %v, want the remote config's base_url", got)
	}
}

// TestCmdApply_OutfitBaseURLBeatsRemoteConfig checks the precedence: a BASEURL
// the user wrote in the Outfit wins over the generated remote config.
func TestCmdApply_OutfitBaseURLBeatsRemoteConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	outfitDir := t.TempDir()
	mustWrite(t, filepath.Join(outfitDir, "remote.json"),
		`{"start_url":"https://start.example/","stop_url":"https://stop.example/","region":"us-east-1","base_url":"http://198.51.100.7:8000/v1"}`)
	outfitFile := filepath.Join(outfitDir, "Outfit")
	mustWrite(t, outfitFile, "PROVIDER llamacpp\nALIAS qwen\nBASEURL http://127.0.0.1:9090/v1\nREMOTE remote.json\n")

	captureStdout(t, func() {
		if err := cmdApply([]string{outfitFile}); err != nil {
			t.Fatalf("cmdApply: %v", err)
		}
	})

	m := readConfigMap(t, filepath.Join(dir, "opencode", "opencode.json"))
	llamacpp := m["provider"].(map[string]any)["llamacpp"].(map[string]any)
	if got := llamacpp["options"].(map[string]any)["baseURL"]; got != "http://127.0.0.1:9090/v1" {
		t.Errorf("baseURL = %v, want the Outfit's own BASEURL", got)
	}
}

// TestCmdApply_RemoteConfigAbsent checks that an Outfit naming a remote config
// that does not exist yet still applies: the deployment that writes it may not
// have run, and apply has nothing to do with starting the endpoint.
func TestCmdApply_RemoteConfigAbsent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	outfitDir := t.TempDir()
	outfitFile := filepath.Join(outfitDir, "Outfit")
	mustWrite(t, outfitFile, "PROVIDER llamacpp\nALIAS qwen\nREMOTE remote.json\n")

	captureStdout(t, func() {
		if err := cmdApply([]string{outfitFile}); err != nil {
			t.Fatalf("cmdApply: %v", err)
		}
	})
}

// TestCmdApply_RemoteConfigMalformed checks that applying an Outfit whose
// path-form REMOTE names a malformed remote.json fails loudly, rather than
// silently applying under the wrong provider name.
func TestCmdApply_RemoteConfigMalformed(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	outfitDir := t.TempDir()
	mustWrite(t, filepath.Join(outfitDir, "remote.json"), "{not valid json")
	outfitFile := filepath.Join(outfitDir, "Outfit")
	mustWrite(t, outfitFile, "PROVIDER llamacpp\nALIAS qwen\nREMOTE remote.json\n")

	err := cmdApply([]string{outfitFile})
	if err == nil {
		t.Fatal("expected an error for a malformed remote config")
	}
	if !strings.Contains(err.Error(), "parsing") {
		t.Errorf("error = %v, want it to mention parsing the remote config", err)
	}
}

// TestCmdUnapply_RemoteConfigMalformed checks the same guard on the unapply side,
// so apply and unapply fail the same way on a broken remote config.
func TestCmdUnapply_RemoteConfigMalformed(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	outfitDir := t.TempDir()
	mustWrite(t, filepath.Join(outfitDir, "remote.json"), "{not valid json")
	outfitFile := filepath.Join(outfitDir, "Outfit")
	mustWrite(t, outfitFile, "PROVIDER llamacpp\nALIAS qwen\nREMOTE remote.json\n")

	if err := cmdUnapply([]string{outfitFile}); err == nil {
		t.Fatal("expected an error for a malformed remote config")
	}
}

// TestCmdApply_RemoteNameIsProviderName checks that a bare-name REMOTE keys the
// harness provider on the environment name — configured from the PROVIDER's
// catalogue entry — with the default model reading as <env>/<model> and the base
// URL taken from that environment's remote.json.
func TestCmdApply_RemoteNameIsProviderName(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	envConfig := filepath.Join(dir, "outfit", "remotes", "dev-1", "remote.json")
	if err := os.MkdirAll(filepath.Dir(envConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, envConfig,
		`{"start_url":"https://start.example/","stop_url":"https://stop.example/","region":"us-east-1","base_url":"http://198.51.100.7:8000/v1","environment":"dev-1"}`)

	outfitDir := t.TempDir()
	outfitFile := filepath.Join(outfitDir, "Outfit")
	mustWrite(t, outfitFile, "PROVIDER llamacpp\nALIAS qwen\nREMOTE dev-1\n")

	captureStdout(t, func() {
		if err := cmdApply([]string{outfitFile}); err != nil {
			t.Fatalf("cmdApply: %v", err)
		}
	})

	m := readConfigMap(t, filepath.Join(dir, "opencode", "opencode.json"))
	prov := m["provider"].(map[string]any)
	if _, ok := prov["llamacpp"]; ok {
		t.Error("provider should be keyed on the environment name, not PROVIDER llamacpp")
	}
	dev1, ok := prov["dev-1"].(map[string]any)
	if !ok {
		t.Fatalf("expected a provider keyed %q, got %v", "dev-1", prov)
	}
	if _, ok := dev1["models"].(map[string]any)["qwen"]; !ok {
		t.Errorf("expected model %q under the dev-1 provider, got %v", "qwen", dev1["models"])
	}
	if got := m["model"]; got != "dev-1/qwen" {
		t.Errorf("default model = %v, want dev-1/qwen", got)
	}
	if got := dev1["options"].(map[string]any)["baseURL"]; got != "http://198.51.100.7:8000/v1" {
		t.Errorf("baseURL = %v, want the environment's base_url", got)
	}
}

// TestCmdApply_RemotePathEnvironmentIsProviderName checks that a path-form REMOTE
// takes the provider name from the environment field of the remote.json it names.
func TestCmdApply_RemotePathEnvironmentIsProviderName(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	outfitDir := t.TempDir()
	mustWrite(t, filepath.Join(outfitDir, "remote.json"),
		`{"start_url":"https://start.example/","stop_url":"https://stop.example/","region":"us-east-1","base_url":"http://198.51.100.7:8000/v1","environment":"dev-1"}`)
	outfitFile := filepath.Join(outfitDir, "Outfit")
	mustWrite(t, outfitFile, "PROVIDER llamacpp\nALIAS qwen\nREMOTE remote.json\n")

	captureStdout(t, func() {
		if err := cmdApply([]string{outfitFile}); err != nil {
			t.Fatalf("cmdApply: %v", err)
		}
	})

	prov := readConfigMap(t, filepath.Join(dir, "opencode", "opencode.json"))["provider"].(map[string]any)
	if _, ok := prov["dev-1"]; !ok {
		t.Errorf("expected a provider keyed %q, got %v", "dev-1", prov)
	}
}

// TestCmdApply_RemotePathWithoutEnvironmentKeepsProvider checks the fallback: a
// path-form REMOTE whose remote.json records no environment keeps the PROVIDER
// value as the provider name.
func TestCmdApply_RemotePathWithoutEnvironmentKeepsProvider(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	outfitDir := t.TempDir()
	mustWrite(t, filepath.Join(outfitDir, "remote.json"),
		`{"start_url":"https://start.example/","stop_url":"https://stop.example/","region":"us-east-1","base_url":"http://198.51.100.7:8000/v1"}`)
	outfitFile := filepath.Join(outfitDir, "Outfit")
	mustWrite(t, outfitFile, "PROVIDER llamacpp\nALIAS qwen\nREMOTE remote.json\n")

	captureStdout(t, func() {
		if err := cmdApply([]string{outfitFile}); err != nil {
			t.Fatalf("cmdApply: %v", err)
		}
	})

	prov := readConfigMap(t, filepath.Join(dir, "opencode", "opencode.json"))["provider"].(map[string]any)
	if _, ok := prov["llamacpp"]; !ok {
		t.Errorf("expected the provider to stay keyed %q, got %v", "llamacpp", prov)
	}
	if got := prov["llamacpp"].(map[string]any)["name"]; got != "llama.cpp" {
		t.Errorf("display name = %v, want the plain engine name when no environment resolves", got)
	}
}

// TestCmdApply_RemoteProviderLabelledPerEnvironment checks that a remote provider
// gets a display name qualified by its environment, so it reads distinctly from a
// local engine of the same kind, which keeps the bare engine name.
func TestCmdApply_RemoteProviderLabelledPerEnvironment(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	envConfig := filepath.Join(dir, "outfit", "remotes", "dev-2", "remote.json")
	if err := os.MkdirAll(filepath.Dir(envConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, envConfig,
		`{"start_url":"https://start.example/","stop_url":"https://stop.example/","region":"us-east-1","base_url":"http://198.51.100.7:8000/v1","environment":"dev-2"}`)

	outfitDir := t.TempDir()
	remoteOutfit := filepath.Join(outfitDir, "Outfit")
	mustWrite(t, remoteOutfit, "PROVIDER llamacpp\nALIAS qwen\nREMOTE dev-2\n")
	localOutfit := filepath.Join(outfitDir, "Local")
	mustWrite(t, localOutfit, "PROVIDER llamacpp\nALIAS qwen\nBASEURL http://127.0.0.1:8080/v1\n")

	captureStdout(t, func() {
		if err := cmdApply([]string{remoteOutfit}); err != nil {
			t.Fatalf("cmdApply remote: %v", err)
		}
		if err := cmdApply([]string{localOutfit}); err != nil {
			t.Fatalf("cmdApply local: %v", err)
		}
	})

	prov := readConfigMap(t, filepath.Join(dir, "opencode", "opencode.json"))["provider"].(map[string]any)
	dev2, ok := prov["dev-2"].(map[string]any)
	if !ok {
		t.Fatalf("expected a provider keyed %q, got %v", "dev-2", prov)
	}
	if got := dev2["name"]; got != "llama.cpp (dev-2)" {
		t.Errorf("remote display name = %v, want %q", got, "llama.cpp (dev-2)")
	}
	local, ok := prov["llamacpp"].(map[string]any)
	if !ok {
		t.Fatalf("expected a local provider keyed %q, got %v", "llamacpp", prov)
	}
	if got := local["name"]; got != "llama.cpp" {
		t.Errorf("local display name = %v, want the bare engine name %q", got, "llama.cpp")
	}
	if dev2["name"] == local["name"] {
		t.Errorf("remote and local providers share a display name %v; they must be distinct", dev2["name"])
	}
}

// TestCmdApply_RemoteReapplyRefreshesLabel checks the stale-label mitigation: a
// provider that a previous apply left with a bare engine name (as it would be
// before this behaviour existed) has its display name refreshed to the
// environment-qualified label when the same Outfit is applied again — the
// deep-merge overwrites the old name rather than keeping it.
func TestCmdApply_RemoteReapplyRefreshesLabel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	envConfig := filepath.Join(dir, "outfit", "remotes", "dev-2", "remote.json")
	if err := os.MkdirAll(filepath.Dir(envConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, envConfig,
		`{"start_url":"https://start.example/","stop_url":"https://stop.example/","region":"us-east-1","base_url":"http://198.51.100.7:8000/v1","environment":"dev-2"}`)

	// Seed a config as an earlier apply would have left it: the dev-2 provider
	// carries the bare engine name, with no environment qualifier.
	opencodeDir := filepath.Join(dir, "opencode")
	if err := os.MkdirAll(opencodeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(opencodeDir, "opencode.json")
	mustWrite(t, configFile,
		`{"provider":{"dev-2":{"name":"llama.cpp","npm":"@ai-sdk/openai-compatible","models":{"qwen":{"name":"qwen"}}}}}`)

	outfitDir := t.TempDir()
	outfitFile := filepath.Join(outfitDir, "Outfit")
	mustWrite(t, outfitFile, "PROVIDER llamacpp\nALIAS qwen\nREMOTE dev-2\n")

	captureStdout(t, func() {
		if err := cmdApply([]string{outfitFile}); err != nil {
			t.Fatalf("cmdApply: %v", err)
		}
	})

	prov := readConfigMap(t, configFile)["provider"].(map[string]any)
	dev2 := prov["dev-2"].(map[string]any)
	if got := dev2["name"]; got != "llama.cpp (dev-2)" {
		t.Errorf("display name = %v, want the stale bare label refreshed to %q", got, "llama.cpp (dev-2)")
	}
}

// TestCmdUnapply_RemoveEnvironmentNamedProvider checks apply/unapply symmetry for
// a remote Outfit: unapply removes the environment-named provider that apply
// wrote, not the PROVIDER-named one (which was never written).
func TestCmdUnapply_RemoveEnvironmentNamedProvider(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	envConfig := filepath.Join(dir, "outfit", "remotes", "dev-1", "remote.json")
	if err := os.MkdirAll(filepath.Dir(envConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, envConfig,
		`{"start_url":"https://start.example/","stop_url":"https://stop.example/","region":"us-east-1","base_url":"http://198.51.100.7:8000/v1","environment":"dev-1"}`)

	outfitDir := t.TempDir()
	outfitFile := filepath.Join(outfitDir, "Outfit")
	mustWrite(t, outfitFile, "PROVIDER llamacpp\nALIAS qwen\nREMOTE dev-1\n")

	captureStdout(t, func() {
		if err := cmdApply([]string{outfitFile}); err != nil {
			t.Fatalf("cmdApply: %v", err)
		}
	})
	captureStdout(t, func() {
		if err := cmdUnapply([]string{outfitFile}); err != nil {
			t.Fatalf("cmdUnapply: %v", err)
		}
	})

	prov := readConfigMap(t, filepath.Join(dir, "opencode", "opencode.json"))["provider"].(map[string]any)
	dev1, _ := prov["dev-1"].(map[string]any)
	if models, ok := dev1["models"].(map[string]any); ok && len(models) != 0 {
		t.Errorf("unapply should have removed the dev-1 model, still have: %v", models)
	}
}
