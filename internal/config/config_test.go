package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// isolate points Path at a temporary directory so tests never touch the real
// config.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return dir
}

// testPath resolves the config file path, failing the test if it cannot.
func testPath(t *testing.T) string {
	t.Helper()
	p, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// readRaw reads the config file as a generic JSON document, for assertions
// about keys this package does not model.
func readRaw(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(testPath(t))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

// TestLoadMissingFile checks that a first run — no config file at all — yields
// an empty File rather than an error, and that the zero value is safe to read.
func TestLoadMissingFile(t *testing.T) {
	isolate(t)

	f, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.Harness != "" {
		t.Errorf("Harness = %q, want empty", f.Harness)
	}
	if _, ok := f.Alias("nope"); ok {
		t.Error("Alias found something in an empty config")
	}
	if names := f.AliasNames(); len(names) != 0 {
		t.Errorf("AliasNames = %v, want none", names)
	}
}

// TestSaveLoadRoundTrip checks that everything the document models survives a
// write and read, and that the file and its directory are written tightly.
func TestSaveLoadRoundTrip(t *testing.T) {
	dir := isolate(t)

	f := &File{Harness: "pi"}
	f.SetAlias("qwen3.6-27b", "/models/qwen/Outfit")
	f.SetAlias("gemma4", "/models/gemma/Outfit")
	if err := f.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Harness != "pi" {
		t.Errorf("Harness = %q, want pi", got.Harness)
	}
	if path, ok := got.Alias("qwen3.6-27b"); !ok || path != "/models/qwen/Outfit" {
		t.Errorf("Alias = %q, %v", path, ok)
	}
	if want := []string{"gemma4", "qwen3.6-27b"}; !reflect.DeepEqual(got.AliasNames(), want) {
		t.Errorf("AliasNames = %v, want %v (sorted)", got.AliasNames(), want)
	}

	info, err := os.Stat(testPath(t))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config file mode = %v, want 0600", perm)
	}
	dirInfo, err := os.Stat(filepath.Join(dir, "outfit"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("config dir mode = %v, want 0700", perm)
	}
}

// TestSaveOmitsEmptyKeys checks that a config holding only aliases does not
// record an empty harness, and vice versa.
func TestSaveOmitsEmptyKeys(t *testing.T) {
	isolate(t)

	f := &File{}
	f.SetAlias("q3", "/models/qwen/Outfit")
	if err := f.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	doc := readRaw(t)
	if _, ok := doc[keyHarness]; ok {
		t.Errorf("harness key written for an alias-only config: %v", doc)
	}

	if err := Update(func(f *File) error {
		f.RemoveAlias("q3")
		f.Harness = "pi"
		return nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	doc = readRaw(t)
	if _, ok := doc[keyAliases]; ok {
		t.Errorf("aliases key kept after the last alias was removed: %v", doc)
	}
}

// TestUpdatePreservesUnknownKeys checks that a read-modify-write keeps
// top-level keys this version does not model, so a newer (or foreign) writer's
// settings are not silently dropped.
func TestUpdatePreservesUnknownKeys(t *testing.T) {
	dir := isolate(t)

	if err := os.MkdirAll(filepath.Join(dir, "outfit"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testPath(t), []byte(`{"harness":"pi","future":{"x":1}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Update(func(f *File) error {
		f.SetAlias("q3", "/models/qwen/Outfit")
		return nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	doc := readRaw(t)
	future, ok := doc["future"].(map[string]any)
	if !ok {
		t.Fatalf("unknown key was dropped: %v", doc)
	}
	if future["x"] != float64(1) {
		t.Errorf("future.x = %v, want 1", future["x"])
	}
	if doc[keyHarness] != "pi" {
		t.Errorf("harness = %v, want pi", doc[keyHarness])
	}
}

// TestUpdateIsReadModifyWrite is the anti-clobber guarantee: two independent
// settings, written one at a time, must both survive.
func TestUpdateIsReadModifyWrite(t *testing.T) {
	isolate(t)

	if err := Update(func(f *File) error { f.Harness = "pi"; return nil }); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := Update(func(f *File) error {
		f.SetAlias("q3", "/models/qwen/Outfit")
		return nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	f, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.Harness != "pi" {
		t.Errorf("Harness = %q, want pi — storing an alias clobbered it", f.Harness)
	}
	if _, ok := f.Alias("q3"); !ok {
		t.Error("alias missing after a second Update")
	}
}

// TestUpdateStopsOnMutateError checks that a mutate that fails writes nothing.
func TestUpdateStopsOnMutateError(t *testing.T) {
	isolate(t)

	wantErr := os.ErrInvalid
	if err := Update(func(f *File) error {
		f.SetAlias("q3", "/models/qwen/Outfit")
		return wantErr
	}); err != wantErr {
		t.Fatalf("Update error = %v, want %v", err, wantErr)
	}
	if _, err := os.Stat(testPath(t)); !os.IsNotExist(err) {
		t.Errorf("config was written despite a mutate error (stat: %v)", err)
	}
}

// TestSetRemoveAlias checks the registry primitives, including SetAlias on the
// nil map a fresh File carries.
func TestSetRemoveAlias(t *testing.T) {
	f := &File{}
	f.SetAlias("q3", "/a/Outfit")
	if path, ok := f.Alias("q3"); !ok || path != "/a/Outfit" {
		t.Errorf("Alias = %q, %v", path, ok)
	}
	if f.RemoveAlias("nope") {
		t.Error("RemoveAlias reported a removal for an unregistered name")
	}
	if !f.RemoveAlias("q3") {
		t.Error("RemoveAlias did not report removing a registered name")
	}
	if _, ok := f.Alias("q3"); ok {
		t.Error("alias survived RemoveAlias")
	}
}

// TestValidAliasName checks that a name can never be confused with a path or a
// flag.
func TestValidAliasName(t *testing.T) {
	bad := []string{"", "a/b", `a\b`, ".", "..", "-x", "--list", "a b", "a\tb"}
	for _, name := range bad {
		if err := ValidAliasName(name); err == nil {
			t.Errorf("ValidAliasName(%q) = nil, want an error", name)
		}
		if NameShaped(name) {
			t.Errorf("NameShaped(%q) = true, want false", name)
		}
	}
	good := []string{"qwen3.6-27b", "gemma_4", "Outfit", "a.b.c"}
	for _, name := range good {
		if err := ValidAliasName(name); err != nil {
			t.Errorf("ValidAliasName(%q) = %v, want nil", name, err)
		}
		if !NameShaped(name) {
			t.Errorf("NameShaped(%q) = false, want true", name)
		}
	}
}

// TestLoadMalformedJSON checks that a corrupt config names itself in the error.
func TestLoadMalformedJSON(t *testing.T) {
	dir := isolate(t)

	if err := os.MkdirAll(filepath.Join(dir, "outfit"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testPath(t), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load()
	if err == nil {
		t.Fatal("expected an error for a malformed config")
	}
	if !strings.Contains(err.Error(), testPath(t)) {
		t.Errorf("error %q does not name the config file", err)
	}
}

// TestLoadWrongAliasType checks that a plausible hand-edit (aliases as a list)
// errors rather than being silently ignored.
func TestLoadWrongAliasType(t *testing.T) {
	dir := isolate(t)

	if err := os.MkdirAll(filepath.Join(dir, "outfit"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testPath(t), []byte(`{"aliases":["q3"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("expected an error for an aliases list")
	}
}

// TestDirResolution covers the precedence and the loud failure of Dir.
func TestDirResolution(t *testing.T) {
	t.Run("override wins verbatim, no outfit segment appended", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "/xdg")
		t.Setenv(DirEnvVar, "/var/lib/outfit")
		got, err := Dir()
		if err != nil {
			t.Fatal(err)
		}
		if got != "/var/lib/outfit" {
			t.Errorf("Dir() = %q, want /var/lib/outfit (verbatim)", got)
		}
	})

	t.Run("XDG default when no override", func(t *testing.T) {
		t.Setenv(DirEnvVar, "")
		t.Setenv("XDG_CONFIG_HOME", "/xdg")
		got, err := Dir()
		if err != nil {
			t.Fatal(err)
		}
		if got != filepath.Join("/xdg", "outfit") {
			t.Errorf("Dir() = %q, want /xdg/outfit", got)
		}
	})

	t.Run("home default when neither is set", func(t *testing.T) {
		t.Setenv(DirEnvVar, "")
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("HOME", "/home/someone")
		got, err := Dir()
		if err != nil {
			t.Fatal(err)
		}
		if got != filepath.Join("/home/someone", ".config", "outfit") {
			t.Errorf("Dir() = %q, want /home/someone/.config/outfit", got)
		}
	})

	t.Run("fails loudly naming the override when no home resolves", func(t *testing.T) {
		t.Setenv(DirEnvVar, "")
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("HOME", "")
		_, err := Dir()
		if err == nil {
			t.Fatal("Dir() with no override/XDG/HOME did not error")
		}
		if !strings.Contains(err.Error(), DirEnvVar) {
			t.Errorf("error %q does not name %s", err, DirEnvVar)
		}
	})
}
