package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const samplePreset = `[*]
ctx-size = 0
mmap     = 1

[qwen]
hf       = unsloth/Qwen:Q4_K_M
ctx-size = 32768
temp     = 1.0
`

// writePresetOutfit writes a preset.ini and an Outfit referencing it (relative)
// into a fresh temp dir, and returns the Outfit's path.
func writePresetOutfit(t *testing.T, outfitBody string) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "preset.ini"), samplePreset)
	outfitPath := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitPath, outfitBody)
	return outfitPath
}

// stubServer writes a script named binName that records its argv to argsFile,
// points *target at it, and restores the original afterwards.
func stubServer(t *testing.T, target *string, binName, argsFile string) {
	t.Helper()
	script := filepath.Join(t.TempDir(), binName)
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := *target
	*target = script
	t.Cleanup(func() { *target = orig })
}

// stubLlamaServer points llamaServerBinary at a script that records its argv to
// argsFile, and restores the original binary afterwards.
func stubLlamaServer(t *testing.T, argsFile string) {
	t.Helper()
	stubServer(t, &llamaServerBinary, "llama-server", argsFile)
}

// stubOMLX does the same for the oMLX CLI, so no Apple Silicon (or oMLX
// install) is needed to pin the argv serve builds for it.
func stubOMLX(t *testing.T, argsFile string) {
	t.Helper()
	stubServer(t, &omlxBinary, "omlx-cli", argsFile)
}

// omlxPreset is a preset written in oMLX's own flag vocabulary. `m` is here
// deliberately: llama.cpp's dialect would rewrite it to --model, and this preset
// must not be read that way.
const omlxPreset = `[*]
model-dir = /Users/me/models
host      = 127.0.0.1
port      = 8000

[qwen]
memory-guard = safe
m            = should-stay-literal
`

// writeOMLXOutfit writes an oMLX preset.ini and an Outfit referencing it into a
// fresh temp dir, and returns the Outfit's path.
func writeOMLXOutfit(t *testing.T, outfitBody string) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "preset.ini"), omlxPreset)
	outfitPath := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitPath, outfitBody)
	return outfitPath
}

func TestCmdServe_PresetDryRun(t *testing.T) {
	outfitPath := writePresetOutfit(t, "PROVIDER llamacpp\nALIAS qwen\nPRESET preset.ini\n")

	out := captureStdout(t, func() {
		if err := cmdServe([]string{"--dry-run", outfitPath}); err != nil {
			t.Fatalf("cmdServe: %v", err)
		}
	})

	if !strings.Contains(out, "Using preset") || !strings.Contains(out, "preset.ini") {
		t.Errorf("missing preset path in output:\n%s", out)
	}
	if !strings.Contains(out, "model qwen") {
		t.Errorf("missing model in header:\n%s", out)
	}
	// The section's ctx-size wins over the global default; mmap is a bare flag;
	// hf normalises to --hf-repo.
	for _, want := range []string{"--ctx-size 32768", "--mmap", "--hf-repo unsloth/Qwen:Q4_K_M", "--temp 1.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("command missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "--ctx-size 0") {
		t.Errorf("global ctx-size should have been overridden:\n%s", out)
	}
}

func TestCmdServe_PresetRunsLlamaServer(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args")
	stubLlamaServer(t, argsFile)
	outfitPath := writePresetOutfit(t, "PROVIDER llamacpp\nALIAS qwen\nPRESET preset.ini\n")

	captureStdout(t, func() {
		if err := cmdServe([]string{outfitPath}); err != nil {
			t.Fatalf("cmdServe: %v", err)
		}
	})

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("stub did not run: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "--hf-repo") || !strings.Contains(got, "unsloth/Qwen:Q4_K_M") {
		t.Errorf("llama-server got unexpected args:\n%s", got)
	}
}

// TestCmdServe_PresetSelectsByAlias checks that, among several preset sections,
// the Outfit's ALIAS picks the right one.
func TestCmdServe_PresetSelectsByAlias(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "preset.ini"), "[a]\nhf = org/a\n[b]\nhf = org/b\n")
	outfitPath := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitPath, "PROVIDER llamacpp\nALIAS b\nPRESET preset.ini\n")

	out := captureStdout(t, func() {
		if err := cmdServe([]string{"--dry-run", outfitPath}); err != nil {
			t.Fatalf("cmdServe: %v", err)
		}
	})
	if !strings.Contains(out, "--hf-repo org/b") {
		t.Errorf("ALIAS did not select section [b]:\n%s", out)
	}
}

