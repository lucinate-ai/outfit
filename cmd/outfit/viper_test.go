package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucinate-ai/outfit/internal/remote"
)

// TestViperOutfitAliasPrecedence pins the OUTFIT_ALIAS resolution through the
// CLI's one Viper: unset falls through, a registered name resolves to its
// Outfit, and a name the registry does not hold errors naming the variable.
func TestViperOutfitAliasPrecedence(t *testing.T) {
	isolateConfig(t)

	t.Setenv("OUTFIT_ALIAS", "")
	if name, path, err := outfitFromEnv(); name != "" || path != "" || err != nil {
		t.Errorf("unset OUTFIT_ALIAS: got (%q, %q, %v), want all empty", name, path, err)
	}

	aliasFor(t, "q3", "gemma")
	t.Setenv("OUTFIT_ALIAS", "q3")
	name, path, err := outfitFromEnv()
	if err != nil {
		t.Fatalf("OUTFIT_ALIAS=q3: %v", err)
	}
	if name != "q3" || path == "" {
		t.Errorf("OUTFIT_ALIAS=q3: got (%q, %q), want the name and its Outfit path", name, path)
	}

	t.Setenv("OUTFIT_ALIAS", "ghost")
	if _, _, err := outfitFromEnv(); err == nil || !strings.Contains(err.Error(), "OUTFIT_ALIAS") {
		t.Errorf("unregistered OUTFIT_ALIAS: %v, want an error naming the variable", err)
	}
}

// TestViperDefaultOutfitNamed pins that the gate and the reader count the same
// default: the variable counts as well as a ./Outfit, and neither alone
// invents one.
func TestViperDefaultOutfitNamed(t *testing.T) {
	isolateConfig(t)
	t.Chdir(t.TempDir())

	t.Setenv("OUTFIT_ALIAS", "")
	if defaultOutfitNamed() {
		t.Error("no variable and no ./Outfit: the gate reported a default")
	}

	t.Setenv("OUTFIT_ALIAS", "q3")
	if !defaultOutfitNamed() {
		t.Error("the variable names a default: the gate must count it")
	}

	t.Setenv("OUTFIT_ALIAS", "")
	mustWrite(t, "Outfit", "PROVIDER llamacpp\nMODEL gemma\n")
	if !defaultOutfitNamed() {
		t.Error("./Outfit present: the gate must count it")
	}
}

// TestViperRemoteEnvPrecedence pins, for every OUTFIT_REMOTE_* variable, the
// resolution the CLI's Viper gives: an exported variable beats the same key in
// the remote config file, and an unset variable falls through to the file. No
// control call is made — only the Config the commands would take is asserted.
func TestViperRemoteEnvPrecedence(t *testing.T) {
	isolateConfig(t)
	t.Chdir(t.TempDir()) // no ./Outfit, so the per-user file is consulted
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
		"OUTFIT_REMOTE_START_URL":  func(c remote.Config) string { return c.StartURL },
		"OUTFIT_REMOTE_STOP_URL":   func(c remote.Config) string { return c.StopURL },
		"OUTFIT_REMOTE_DEPLOY_URL": func(c remote.Config) string { return c.DeployURL },
		"OUTFIT_REMOTE_STATS_URL":  func(c remote.Config) string { return c.StatsURL },
		"OUTFIT_REMOTE_ENV_URL":    func(c remote.Config) string { return c.EnvURL },
		"OUTFIT_REMOTE_UPDATE_URL": func(c remote.Config) string { return c.UpdateURL },
		"OUTFIT_REMOTE_REGION":     func(c remote.Config) string { return c.Region },
	}

	// Unset variables fall through to the file.
	cfg, err := resolveRemoteConfig("")
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
		cfg, err := resolveRemoteConfig("")
		if err != nil {
			t.Fatalf("%s set: %v", name, err)
		}
		if got := get(cfg); got != envValue {
			t.Errorf("%s set: resolved %q, want %q", name, got, envValue)
		}
		t.Setenv(name, "")
	}
}
