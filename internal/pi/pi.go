// Package pi reads and writes the Pi coding agent's model catalogue at
// ~/.pi/agent/models.json, deep-merging a managed provider entry into it while
// preserving the rest of the file, and reads provider state back for export.
//
// Pi's file is plain JSON of the form {"providers": {"<id>": { ... }}}; see
// https://github.com/earendil-works/pi (packages/coding-agent/docs/models.md).
package pi

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/lucinate-ai/outfit/internal/catalog"
)

// ConfigPath returns the path to Pi's models.json (~/.pi/agent/models.json).
// Pi does not follow XDG, so this is resolved from the home directory.
func ConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pi", "agent", "models.json"), nil
}

// ProviderState is one configured provider read back from models.json: its
// model ids (sorted), the base URL, and the per-model contextWindow/maxTokens
// for those that set them. Pi has no top-level default model, so export relies
// on the provider selection alone.
type ProviderState struct {
	ModelKeys []string
	BaseURL   string
	Contexts  map[string]int
	Outputs   map[string]int
}

// load reads models.json into a generic map, returning an empty object when the
// file does not yet exist so unknown keys round-trip untouched.
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

// write marshals root with indentation and 0600 permissions (Pi's own files use
// 0600), creating the ~/.pi/agent directory if needed.
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

// providersMap returns root["providers"] as a map, creating it when absent.
func providersMap(root map[string]any) map[string]any {
	pv, ok := root["providers"].(map[string]any)
	if !ok {
		pv = map[string]any{}
		root["providers"] = pv
	}
	return pv
}

// Write deep-merges a provider entry into models.json. An existing provider's
// unknown fields (headers, compat, modelOverrides, …) are preserved; baseUrl,
// api and apiKey are overwritten when set, and models are merged by id with the
// new entries winning. contextWindow and outputTokens, when > 0, are applied to
// every model the selection writes (as contextWindow and maxTokens).
func Write(id string, prov catalog.PiProvider, contextWindow, outputTokens int) error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	root, err := load(path)
	if err != nil {
		return err
	}

	for i := range prov.Models {
		if contextWindow > 0 {
			prov.Models[i].ContextWindow = contextWindow
		}
		if outputTokens > 0 {
			prov.Models[i].MaxTokens = outputTokens
		}
	}

	// Convert the typed provider to a generic map so we can merge field-by-field.
	incoming, err := toMap(prov)
	if err != nil {
		return err
	}

	pv := providersMap(root)
	existing, _ := pv[id].(map[string]any)
	pv[id] = mergeProvider(existing, incoming)

	return write(path, root)
}

// mergeProvider merges an incoming provider entry onto an existing one. Scalar
// fields from incoming win; the models arrays are unioned by id (incoming wins);
// every other existing key is preserved.
func mergeProvider(existing, incoming map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range existing {
		out[k] = v
	}
	for k, v := range incoming {
		if k == "models" {
			continue
		}
		out[k] = v
	}
	out["models"] = mergeModels(asArray(existing["models"]), asArray(incoming["models"]))
	return out
}

// mergeModels unions two model arrays by their "id", with incoming entries
// replacing existing ones, and returns them sorted by id for a stable file.
func mergeModels(existing, incoming []any) []any {
	byID := map[string]any{}
	order := func(models []any) {
		for _, m := range models {
			if mm, ok := m.(map[string]any); ok {
				if id, ok := mm["id"].(string); ok {
					byID[id] = mm
				}
			}
		}
	}
	order(existing)
	order(incoming)

	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]any, 0, len(ids))
	for _, id := range ids {
		out = append(out, byID[id])
	}
	return out
}

// Remove removes a provider, or specific model ids within it, from models.json.
// With no modelIDs the whole provider entry is removed. Returns the number of
// removals made.
func Remove(id string, modelIDs []string) (int, error) {
	path, err := ConfigPath()
	if err != nil {
		return 0, err
	}
	root, err := load(path)
	if err != nil {
		return 0, err
	}
	pv := providersMap(root)
	prov, ok := pv[id].(map[string]any)
	if !ok {
		return 0, nil
	}

	if len(modelIDs) == 0 {
		delete(pv, id)
		return 1, write(path, root)
	}

	drop := make(map[string]bool, len(modelIDs))
	for _, m := range modelIDs {
		drop[m] = true
	}
	models := asArray(prov["models"])
	kept := make([]any, 0, len(models))
	removed := 0
	for _, m := range models {
		if mm, ok := m.(map[string]any); ok {
			if mid, ok := mm["id"].(string); ok && drop[mid] {
				removed++
				continue
			}
		}
		kept = append(kept, m)
	}
	if removed == 0 {
		return 0, nil
	}
	prov["models"] = kept
	return removed, write(path, root)
}

// State reads models.json and reports each configured provider's state.
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
	pv, _ := root["providers"].(map[string]any)
	for id, raw := range pv {
		prov, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		st := ProviderState{Contexts: map[string]int{}, Outputs: map[string]int{}}
		st.BaseURL, _ = prov["baseUrl"].(string)
		for _, m := range asArray(prov["models"]) {
			mm, ok := m.(map[string]any)
			if !ok {
				continue
			}
			mid, _ := mm["id"].(string)
			if mid == "" {
				continue
			}
			st.ModelKeys = append(st.ModelKeys, mid)
			if c, ok := mm["contextWindow"].(float64); ok && c > 0 {
				st.Contexts[mid] = int(c)
			}
			if o, ok := mm["maxTokens"].(float64); ok && o > 0 {
				st.Outputs[mid] = int(o)
			}
		}
		sort.Strings(st.ModelKeys)
		out[id] = st
	}
	return out, nil
}

// toMap converts a typed PiProvider to a generic map via JSON, honouring the
// struct's omitempty tags so empty fields do not land in the file.
func toMap(prov catalog.PiProvider) (map[string]any, error) {
	data, err := json.Marshal(prov)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if _, ok := m["models"]; !ok {
		m["models"] = []any{}
	}
	return m, nil
}

// asArray coerces a JSON value to a slice, returning nil for anything else.
func asArray(v any) []any {
	a, _ := v.([]any)
	return a
}