// TestCmdServe_OutfitOverridesPreset checks that values stated in the Outfit win
// over the preset's: CONTEXT replaces the section's ctx-size, BASEURL replaces
// host/port, and ALIAS adds --alias.
func TestCmdServe_OutfitOverridesPreset(t *testing.T) {
	outfitPath := writePresetOutfit(t, "PROVIDER llamacpp\nALIAS qwen\nCONTEXT 8192\nBASEURL http://0.0.0.0:9999/v1\nPRESET preset.ini\n")

	out := captureStdout(t, func() {
		if err := cmdServe([]string{"--dry-run", outfitPath}); err != nil {
			t.Fatalf("cmdServe: %v", err)
		}
	})
	for _, want := range []string{"--ctx-size 8192", "--host 0.0.0.0", "--port 9999", "--alias qwen"} {
		if !strings.Contains(out, want) {
			t.Errorf("Outfit value did not override the preset, missing %q:\n%s", want, out)
		}
	}
	// The preset's own ctx-size of 32768 must be gone, not emitted alongside.
	if strings.Contains(out, "--ctx-size 32768") {
		t.Errorf("preset ctx-size should have been overridden:\n%s", out)
	}
}

// TestCmdServe_DerivesFromOutfit covers serving with no PRESET: the command is
// built from MODEL (an HF repo), ALIAS, CONTEXT, and BASEURL.
func TestCmdServe_DerivesFromOutfit(t *testing.T) {
	dir := t.TempDir()
	outfitPath := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitPath, "PROVIDER llamacpp\nMODEL unsloth/Qwen:Q4_K_M\nALIAS qwen\nCONTEXT 32k\nBASEURL http://127.0.0.1:9090/v1\n")

	out := captureStdout(t, func() {
		if err := cmdServe([]string{"--dry-run", outfitPath}); err != nil {
			t.Fatalf("cmdServe: %v", err)
		}
	})
	for _, want := range []string{
		"--hf-repo unsloth/Qwen:Q4_K_M",
		"--alias qwen",
		"--ctx-size 32000",
		"--host 127.0.0.1",
		"--port 9090",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("derived command missing %q:\n%s", want, out)
		}
	}
}

// TestCmdServe_DerivesGGUFPath checks that a .gguf MODEL becomes --model, not
// --hf-repo.
func TestCmdServe_DerivesGGUFPath(t *testing.T) {
	dir := t.TempDir()
	outfitPath := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitPath, "PROVIDER llamacpp\nMODEL ./models/qwen.gguf\n")

	out := captureStdout(t, func() {
		if err := cmdServe([]string{"--dry-run", outfitPath}); err != nil {
			t.Fatalf("cmdServe: %v", err)
		}
	})
	if !strings.Contains(out, "--model ./models/qwen.gguf") {
		t.Errorf("expected --model for a .gguf path:\n%s", out)
	}
	if strings.Contains(out, "--hf-repo") {
		t.Errorf("a .gguf path should not become --hf-repo:\n%s", out)
	}
}

// TestCmdServe_DerivesSchemelessBaseURL checks a BASEURL with no scheme still
// yields a host and port.
func TestCmdServe_DerivesSchemelessBaseURL(t *testing.T) {
	dir := t.TempDir()
	outfitPath := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitPath, "PROVIDER llamacpp\nMODEL org/model\nBASEURL localhost:9090\n")

	out := captureStdout(t, func() {
		if err := cmdServe([]string{"--dry-run", outfitPath}); err != nil {
			t.Fatalf("cmdServe: %v", err)
		}
	})
	if !strings.Contains(out, "--host localhost") || !strings.Contains(out, "--port 9090") {
		t.Errorf("scheme-less BASEURL not parsed:\n%s", out)
	}
}

func TestCmdServe_DerivesBadContext(t *testing.T) {
	dir := t.TempDir()
	outfitPath := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitPath, "PROVIDER llamacpp\nMODEL org/model\nCONTEXT not-a-number\n")
	if err := cmdServe([]string{"--dry-run", outfitPath}); err == nil {
		t.Error("expected error for an unparseable CONTEXT")
	}
}

