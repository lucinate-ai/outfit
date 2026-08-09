package lucinate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// readStore reads and unmarshals connections.json for assertions.
func readStore(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("connections.json is not valid JSON: %v", err)
	}
	return m
}

// connByID returns the connection entry with the given id, or nil.
func connByID(root map[string]any, id string) map[string]any {
	conns, _ := root["connections"].([]any)
	for _, raw := range conns {
		if m, ok := raw.(map[string]any); ok && m["id"] == id {
			return m
		}
	}
	return nil
}

func sample() Connection {
	return Connection{
		URL:          "https://openrouter.ai/api/v1",
		DefaultModel: "deepseek/deepseek-v4-pro",
		Name:         "OpenRouter",
	}
}

func TestConfigPath_Home(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(DataDirEnv, "")
	t.Setenv("HOME", dir)
	got, err := ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, ".lucinate", "connections.json"); got != want {
		t.Errorf("ConfigPath = %q, want %q", got, want)
	}
}

func TestConfigPath_DataDirOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(DataDirEnv, dir)
	got, err := ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "connections.json"); got != want {
		t.Errorf("ConfigPath = %q, want %q", got, want)
	}
}

func TestWrite_FreshFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(DataDirEnv, dir)

	if err := Write("openrouter", sample()); err != nil {
		t.Fatalf("Write: %v", err)
	}

	path := filepath.Join(dir, "connections.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perms = %o, want 600", perm)
	}

	root := readStore(t, path)
	if root["defaultId"] != "outfit:openrouter" {
		t.Errorf("defaultId = %v, want outfit:openrouter", root["defaultId"])
	}
	conn := connByID(root, "outfit:openrouter")
	if conn == nil {
		t.Fatal("managed connection missing")
	}
	if conn["type"] != "openai" {
		t.Errorf("type = %v, want openai", conn["type"])
	}
	if conn["url"] != "https://openrouter.ai/api/v1" {
		t.Errorf("url = %v", conn["url"])
	}
	if conn["defaultModel"] != "deepseek/deepseek-v4-pro" {
		t.Errorf("defaultModel = %v", conn["defaultModel"])
	}
	if conn["createdAt"] == nil || conn["createdAt"] == "" {
		t.Error("createdAt was not stamped")
	}
	// No secret must ever land in the connections store.
	if _, ok := conn["apiKey"]; ok {
		t.Error("connection must not carry an apiKey")
	}
}

func TestWrite_PreservesSiblingsAndUnknownFields(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(DataDirEnv, dir)
	path := filepath.Join(dir, "connections.json")

	seed := `{
  "defaultId": "abc123",
  "theme": "dark",
  "connections": [
    { "id": "abc123", "name": "My Gateway", "type": "openclaw", "url": "https://gw.example" },
    { "id": "outfit:openrouter", "name": "old", "type": "openai", "url": "https://old",
      "createdAt": "2020-01-01T00:00:00Z", "lastUsed": "2020-02-02T00:00:00Z" }
  ]
}`
	os.WriteFile(path, []byte(seed), 0o600)

	if err := Write("openrouter", sample()); err != nil {
		t.Fatalf("Write: %v", err)
	}

	root := readStore(t, path)
	if root["theme"] != "dark" {
		t.Error("unknown top-level key not preserved")
	}
	if connByID(root, "abc123") == nil {
		t.Error("sibling connection was dropped")
	}
	conn := connByID(root, "outfit:openrouter")
	if conn["url"] != "https://openrouter.ai/api/v1" {
		t.Errorf("url not updated: %v", conn["url"])
	}
	// createdAt preserved; unknown field (lastUsed) preserved.
	if conn["createdAt"] != "2020-01-01T00:00:00Z" {
		t.Errorf("createdAt = %v, want the preserved value", conn["createdAt"])
	}
	if conn["lastUsed"] != "2020-02-02T00:00:00Z" {
		t.Errorf("unknown field lastUsed not preserved: %v", conn["lastUsed"])
	}
	// defaultId now points at the managed connection.
	if root["defaultId"] != "outfit:openrouter" {
		t.Errorf("defaultId = %v, want outfit:openrouter", root["defaultId"])
	}
}

