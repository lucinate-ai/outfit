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

func TestCmdServe_DryRun(t *testing.T) {
	outfitPath := writePresetOutfit(t, "PROVIDER llamacpp\nMODEL qwen\nPRESET preset.ini\n")

	out := captureStdout(t, func() {
		if err := cmdServe([]string{"--dry-run", outfitPath}); err != nil {
			t.Fatalf("cmdServe: %v", err)
		}
	})

	if !strings.Contains(out, "Using preset") || !strings.Contains(out, "preset.ini") {
		t.Errorf("missing preset path in output:\n%s", out)
	}
	if !strings.Contains(out, "Model: qwen") {
		t.Errorf("missing model line:\n%s", out)
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

func TestCmdServe_RunsLlamaServer(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args")
	stubLlamaServer(t, argsFile)
	outfitPath := writePresetOutfit(t, "PROVIDER llamacpp\nMODEL qwen\nPRESET preset.ini\n")

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

func TestCmdServe_LlamaServerNotFound(t *testing.T) {
	orig := llamaServerBinary
	llamaServerBinary = filepath.Join(t.TempDir(), "definitely-not-installed")
	t.Cleanup(func() { llamaServerBinary = orig })
	outfitPath := writePresetOutfit(t, "PROVIDER llamacpp\nMODEL qwen\nPRESET preset.ini\n")

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
	outfitPath := writePresetOutfit(t, "PROVIDER llamacpp\nMODEL qwen\nPRESET preset.ini\n")

	var err error
	captureStdout(t, func() { err = cmdServe([]string{outfitPath}) })
	if err == nil {
		t.Error("expected an error when llama-server exits non-zero")
	}
}

func TestCmdServe_NoPreset(t *testing.T) {
	dir := t.TempDir()
	outfitPath := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitPath, "PROVIDER llamacpp\nMODEL qwen\n")
	if err := cmdServe([]string{outfitPath}); err == nil {
		t.Error("expected error when the Outfit has no PRESET")
	}
}

func TestCmdServe_MissingPresetFile(t *testing.T) {
	dir := t.TempDir()
	outfitPath := filepath.Join(dir, "Outfit")
	mustWrite(t, outfitPath, "PROVIDER llamacpp\nMODEL qwen\nPRESET nope.ini\n")
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
	outfitPath := writePresetOutfit(t, "PROVIDER llamacpp\nMODEL qwen\nPRESET preset.ini\n")
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