// TestCmdServe_VllmDerivesBadContext is llama.cpp's bad-CONTEXT guard for the
// other engine that has a context flag: an unparseable CONTEXT fails rather
// than quietly launching vLLM with its own default window, which would serve
// happily at a size the harness was never told about.
func TestCmdServe_VllmDerivesBadContext(t *testing.T) {
	dir := t.TempDir()
	outfitPath := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitPath, "PROVIDER vllm\nMODEL org/model\nCONTEXT not-a-number\n")
	err := cmdServe([]string{"--dry-run", outfitPath})
	if err == nil {
		t.Fatal("expected an error for an unparseable CONTEXT")
	}
	if !strings.Contains(err.Error(), "not-a-number") {
		t.Errorf("error should name the offending value, got: %v", err)
	}
}

// TestCmdServe_ParallelScalesContext checks the practical case this capability
// exists for: CONTEXT is the context a single request should get, so with
// PARALLEL slots llama.cpp's --ctx-size (a total budget it divides across
// slots) must be scaled up to compensate.
func TestCmdServe_ParallelScalesContext(t *testing.T) {
	dir := t.TempDir()
	outfitPath := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitPath, "PROVIDER llamacpp\nMODEL org/model\nCONTEXT 128k\nPARALLEL 2\n")

	out := captureStdout(t, func() {
		if err := cmdServe([]string{"--dry-run", outfitPath}); err != nil {
			t.Fatalf("cmdServe: %v", err)
		}
	})
	for _, want := range []string{"--ctx-size 256000", "--parallel 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("command missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "--ctx-size 128000") {
		t.Errorf("ctx-size should have been scaled by PARALLEL, not left at CONTEXT's raw value:\n%s", out)
	}
}

// TestCmdServe_ParallelWithNoContext checks PARALLEL alone: --parallel is
// emitted, and with no CONTEXT stated there is nothing to scale.
func TestCmdServe_ParallelWithNoContext(t *testing.T) {
	dir := t.TempDir()
	outfitPath := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitPath, "PROVIDER llamacpp\nMODEL org/model\nPARALLEL 4\n")

	out := captureStdout(t, func() {
		if err := cmdServe([]string{"--dry-run", outfitPath}); err != nil {
			t.Fatalf("cmdServe: %v", err)
		}
	})
	if !strings.Contains(out, "--parallel 4") {
		t.Errorf("command missing --parallel 4:\n%s", out)
	}
	if strings.Contains(out, "--ctx-size") {
		t.Errorf("no CONTEXT stated should mean no --ctx-size flag:\n%s", out)
	}
}

// TestCmdServe_ContextWithNoParallelIsUnscaled checks that CONTEXT alone,
// exactly as before PARALLEL existed, is passed straight through.
func TestCmdServe_ContextWithNoParallelIsUnscaled(t *testing.T) {
	dir := t.TempDir()
	outfitPath := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitPath, "PROVIDER llamacpp\nMODEL org/model\nCONTEXT 128k\n")

	out := captureStdout(t, func() {
		if err := cmdServe([]string{"--dry-run", outfitPath}); err != nil {
			t.Fatalf("cmdServe: %v", err)
		}
	})
	if !strings.Contains(out, "--ctx-size 128000") {
		t.Errorf("command missing unscaled --ctx-size 128000:\n%s", out)
	}
	if strings.Contains(out, "--parallel") {
		t.Errorf("no PARALLEL stated should mean no --parallel flag:\n%s", out)
	}
}

// TestCmdServe_ParallelDoesNotRescalePresetContext documents the non-goal in
// design.md: when CONTEXT comes only from a PRESET's own ctx-size (the Outfit
// states none), PARALLEL still emits --parallel but does not retroactively
// scale the preset's value.
func TestCmdServe_ParallelDoesNotRescalePresetContext(t *testing.T) {
	outfitPath := writePresetOutfit(t, "PROVIDER llamacpp\nALIAS qwen\nPARALLEL 2\nPRESET preset.ini\n")

	out := captureStdout(t, func() {
		if err := cmdServe([]string{"--dry-run", outfitPath}); err != nil {
			t.Fatalf("cmdServe: %v", err)
		}
	})
	if !strings.Contains(out, "--parallel 2") {
		t.Errorf("command missing --parallel 2:\n%s", out)
	}
	// The preset's section sets ctx-size = 32768; PARALLEL must not scale it
	// since the Outfit itself states no CONTEXT.
	if !strings.Contains(out, "--ctx-size 32768") {
		t.Errorf("preset ctx-size should pass through unscaled:\n%s", out)
	}
	if strings.Contains(out, "--ctx-size 65536") {
		t.Errorf("PARALLEL must not rescale a preset-only ctx-size:\n%s", out)
	}
}