func TestWrite_ReapplyUpdatesInPlace(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(DataDirEnv, dir)
	path := filepath.Join(dir, "connections.json")

	if err := Write("openrouter", sample()); err != nil {
		t.Fatal(err)
	}
	next := sample()
	next.DefaultModel = "deepseek/deepseek-v4-flash"
	if err := Write("openrouter", next); err != nil {
		t.Fatal(err)
	}

	conns, _ := readStore(t, path)["connections"].([]any)
	count := 0
	for _, raw := range conns {
		if m, ok := raw.(map[string]any); ok && m["id"] == "outfit:openrouter" {
			count++
			if m["defaultModel"] != "deepseek/deepseek-v4-flash" {
				t.Errorf("defaultModel = %v, want the updated model", m["defaultModel"])
			}
		}
	}
	if count != 1 {
		t.Errorf("found %d managed connections, want exactly 1 (no duplicate)", count)
	}
}

func TestRemove(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(DataDirEnv, dir)
	path := filepath.Join(dir, "connections.json")

	if err := Write("openrouter", sample()); err != nil {
		t.Fatal(err)
	}

	// Removing a non-matching model key is a no-op.
	if n, _ := Remove("openrouter", []string{"nope"}); n != 0 {
		t.Errorf("remove with wrong model = %d, want 0", n)
	}

	// Removing the whole provider removes the connection and clears defaultId.
	n, err := Remove("openrouter", nil)
	if err != nil || n != 1 {
		t.Fatalf("Remove = %d, %v; want 1, nil", n, err)
	}
	root := readStore(t, path)
	if connByID(root, "outfit:openrouter") != nil {
		t.Error("connection should have been removed")
	}
	if _, ok := root["defaultId"]; ok {
		t.Error("defaultId should have been cleared")
	}

	// Removing again is a no-op.
	if n, _ := Remove("openrouter", nil); n != 0 {
		t.Errorf("removing missing connection returned %d, want 0", n)
	}
}

func TestRemove_MatchingModelKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(DataDirEnv, dir)

	if err := Write("openrouter", sample()); err != nil {
		t.Fatal(err)
	}
	// The connection's model is named, so it is removed.
	n, err := Remove("openrouter", []string{"deepseek/deepseek-v4-pro"})
	if err != nil || n != 1 {
		t.Fatalf("Remove matching model = %d, %v; want 1, nil", n, err)
	}
}

func TestState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(DataDirEnv, dir)

	if err := Write("openrouter", sample()); err != nil {
		t.Fatal(err)
	}
	states, err := State()
	if err != nil {
		t.Fatal(err)
	}
	st, ok := states["openrouter"]
	if !ok {
		t.Fatal("openrouter missing from state")
	}
	if st.BaseURL != "https://openrouter.ai/api/v1" {
		t.Errorf("baseURL = %q", st.BaseURL)
	}
	if len(st.ModelKeys) != 1 || st.ModelKeys[0] != "deepseek/deepseek-v4-pro" {
		t.Errorf("model keys = %v, want the single configured model", st.ModelKeys)
	}
	if len(st.Contexts) != 0 || len(st.Outputs) != 0 {
		t.Error("a lucinate connection cannot carry context/output limits")
	}
}

func TestState_IgnoresUnmanagedConnections(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(DataDirEnv, dir)
	path := filepath.Join(dir, "connections.json")
	os.WriteFile(path, []byte(`{"connections":[{"id":"hex123","type":"openclaw","url":"https://gw"}]}`), 0o600)

	states, err := State()
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 0 {
		t.Errorf("expected no managed providers, got %v", states)
	}
}

func TestState_NoFile(t *testing.T) {
	t.Setenv(DataDirEnv, t.TempDir())
	states, err := State()
	if err != nil {
		t.Fatalf("State on missing file: %v", err)
	}
	if len(states) != 0 {
		t.Errorf("expected empty state, got %v", states)
	}
}

func TestDefaultProviderID(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(DataDirEnv, dir)

	if _, ok := DefaultProviderID(); ok {
		t.Error("no store yet, expected no default provider")
	}

	if err := Write("openrouter", sample()); err != nil {
		t.Fatal(err)
	}
	id, ok := DefaultProviderID()
	if !ok || id != "openrouter" {
		t.Errorf("DefaultProviderID = %q, %v; want openrouter, true", id, ok)
	}
}

func TestState_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(DataDirEnv, dir)
	path := filepath.Join(dir, "connections.json")
	if err := os.WriteFile(path, []byte("{invalid}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := State(); err == nil {
		t.Error("expected error for malformed connections.json")
	}
}

func TestDefaultProviderID_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(DataDirEnv, dir)
	path := filepath.Join(dir, "connections.json")
	if err := os.WriteFile(path, []byte("{invalid}"), 0o600); err != nil {
		t.Fatal(err)
	}
	// DefaultProviderID returns (false, false) for malformed JSON.
	_, ok := DefaultProviderID()
	if ok {
		t.Error("expected no default provider for malformed JSON")
	}
}
