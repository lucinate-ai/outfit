package harness

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePrecedence(t *testing.T) {
	// Isolate preference and env from the developer's real config.
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv(HarnessEnv, "")

	// Default when nothing is set.
	h, source, err := Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if h.Name() != "opencode" || source != "default" {
		t.Errorf("default resolve = %s (%s), want opencode (default)", h.Name(), source)
	}

	// Stored preference beats the default.
	if err := SavePreference("pi"); err != nil {
		t.Fatal(err)
	}
	h, source, _ = Resolve("")
	if h.Name() != "pi" || source != "stored preference" {
		t.Errorf("preference resolve = %s (%s), want pi (stored preference)", h.Name(), source)
	}

	// Env beats the stored preference.
	t.Setenv(HarnessEnv, "opencode")
	h, source, _ = Resolve("")
	if h.Name() != "opencode" || source != HarnessEnv {
		t.Errorf("env resolve = %s (%s), want opencode (%s)", h.Name(), source, HarnessEnv)
	}

	// Flag beats everything.
	h, source, _ = Resolve("pi")
	if h.Name() != "pi" || source != "--harness flag" {
		t.Errorf("flag resolve = %s (%s), want pi (--harness flag)", h.Name(), source)
	}

	// Unknown name errors.
	if _, _, err := Resolve("bogus"); err == nil {
		t.Error("expected error for an unknown harness")
	}
}

func TestPreferenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	// No file yet → empty.
	if pref, err := LoadPreference(); err != nil || pref != "" {
		t.Fatalf("LoadPreference (none) = %q, %v; want \"\", nil", pref, err)
	}

	if err := SavePreference("pi"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "outfit", "config.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perms = %o, want 600", perm)
	}
	if pref, _ := LoadPreference(); pref != "pi" {
		t.Errorf("LoadPreference = %q, want pi", pref)
	}

	// Saving an unknown harness is rejected.
	if err := SavePreference("bogus"); err == nil {
		t.Error("expected error saving an unknown harness")
	}
}

func TestNamesAndLookup(t *testing.T) {
	names := Names()
	if len(names) != 2 || names[0] != "opencode" || names[1] != "pi" {
		t.Errorf("Names = %v, want [opencode pi]", names)
	}
	if _, ok := Lookup("pi"); !ok {
		t.Error("Lookup(pi) should succeed")
	}
	if _, ok := Lookup("nope"); ok {
		t.Error("Lookup(nope) should fail")
	}
}