// TestCmdServe_ParallelOverridesPresetValue checks an Outfit's PARALLEL wins
// over a preset's own np/parallel, the same override-by-canonical-name rule
// CONTEXT already exercises against a preset's ctx-size.
func TestCmdServe_ParallelOverridesPresetValue(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "preset.ini"), "[qwen]\nhf = org/model\nnp = 4\n")
	outfitPath := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitPath, "PROVIDER llamacpp\nALIAS qwen\nPARALLEL 2\nPRESET preset.ini\n")

	out := captureStdout(t, func() {
		if err := cmdServe([]string{"--dry-run", outfitPath}); err != nil {
			t.Fatalf("cmdServe: %v", err)
		}
	})
	if !strings.Contains(out, "--parallel 2") {
		t.Errorf("Outfit PARALLEL should win, missing --parallel 2:\n%s", out)
	}
	if strings.Contains(out, "--parallel 4") {
		t.Errorf("preset's np=4 should have been overridden:\n%s", out)
	}
}

// TestCmdServe_InvalidParallel checks a non-positive or non-numeric PARALLEL
// fails rather than being passed to the engine, for all three engines.
func TestCmdServe_InvalidParallel(t *testing.T) {
	for _, provider := range []string{"llamacpp", "vllm", "omlx"} {
		for _, bad := range []string{"0", "-1", "abc"} {
			t.Run(provider+"/"+bad, func(t *testing.T) {
				dir := t.TempDir()
				outfitPath := filepath.Join(dir, "Outfit")
				body := "PROVIDER " + provider + "\nPARALLEL " + bad + "\n"
				if provider != "omlx" {
					body = "PROVIDER " + provider + "\nMODEL org/model\nPARALLEL " + bad + "\n"
				}
				mustWrite(t, outfitPath, body)
				if err := cmdServe([]string{"--dry-run", outfitPath}); err == nil {
					t.Errorf("expected error for PARALLEL %q", bad)
				}
			})
		}
	}
}

// TestCmdServe_OMLXParallel checks PARALLEL maps to oMLX's own concurrency
// flag, with no context flag either way.
func TestCmdServe_OMLXParallel(t *testing.T) {
	dir := t.TempDir()
	outfitPath := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitPath, "PROVIDER omlx\nPARALLEL 8\n")

	out := captureStdout(t, func() {
		if err := cmdServe([]string{"--dry-run", outfitPath}); err != nil {
			t.Fatalf("cmdServe: %v", err)
		}
	})
	if !strings.Contains(out, "--max-concurrent-requests 8") {
		t.Errorf("command missing --max-concurrent-requests 8:\n%s", out)
	}
	if strings.Contains(out, "--ctx-size") || strings.Contains(out, "--parallel ") {
		t.Errorf("oMLX should use its own flag, not another engine's:\n%s", out)
	}
}

func TestCmdServe_NoPresetNoModel(t *testing.T) {
	dir := t.TempDir()
	outfitPath := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitPath, "PROVIDER llamacpp\n")
	if err := cmdServe([]string{"--dry-run", outfitPath}); err == nil {
		t.Error("expected error when there is neither a PRESET nor a MODEL")
	}
}

func TestCmdServe_LlamaServerNotFound(t *testing.T) {
	orig := llamaServerBinary
	llamaServerBinary = filepath.Join(t.TempDir(), "definitely-not-installed")
	t.Cleanup(func() { llamaServerBinary = orig })
	outfitPath := writePresetOutfit(t, "PROVIDER llamacpp\nALIAS qwen\nPRESET preset.ini\n")

	var err error
	captureStdout(t, func() { err = cmdServe([]string{outfitPath}) })
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected a not-found error, got %v", err)
	}
}

func TestCmdServe_LlamaServerExitsNonZero(t *testing.T) {
	script := filepath.Join(t.TempDir(), "llama-server")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := llamaServerBinary
	llamaServerBinary = script
	t.Cleanup(func() { llamaServerBinary = orig })
	outfitPath := writePresetOutfit(t, "PROVIDER llamacpp\nALIAS qwen\nPRESET preset.ini\n")

	var err error
	captureStdout(t, func() { err = cmdServe([]string{outfitPath}) })
	if err == nil {
		t.Error("expected an error when llama-server exits non-zero")
	}
}

