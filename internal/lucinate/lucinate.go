// Package lucinate reads and writes the lucinate chat client's connections
// store at ~/.lucinate/connections.json, merging a single managed
// OpenAI-compatible connection into it while preserving the rest of the file,
// and reads that state back for export.
//
// lucinate's file is plain JSON of the form
// {"defaultId": "<id>", "connections": [{id, name, type, url, defaultModel, …}]}.
// outfit manages one connection per provider, keyed by a deterministic id
// (outfit:<providerId>), and points defaultId at it so lucinate launches
// straight into the configured model. No API key is written: lucinate reads an
// OpenAI-compatible key from LUCINATE_OPENAI_API_KEY at run time, which
// `outfit harness` supplies — so, as with the other harnesses, no secret lands
// on disk.
package lucinate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DataDirEnv is lucinate's data-directory override, matching lucinate itself.
const DataDirEnv = "LUCINATE_DATA_DIR"

// managedIDPrefix namespaces the connection ids outfit owns, so a re-apply
// updates the same entry, Remove can target it, and State can recover the
// provider id. lucinate's own ids are random hex and contain no colon, so the
// namespaces cannot collide.
const managedIDPrefix = "outfit:"

// connectionType is the lucinate connection type outfit writes: an
// OpenAI-compatible endpoint.
const connectionType = "openai"

// ProviderState is one managed connection read back from the store: its model
// (as a single key) and base URL. A lucinate connection has no fields for
// context or output limits, so those maps are always empty; they exist to match
// the shape the other harness adapters report.
type ProviderState struct {
	ModelKeys []string
	BaseURL   string
	Contexts  map[string]int
	Outputs   map[string]int
}

// Connection is the managed connection outfit writes for a provider.
type Connection struct {
	URL          string
	DefaultModel string
	Name         string
}

// DataDir returns lucinate's data directory: $LUCINATE_DATA_DIR when set,
// otherwise ~/.lucinate (resolved from the home directory, not XDG — matching
// lucinate).
func DataDir() (string, error) {
	if env := os.Getenv(DataDirEnv); env != "" {
		return env, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lucinate"), nil
}

// ConfigPath returns the path to lucinate's connections.json.
func ConfigPath() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "connections.json"), nil
}

// ManagedID returns the deterministic connection id outfit uses for a provider.
func ManagedID(providerID string) string {
	return managedIDPrefix + providerID
}

// providerIDFromManaged returns the provider id encoded in a managed connection
// id, and false when the id is not one outfit owns.
func providerIDFromManaged(id string) (string, bool) {
	if !strings.HasPrefix(id, managedIDPrefix) {
		return "", false
	}
	return strings.TrimPrefix(id, managedIDPrefix), true
}

// load reads connections.json into a generic map, returning an empty object
// when the file does not yet exist so unknown keys round-trip untouched.
func load(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if root == nil {
		root = map[string]any{}
	}
	return root, nil
}

// write marshals root with indentation and 0600 permissions (lucinate's own
// files use 0600), creating the data directory if needed.
func write(path string, root map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// connectionsArray returns root["connections"] as a slice, or nil.
func connectionsArray(root map[string]any) []any {
	a, _ := root["connections"].([]any)
	return a
}

// Write merges the managed connection for providerID into connections.json,
// preserving every other connection and any unknown fields, and points the
// store's defaultId at it. An existing managed connection keeps its creation
// timestamp and any unknown fields; only the fields outfit owns (type, url,
// defaultModel, name) are overwritten.
func Write(providerID string, conn Connection) error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	root, err := load(path)
	if err != nil {
		return err
	}

	id := ManagedID(providerID)
	conns := connectionsArray(root)
	found := false
	for _, raw := range conns {
		el, ok := raw.(map[string]any)
		if !ok || el["id"] != id {
			continue
		}
		applyConnFields(el, id, conn)
		found = true
		break
	}
	if !found {
		el := map[string]any{}
		applyConnFields(el, id, conn)
		conns = append(conns, el)
	}

	root["connections"] = conns
	root["defaultId"] = id
	return write(path, root)
}

// applyConnFields writes the outfit-owned fields onto a connection entry,
// preserving its creation timestamp when present and stamping one when not.
func applyConnFields(el map[string]any, id string, conn Connection) {
	el["id"] = id
	el["type"] = connectionType
	el["url"] = conn.URL
	el["name"] = conn.Name
	if conn.DefaultModel != "" {
		el["defaultModel"] = conn.DefaultModel
	} else {
		delete(el, "defaultModel")
	}
	if _, ok := el["createdAt"]; !ok {
		el["createdAt"] = time.Now().UTC().Format(time.RFC3339)
	}
}

// Remove deletes the managed connection for providerID. With model keys named,
// it removes the connection only when its model is among them (a lucinate
// connection holds exactly one model); with none named, it removes the
// connection outright. When the removed connection was the store's default, the
// defaultId pointer is cleared. Returns the number of connections removed.
func Remove(providerID string, modelKeys []string) (int, error) {
	path, err := ConfigPath()
	if err != nil {
		return 0, err
	}
	root, err := load(path)
	if err != nil {
		return 0, err
	}

	id := ManagedID(providerID)
	conns := connectionsArray(root)
	idx := -1
	var el map[string]any
	for i, raw := range conns {
		m, ok := raw.(map[string]any)
		if ok && m["id"] == id {
			idx, el = i, m
			break
		}
	}
	if idx == -1 {
		return 0, nil
	}
	if len(modelKeys) > 0 {
		model, _ := el["defaultModel"].(string)
		matched := false
		for _, k := range modelKeys {
			if k == model {
				matched = true
				break
			}
		}
		if !matched {
			return 0, nil
		}
	}

	root["connections"] = append(conns[:idx:idx], conns[idx+1:]...)
	if root["defaultId"] == id {
		delete(root, "defaultId")
	}
	return 1, write(path, root)
}

// State reads connections.json and reports each managed connection as a
// provider: its model (from defaultModel) and base URL (from url). Connections
// outfit does not own are ignored. Context and output limits are always empty —
// a lucinate connection cannot hold them.
func State() (map[string]ProviderState, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	root, err := load(path)
	if err != nil {
		return nil, err
	}
	out := map[string]ProviderState{}
	for _, raw := range connectionsArray(root) {
		el, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		idStr, _ := el["id"].(string)
		providerID, ok := providerIDFromManaged(idStr)
		if !ok {
			continue
		}
		st := ProviderState{Contexts: map[string]int{}, Outputs: map[string]int{}}
		st.BaseURL, _ = el["url"].(string)
		if model, _ := el["defaultModel"].(string); model != "" {
			st.ModelKeys = []string{model}
		}
		out[providerID] = st
	}
	return out, nil
}

// DefaultProviderID returns the provider behind the store's default connection,
// when that connection is one outfit manages. It lets the launch path inject the
// right key for the model lucinate will boot into. Returns false when there is
// no default, the file is unreadable, or the default is not an outfit-managed
// connection.
func DefaultProviderID() (string, bool) {
	path, err := ConfigPath()
	if err != nil {
		return "", false
	}
	root, err := load(path)
	if err != nil {
		return "", false
	}
	def, _ := root["defaultId"].(string)
	if def == "" {
		return "", false
	}
	return providerIDFromManaged(def)
}
