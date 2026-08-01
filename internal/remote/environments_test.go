package remote

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsEnvName(t *testing.T) {
	cases := map[string]bool{
		"qwen3.6-27b-prod": true,
		"default":          true,
		"qwen3.6":          true, // a dot that is not a .json suffix
		"./remote.json":    false,
		"/abs/remote.json": false,
		"remotes/x":        false,
		"remote.json":      false,
		`win\path`:         false,
		"":                 false,
	}
	for value, want := range cases {
		if got := IsEnvName(value); got != want {
			t.Errorf("IsEnvName(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestEnvConfigPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	want := filepath.Join(home, "outfit", "remotes", "prod", "remote.json")
	if got := EnvConfigPath("prod"); got != want {
		t.Errorf("EnvConfigPath = %q, want %q", got, want)
	}
}

// writeEnv registers an environment's remote.json for a test.
func writeEnv(t *testing.T, name, body string) {
	t.Helper()
	if err := os.MkdirAll(EnvDir(name), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(EnvConfigPath(name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestListEnvironments(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// Empty registry is not an error.
	if envs, err := ListEnvironments(); err != nil || len(envs) != 0 {
		t.Fatalf("empty registry: got %v, %v", envs, err)
	}

	writeEnv(t, "prod", `{"start_url":"https://s","stop_url":"https://x","region":"eu-west-1","base_url":"http://1.2.3.4:8000/v1"}`)
	// A directory with an unreadable/invalid remote.json is listed, not fatal.
	if err := os.MkdirAll(EnvDir("broken"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(EnvConfigPath("broken"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	envs, err := ListEnvironments()
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 2 {
		t.Fatalf("got %d environments, want 2: %+v", len(envs), envs)
	}
	// ReadDir sorts by name: "broken" before "prod".
	if envs[0].Name != "broken" || envs[0].OK {
		t.Errorf("broken entry = %+v, want name=broken OK=false", envs[0])
	}
	if envs[1].Name != "prod" || !envs[1].OK || envs[1].Region != "eu-west-1" || envs[1].BaseURL != "http://1.2.3.4:8000/v1" {
		t.Errorf("prod entry = %+v", envs[1])
	}
}

func TestLoadDefault(t *testing.T) {
	getenv := func(string) string { return "" }
	cfg := `{"start_url":"https://s","stop_url":"https://x","region":"eu-west-1"}`

	t.Run("default environment", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		writeEnv(t, "default", cfg)
		got, err := LoadDefault(getenv)
		if err != nil || got.StartURL != "https://s" {
			t.Fatalf("default env: %+v, %v", got, err)
		}
	})

	t.Run("legacy file fallback", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", home)
		if err := os.MkdirAll(filepath.Dir(ConfigPath()), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(ConfigPath(), []byte(cfg), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := LoadDefault(getenv)
		if err != nil || got.Region != "eu-west-1" {
			t.Fatalf("legacy fallback: %+v, %v", got, err)
		}
	})

	t.Run("neither present reports where to put it", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		if _, err := LoadDefault(getenv); err == nil {
			t.Fatal("expected an error naming the default environment")
		}
	})
}
