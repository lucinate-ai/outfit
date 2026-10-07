package cloud

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsEnvName(t *testing.T) {
	cases := map[string]bool{
		"qwen3.6-27b-prod": true,
		"default":          true,
		"qwen3.6":          true, // a dot that is not a .json suffix
		"./cloud.json":     false,
		"/abs/cloud.json":  false,
		"clouds/x":         false,
		"cloud.json":       false,
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
	want := filepath.Join(home, "spinloop", "clouds", "prod", "cloud.json")
	if got := must1(EnvConfigPath("prod")); got != want {
		t.Errorf("EnvConfigPath = %q, want %q", got, want)
	}
}

// writeEnv registers an environment's cloud.json for a test.
func writeEnv(t *testing.T, name, body string) {
	t.Helper()
	if err := os.MkdirAll(must1(EnvDir(name)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(must1(EnvConfigPath(name)), []byte(body), 0o600); err != nil {
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
	// A directory with an unreadable/invalid cloud.json is listed, not fatal.
	if err := os.MkdirAll(must1(EnvDir("broken")), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(must1(EnvConfigPath("broken")), []byte("not json"), 0o600); err != nil {
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

func TestSaveEnvironment(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := Config{
		StartURL: "https://s", StopURL: "https://x", DeployURL: "https://d",
		Region: "us-east-1", BaseURL: "http://1.2.3.4:8000/v1", Environment: "prod",
	}
	if err := SaveEnvironment("prod", cfg); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(must1(EnvConfigPath("prod")))
	if err != nil {
		t.Fatal(err)
	}
	// Owner-only: the file names a deployment's URLs and address.
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("cloud.json mode = %v, want 0600", fi.Mode().Perm())
	}
	// Round-trips through the loader, environment identifier included.
	got, err := LoadEnvironment("prod", func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if got.Environment != "prod" || got.BaseURL != cfg.BaseURL || got.DeployURL != "https://d" {
		t.Errorf("round-trip = %+v", got)
	}

	// A second environment leaves the first intact.
	if err := SaveEnvironment("staging", Config{StartURL: "https://s2", StopURL: "https://x2", Region: "us-east-1", Environment: "staging"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(must1(EnvConfigPath("prod"))); err != nil {
		t.Error("registering a second environment must not touch the first")
	}
}

func TestLoadEnvironment_ByName(t *testing.T) {
	getenv := func(string) string { return "" }
	cfg := `{"start_url":"https://s","stop_url":"https://x","region":"eu-west-1"}`

	t.Run("reads the named environment", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		writeEnv(t, "default", cfg)
		got, err := LoadEnvironment("default", getenv)
		if err != nil || got.StartURL != "https://s" {
			t.Fatalf("named env: %+v, %v", got, err)
		}
	})

	// The superseded path is not a second place an environment can live: a
	// file there is simply not read, whatever it holds.
	t.Run("a file at the superseded path is not read", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", home)
		legacy := filepath.Join(home, "spinloop", "cloud.json")
		if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(legacy, []byte(cfg), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadEnvironment("default", getenv); err == nil {
			t.Fatal("the superseded path must not configure an environment")
		}
	})

	t.Run("nothing present reports where to put it", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		_, err := LoadEnvironment("default", getenv)
		if err == nil {
			t.Fatal("expected an error naming the environment's path")
		}
		if !strings.Contains(err.Error(), "clouds/default/cloud.json") {
			t.Errorf("the failure should name the registry path, got %v", err)
		}
	})

	// The documented no-file workflow: the overrides carry the configuration,
	// and the name carries the identifier the control calls need.
	t.Run("overrides configure a named environment with no file", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		got, err := LoadEnvironment("ci", envMap(map[string]string{
			"SPINLOOP_CLOUD_START_URL": "https://s",
			"SPINLOOP_CLOUD_STOP_URL":  "https://x",
			"SPINLOOP_CLOUD_REGION":    "eu-west-1",
		}))
		if err != nil {
			t.Fatalf("overrides should configure it: %v", err)
		}
		if got.Environment != "ci" {
			t.Errorf("Environment = %q, want the name given", got.Environment)
		}
	})
}
