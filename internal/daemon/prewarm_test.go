//go:build !windows

package daemon

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/lucinate-ai/outfit/internal/remote"
)

func TestModelFilesNamesThePathItself(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "model.gguf")
	if err := os.WriteFile(file, []byte("weights"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := modelFiles(file); len(got) != 1 || got[0] != file {
		t.Fatalf("modelFiles(file) = %v, want [%s]", got, file)
	}
}

func TestModelFilesListsRegularFilesUnderADirectory(t *testing.T) {
	dir := t.TempDir()
	write := func(rel string, size int) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel), make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("shard-00001.safetensors", 4)
	write("nested/shard-00002.safetensors", 4)
	if err := os.MkdirAll(filepath.Join(dir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := modelFiles(dir)
	want := []string{
		filepath.Join(dir, "nested", "shard-00002.safetensors"),
		filepath.Join(dir, "shard-00001.safetensors"),
	}
	if len(got) != len(want) {
		t.Fatalf("modelFiles(dir) = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("modelFiles(dir) = %v, want %v", got, want)
		}
	}
}

func TestModelFilesOfAMissingPathIsEmpty(t *testing.T) {
	if got := modelFiles(filepath.Join(t.TempDir(), "absent")); got != nil {
		t.Fatalf("modelFiles(absent) = %v, want nil", got)
	}
}

// TestPrewarmPathReadsThrough proves the pass consumes its input to EOF: a
// fifo's writer can only finish once the reader has taken every byte.
func TestPrewarmPathReadsThrough(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("no fifo support: %v", err)
	}
	done := make(chan struct{})
	go func() {
		prewarmPath(path)
		close(done)
	}()
	w, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 8<<20)
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	w.Close()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("prewarmPath did not finish after the writer closed")
	}
}

func TestStartEnginePrewarmsTheStoredModel(t *testing.T) {
	var warmed []string
	d := testDaemon(t, "exit 0")
	d.Prewarm = func(modelPath string) { warmed = append(warmed, modelPath) }

	model := filepath.Join(t.TempDir(), "model.gguf")
	if err := d.Push(remote.DeployConfig{Runner: "llamacpp", ModelID: model}); err != nil {
		t.Fatal(err)
	}
	if err := d.StartEngine(); err != nil {
		t.Fatal(err)
	}
	if len(warmed) != 1 || warmed[0] != model {
		t.Fatalf("prewarm calls = %v, want one of %s", warmed, model)
	}
}

func TestStartEngineWithoutAStoredModelPrewarmsNothing(t *testing.T) {
	var warmed []string
	d := testDaemon(t, "exit 0")
	d.Prewarm = func(modelPath string) { warmed = append(warmed, modelPath) }

	// No stored config: the start fails before anything is warmed.
	if err := d.StartEngine(); err == nil {
		t.Fatal("start with nothing stored succeeded")
	}
	// A stored config with no model: served, but nothing to warm.
	if err := d.Push(remote.DeployConfig{Runner: "llamacpp"}); err != nil {
		t.Fatal(err)
	}
	if err := d.StartEngine(); err != nil {
		t.Fatal(err)
	}
	if len(warmed) != 0 {
		t.Fatalf("prewarm calls = %v, want none", warmed)
	}
}

// Guard against a regression that makes the warm path block the start: the
// default prewarmModel must return immediately no matter what it is given.
func TestPrewarmModelNeverBlocks(t *testing.T) {
	start := time.Now()
	prewarmModel(filepath.Join(t.TempDir(), "absent"))
	prewarmModel(t.TempDir())
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("prewarmModel took %s; it must not delay a start", elapsed)
	}
}
