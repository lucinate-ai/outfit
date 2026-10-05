package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spinloop-ai/spinloop/internal/remote"
)

// TestViperSpinloopAliasPrecedence pins the SPINLOOP_ALIAS resolution through the
// CLI's one Viper: unset falls through, a registered name resolves to its
// Spinloop, and a name the registry does not hold errors naming the variable.
func TestViperSpinloopAliasPrecedence(t *testing.T) {
	isolateConfig(t)

	t.Setenv("SPINLOOP_ALIAS", "")
	if name, path, err := spinloopFromEnv(); name != "" || path != "" || err != nil {
		t.Errorf("unset SPINLOOP_ALIAS: got (%q, %q, %v), want all empty", name, path, err)
	}

	aliasFor(t, "q3", "gemma")
	t.Setenv("SPINLOOP_ALIAS", "q3")
	name, path, err := spinloopFromEnv()
	if err != nil {
		t.Fatalf("SPINLOOP_ALIAS=q3: %v", err)
	}
	if name != "q3" || path == "" {
		t.Errorf("SPINLOOP_ALIAS=q3: got (%q, %q), want the name and its Spinloop path", name, path)
	}

	t.Setenv("SPINLOOP_ALIAS", "ghost")
	if _, _, err := spinloopFromEnv(); err == nil || !strings.Contains(err.Error(), "SPINLOOP_ALIAS") {
		t.Errorf("unregistered SPINLOOP_ALIAS: %v, want an error naming the variable", err)
	}
}

// TestViperDefaultSpinloopNamed pins that the gate and the reader count the same
// default: the variable counts as well as a ./Spinloop, and neither alone
// invents one.
func TestViperDefaultSpinloopNamed(t *testing.T) {
	isolateConfig(t)
	t.Chdir(t.TempDir())

	t.Setenv("SPINLOOP_ALIAS", "")
	if defaultSpinloopNamed() {
		t.Error("no variable and no ./Spinloop: the gate reported a default")
	}

	t.Setenv("SPINLOOP_ALIAS", "q3")
	if !defaultSpinloopNamed() {
		t.Error("the variable names a default: the gate must count it")
	}

	t.Setenv("SPINLOOP_ALIAS", "")
	mustWrite(t, "Spinloop", "PROVIDER llamacpp\nMODEL gemma\n")
	if !defaultSpinloopNamed() {
		t.Error("./Spinloop present: the gate must count it")
	}
}

// TestViperRemoteEnvPrecedence pins, for every SPINLOOP_REMOTE_* variable, the
// resolution the CLI's Viper gives: an exported variable beats the same key in
// the remote config file, and an unset variable falls through to the file. No
// control call is made — only the Config the commands would take is asserted.
func TestViperRemoteEnvPrecedence(t *testing.T) {
	isolateConfig(t)
	t.Chdir(t.TempDir()) // no ./Spinloop, so the per-user file is consulted
	stubAWSEnv(t)

	file := remote.Config{
		StartURL:    "https://file.example/start",
		StopURL:     "https://file.example/stop",
		DeployURL:   "https://file.example/deploy",
		StatsURL:    "https://file.example/stats",
		EnvURL:      "https://file.example/env",
		UpdateURL:   "https://file.example/update",
		Region:      "us-east-1",
		Environment: "default",
	}
	path := must1(remote.EnvConfigPath("default"))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	const envValue = "https://env.example/wins"
	legs := map[string]func(remote.Config) string{
		"SPINLOOP_REMOTE_START_URL":    func(c remote.Config) string { return c.StartURL },
		"SPINLOOP_REMOTE_STOP_URL":     func(c remote.Config) string { return c.StopURL },
		"SPINLOOP_REMOTE_DEPLOY_URL":   func(c remote.Config) string { return c.DeployURL },
		"SPINLOOP_REMOTE_STATS_URL":    func(c remote.Config) string { return c.StatsURL },
		"SPINLOOP_REMOTE_ENV_URL":      func(c remote.Config) string { return c.EnvURL },
		"SPINLOOP_REMOTE_UPDATE_URL":   func(c remote.Config) string { return c.UpdateURL },
		"SPINLOOP_REMOTE_SCHEDULE_URL": func(c remote.Config) string { return c.ScheduleURL },
		"SPINLOOP_REMOTE_REGION":       func(c remote.Config) string { return c.Region },
	}

	// Unset variables fall through to the file.
	cfg, err := resolveRemoteConfig("default", "")
	if err != nil {
		t.Fatalf("resolveRemoteConfig: %v", err)
	}
	for name, get := range legs {
		if got := get(cfg); got != get(file) {
			t.Errorf("%s unset: resolved %q, want the file's %q", name, got, get(file))
		}
	}

	// Each exported variable wins over the file, one at a time.
	for name, get := range legs {
		t.Setenv(name, envValue)
		cfg, err := resolveRemoteConfig("default", "")
		if err != nil {
			t.Fatalf("%s set: %v", name, err)
		}
		if got := get(cfg); got != envValue {
			t.Errorf("%s set: resolved %q, want %q", name, got, envValue)
		}
		t.Setenv(name, "")
	}
}