func TestCmdServe_MissingPresetFile(t *testing.T) {
	dir := t.TempDir()
	outfitPath := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitPath, "PROVIDER llamacpp\nALIAS qwen\nPRESET nope.ini\n")
	if err := cmdServe([]string{outfitPath}); err == nil {
		t.Error("expected error when the preset file is missing")
	}
}

func TestCmdServe_DefaultFileMissing(t *testing.T) {
	t.Chdir(t.TempDir()) // a directory with no Outfit
	if err := cmdServe(nil); err == nil {
		t.Error("expected error when ./Outfit is missing")
	}
}

// TestCmdServe_RelativePresetResolvesToOutfitDir checks that a relative PRESET
// is resolved against the Outfit's directory, not the working directory.
func TestCmdServe_RelativePresetResolvesToOutfitDir(t *testing.T) {
	outfitPath := writePresetOutfit(t, "PROVIDER llamacpp\nALIAS qwen\nPRESET preset.ini\n")
	t.Chdir(t.TempDir()) // a different working directory

	out := captureStdout(t, func() {
		if err := cmdServe([]string{"--dry-run", outfitPath}); err != nil {
			t.Fatalf("cmdServe from a different cwd: %v", err)
		}
	})
	if !strings.Contains(out, "--hf-repo unsloth/Qwen:Q4_K_M") {
		t.Errorf("preset not resolved relative to the Outfit dir:\n%s", out)
	}
}

// TestCmdServe_OMLXDryRun pins the whole oMLX shape: the `serve` subcommand, and
// a bind address taken from BASEURL.
func TestCmdServe_OMLXDryRun(t *testing.T) {
	dir := t.TempDir()
	outfitPath := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitPath, "PROVIDER omlx\nBASEURL http://127.0.0.1:9100/v1\n")

	out := captureStdout(t, func() {
		if err := cmdServe([]string{"--dry-run", outfitPath}); err != nil {
			t.Fatalf("cmdServe: %v", err)
		}
	})

	for _, want := range []string{"serve", "--host 127.0.0.1", "--port 9100"} {
		if !strings.Contains(out, want) {
			t.Errorf("command missing %q:\n%s", want, out)
		}
	}
	// llama.cpp's vocabulary must not leak into another engine's command.
	for _, unwanted := range []string{"llama-server", "--hf-repo", "--ctx-size"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("command should not contain %q:\n%s", unwanted, out)
		}
	}
}

// TestCmdServe_OMLXNeedsNoModel checks that oMLX starts with neither a PRESET
// nor a MODEL: it serves a whole model directory and picks per request, so
// llama.cpp's "needs a PRESET or a MODEL" rule must not apply to it.
func TestCmdServe_OMLXNeedsNoModel(t *testing.T) {
	dir := t.TempDir()
	outfitPath := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitPath, "PROVIDER omlx\n")

	out := captureStdout(t, func() {
		if err := cmdServe([]string{"--dry-run", outfitPath}); err != nil {
			t.Fatalf("cmdServe: %v", err)
		}
	})

	if !strings.Contains(out, "serve") {
		t.Errorf("expected a serve command:\n%s", out)
	}
	// With no BASEURL, oMLX's own defaults stand.
	if strings.Contains(out, "--host") || strings.Contains(out, "--port") {
		t.Errorf("no BASEURL should mean no bind flags:\n%s", out)
	}
}

