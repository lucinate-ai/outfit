package cloud

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/spinloop-ai/spinloop/internal/config"
)

// ConfigHome returns spinloop's own config directory, where both the legacy
// cloud.json and the environments registry live. It delegates to
// internal/config.Dir, so the SPINLOOP_CONFIG_DIR override and the fallback
// rules are resolved in one place; it fails when the directory cannot be
// determined (see config.Dir).
func ConfigHome() (string, error) {
	return config.Dir()
}

// remotesRoot is the environments registry directory: one subdirectory per
// named environment, each holding a cloud.json.
func remotesRoot() (string, error) {
	home, err := ConfigHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "clouds"), nil
}

// EnvDir returns an environment's directory, <config-dir>/clouds/<name>. A
// cloud deployment's state (currently just cloud.json) lives here, keyed by
// name so several instances never share a file.
func EnvDir(name string) (string, error) {
	root, err := remotesRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, name), nil
}

// EnvConfigPath returns the cloud.json inside an environment's directory.
func EnvConfigPath(name string) (string, error) {
	dir, err := EnvDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cloud.json"), nil
}

// IsEnvName reports whether a value is a plain environment name: non-empty,
// with no path separator and no .json suffix. Environments are selected by
// name from the registry, never by path, so a --env value (or a fleet node
// name) that is not a plain identifier is rejected.
func IsEnvName(value string) bool {
	if value == "" {
		return false
	}
	if strings.ContainsAny(value, `/\`) {
		return false
	}
	if strings.HasSuffix(value, ".json") {
		return false
	}
	return true
}

// EnvInfo describes one registered environment for listing. OK is false when
// the environment's cloud.json is missing or unreadable.
type EnvInfo struct {
	Name    string
	BaseURL string
	Region  string
	OK      bool
}

// ListEnvironments returns the registered environments, sorted by name. An
// absent registry is not an error — it yields no environments. Each entry's
// cloud.json is read best-effort: a directory without a readable one is still
// listed, with OK false, rather than failing the whole listing.
func ListEnvironments() ([]EnvInfo, error) {
	root, err := remotesRoot()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var envs []EnvInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info := EnvInfo{Name: e.Name()}
		envPath, err := EnvConfigPath(e.Name())
		if err != nil {
			return nil, err
		}
		if data, err := os.ReadFile(envPath); err == nil {
			var cfg Config
			if json.Unmarshal(data, &cfg) == nil {
				info.BaseURL, info.Region, info.OK = cfg.BaseURL, cfg.Region, true
			}
		}
		envs = append(envs, info)
	}
	return envs, nil
}

// SaveEnvironment registers a deployed environment: its cloud.json (the
// shared control URLs, region, base URL, and the environment identifier) is
// written under the registry, owner-only, since it names a deployment's URLs
// and address. Registering a second environment never touches the first.
func SaveEnvironment(name string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	dir, err := EnvDir(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "cloud.json"), append(data, '\n'), 0o600)
}
