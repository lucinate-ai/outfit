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

// stubLlamaServer points llamaServerBinary at a script that records its argv to
// argsFile, and restores the original binary afterwards.
func stubLlamaServer(t *testing.T, argsFile string) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "llama-server")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := llamaServerBinary
	llamaServerBinary = script
	t.Cleanup(func() { llamaServerBinary = orig })
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

func TestCmdServe_NoPresetNoModel(t *testing.T) {
	dir := t.TempDir()
	outfitPath := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitPath, "PROVIDER llamacpp\nFAMILY local\n")
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