// TestCmdServe_OMLXPresetKeysAreNotAliased is the guard on the dialect split: an
// oMLX preset is read in oMLX's vocabulary, so a key llama.cpp would rewrite is
// left exactly as written.
func TestCmdServe_OMLXPresetKeysAreNotAliased(t *testing.T) {
	outfitPath := writeOMLXOutfit(t, "PROVIDER omlx\nALIAS qwen\nPRESET preset.ini\n")

	out := captureStdout(t, func() {
		if err := cmdServe([]string{"--dry-run", outfitPath}); err != nil {
			t.Fatalf("cmdServe: %v", err)
		}
	})

	for _, want := range []string{"--model-dir /Users/me/models", "--memory-guard safe", "-m should-stay-literal"} {
		if !strings.Contains(out, want) {
			t.Errorf("command missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "--model should-stay-literal") {
		t.Errorf("llama.cpp aliasing leaked into the oMLX dialect:\n%s", out)
	}
}

// TestCmdServe_OMLXOutfitOverridesPreset checks the Outfit still wins over its
// preset for the settings it states, as it does for llama.cpp.
func TestCmdServe_OMLXOutfitOverridesPreset(t *testing.T) {
	outfitPath := writeOMLXOutfit(t, "PROVIDER omlx\nALIAS qwen\nPRESET preset.ini\nBASEURL http://0.0.0.0:9999/v1\n")

	out := captureStdout(t, func() {
		if err := cmdServe([]string{"--dry-run", outfitPath}); err != nil {
			t.Fatalf("cmdServe: %v", err)
		}
	})

	if !strings.Contains(out, "--host 0.0.0.0") || !strings.Contains(out, "--port 9999") {
		t.Errorf("Outfit should override the preset's bind address:\n%s", out)
	}
	if strings.Contains(out, "--port 8000") {
		t.Errorf("preset port should have been overridden:\n%s", out)
	}
}

// TestCmdServe_OMLXNeverPassesAPIKey is a security guard: serve prints the
// command it runs, and oMLX takes its key on the command line, so a resolved
// secret must never reach either.
func TestCmdServe_OMLXNeverPassesAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-must-not-appear")
	dir := t.TempDir()
	outfitPath := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitPath, "PROVIDER omlx\nBASEURL http://127.0.0.1:9100/v1\n")

	out := captureStdout(t, func() {
		if err := cmdServe([]string{"--dry-run", outfitPath}); err != nil {
			t.Fatalf("cmdServe: %v", err)
		}
	})

	if strings.Contains(out, "sk-must-not-appear") || strings.Contains(out, "--api-key") {
		t.Errorf("serve must not pass or print an API key:\n%s", out)
	}
}

// TestCmdServe_OMLXRuns checks the stubbed binary is actually executed with the
// subcommand first.
func TestCmdServe_OMLXRuns(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args")
	stubOMLX(t, argsFile)
	dir := t.TempDir()
	outfitPath := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitPath, "PROVIDER omlx\nBASEURL http://127.0.0.1:9100/v1\n")

	captureStdout(t, func() {
		if err := cmdServe([]string{outfitPath}); err != nil {
			t.Fatalf("cmdServe: %v", err)
		}
	})

	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("reading recorded args: %v", err)
	}
	args := strings.Fields(string(got))
	if len(args) == 0 || args[0] != "serve" {
		t.Errorf("expected the serve subcommand first, got %v", args)
	}
	if !strings.Contains(string(got), "9100") {
		t.Errorf("recorded args missing the port:\n%s", got)
	}
}

// TestCmdServe_OMLXNotFound checks the install hint names oMLX rather than
// llama.cpp.
func TestCmdServe_OMLXNotFound(t *testing.T) {
	orig := omlxBinary
	omlxBinary = filepath.Join(t.TempDir(), "definitely-not-installed")
	t.Cleanup(func() { omlxBinary = orig })
	dir := t.TempDir()
	outfitPath := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitPath, "PROVIDER omlx\n")

	var err error
	captureStdout(t, func() { err = cmdServe([]string{outfitPath}) })
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected a not-found error, got %v", err)
	}
	if !strings.Contains(err.Error(), "oMLX") {
		t.Errorf("hint should name oMLX, got %v", err)
	}
	if strings.Contains(err.Error(), "llama.cpp") {
		t.Errorf("hint should not name llama.cpp, got %v", err)
	}
}

// TestCmdServe_UnsupportedProvider pins the behaviour change: serve used to run
// llama-server whatever the PROVIDER said, which quietly served the wrong engine.
func TestCmdServe_UnsupportedProvider(t *testing.T) {
	dir := t.TempDir()
	outfitPath := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitPath, "PROVIDER ollama\nMODEL llama3.2\n")

	var err error
	captureStdout(t, func() { err = cmdServe([]string{"--dry-run", outfitPath}) })
	if err == nil {
		t.Fatal("expected an error for a provider that is not a local engine")
	}
	for _, want := range []string{"ollama", "llamacpp", "omlx"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got %v", want, err)
		}
	}
}
