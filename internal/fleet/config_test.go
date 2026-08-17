package fleet

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lucinate-ai/outfit/internal/daemon"
)

// writeFleet puts a fleet.yaml (and optionally a .env) in a temp dir and
// returns the file's path.
func writeFleet(t *testing.T, body string, dotEnv string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultFile)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if dotEnv != "" {
		if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(dotEnv), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestLoadMultiNode(t *testing.T) {
	path := writeFleet(t, `
nodes:
  - name: studio
    host: studio.local
  - name: gpu-box
    host: 198.51.100.7
    port: 5252
    tokenEnv: GPU_BOX_TOKEN
`, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Nodes) != 2 {
		t.Fatalf("got %d nodes, want 2", len(cfg.Nodes))
	}

	// A node with no port takes the daemon's default — the two sides share
	// the constant, so this cannot drift.
	studio := cfg.Nodes[0]
	if studio.Name != "studio" || studio.Host != "studio.local" {
		t.Errorf("node 0 = %+v", studio)
	}
	if want := "http://studio.local:" + strconv.Itoa(daemon.DefaultAPIPort); studio.BaseURL() != want {
		t.Errorf("BaseURL = %q, want %q", studio.BaseURL(), want)
	}
	// Kind defaults to daemon.
	if studio.Kind != KindDaemon {
		t.Errorf("kind = %q, want %q", studio.Kind, KindDaemon)
	}

	if got := cfg.Nodes[1].BaseURL(); got != "http://198.51.100.7:5252" {
		t.Errorf("explicit port BaseURL = %q", got)
	}
	if got := cfg.Names(); len(got) != 2 || got[0] != "studio" || got[1] != "gpu-box" {
		t.Errorf("Names() = %v, want file order", got)
	}
	if _, ok := cfg.Node("gpu-box"); !ok {
		t.Error("Node(gpu-box) not found")
	}
	if _, ok := cfg.Node("nope"); ok {
		t.Error("Node(nope) unexpectedly found")
	}
}

func TestLoadRejectsDuplicateNames(t *testing.T) {
	path := writeFleet(t, `
nodes:
  - name: studio
    host: a.local
  - name: studio
    host: b.local
`, "")
	_, err := Load(path)
	if err == nil {
		t.Fatal("duplicate node names accepted")
	}
	if !strings.Contains(err.Error(), "studio") {
		t.Errorf("error %q does not name the duplicate", err)
	}
}

func TestLoadRejectsIncompleteNodes(t *testing.T) {
	for name, body := range map[string]string{
		"no nodes":              "nodes: []\n",
		"no name":               "nodes:\n  - host: a.local\n",
		"no host":               "nodes:\n  - name: studio\n",
		"remote name is a path": "nodes:\n  - name: a/b\n    kind: remote\n",
		"remote name has .json": "nodes:\n  - name: prod.json\n    kind: remote\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeFleet(t, body, "")); err == nil {
				t.Fatalf("%s accepted", name)
			}
		})
	}
}

// An unimplemented kind must say so rather than be silently skipped.
func TestLoadUnknownKindNamesIt(t *testing.T) {
	_, err := Load(writeFleet(t, "nodes:\n  - name: prod\n    host: a.local\n    kind: satellite\n", ""))
	if err == nil || !strings.Contains(err.Error(), "satellite") {
		t.Fatalf("error = %v, want one naming the unsupported kind", err)
	}
}

// A kind-remote node's name is the registered environment it drives; it needs
// no host, and nothing else to name it with.
func TestLoadRemoteKindNamedByEnvironment(t *testing.T) {
	path := writeFleet(t, `
nodes:
  - name: prod
    kind: remote
`, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	n := cfg.Nodes[0]
	if n.Kind != KindRemote || n.Name != "prod" {
		t.Errorf("remote node = %+v, want kind remote named prod", n)
	}
	if n.Host != "" {
		t.Errorf("remote node needs no host, got %q", n.Host)
	}
}

func TestResolveDefaultAndExplicit(t *testing.T) {
	path := writeFleet(t, "nodes:\n  - name: studio\n    host: a.local\n", "")

	// Explicit path.
	if _, err := Resolve(path); err != nil {
		t.Fatal(err)
	}

	// Default: ./fleet.yaml in the working directory.
	t.Chdir(filepath.Dir(path))
	cfg, err := Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Path != DefaultFile {
		t.Errorf("Path = %q, want %q", cfg.Path, DefaultFile)
	}
}

func TestResolveMissingFileNamesThePath(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := Resolve("")
	if err == nil {
		t.Fatal("missing fleet file did not error")
	}
	if !strings.Contains(err.Error(), DefaultFile) {
		t.Errorf("error %q does not name the expected path", err)
	}
}

func TestTokenResolution(t *testing.T) {
	path := writeFleet(t, `
nodes:
  - name: plain
    host: a.local
  - name: gpu-box
    host: b.local
    tokenEnv: GPU_BOX_TOKEN
`, "GPU_BOX_TOKEN=from-dotenv\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := cfg.Node("plain")
	gpu, _ := cfg.Node("gpu-box")

	// No reference: no token, no error — a loopback daemon needs none.
	if tok, err := cfg.Token(plain); err != nil || tok != "" {
		t.Errorf("Token(plain) = %q, %v; want empty, nil", tok, err)
	}

	// Falls back to the .env beside the file.
	t.Setenv("GPU_BOX_TOKEN", "")
	if tok, err := cfg.Token(gpu); err != nil || tok != "from-dotenv" {
		t.Errorf("Token(gpu) = %q, %v; want from-dotenv", tok, err)
	}

	// An exported value wins over the .env.
	t.Setenv("GPU_BOX_TOKEN", "from-env")
	if tok, err := cfg.Token(gpu); err != nil || tok != "from-env" {
		t.Errorf("Token(gpu) = %q, %v; want from-env (environment beats .env)", tok, err)
	}
}

func TestTokenUnsetIsAConfigError(t *testing.T) {
	path := writeFleet(t, "nodes:\n  - name: gpu-box\n    host: b.local\n    tokenEnv: MISSING_TOKEN\n", "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	node, _ := cfg.Node("gpu-box")
	t.Setenv("MISSING_TOKEN", "")
	_, err = cfg.Token(node)
	if err == nil {
		t.Fatal("an unset token variable was accepted as an empty token")
	}
	// The message must name the variable and the node, so a typo is obvious.
	for _, want := range []string{"MISSING_TOKEN", "gpu-box"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// The file format carries a token *reference*, never a value: a stray `token:`
// key is not a field, so it cannot smuggle a secret into the file.
func TestLiteralTokenIsNotAField(t *testing.T) {
	path := writeFleet(t, "nodes:\n  - name: gpu-box\n    host: b.local\n    token: sk-literal-secret\n", "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	node, _ := cfg.Node("gpu-box")
	if node.TokenEnv != "" {
		t.Errorf("a literal token populated TokenEnv: %q", node.TokenEnv)
	}
	tok, err := cfg.Token(node)
	if err != nil || tok != "" {
		t.Errorf("Token = %q, %v; a literal `token:` must not be used", tok, err)
	}
}

// A node may declare where its engine serves, for the setups a daemon cannot
// describe. Each field falls back independently.
func TestEngineOverride(t *testing.T) {
	path := writeFleet(t, `
nodes:
  - name: proxied
    host: node.local
    engine:
      host: proxy.local
      port: 8443
      path: /openai
  - name: published
    host: node2.local
    engine:
      port: 18080
  - name: plain
    host: node3.local
`, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	full, _ := cfg.Node("proxied")
	if full.Engine == nil {
		t.Fatal("engine block not parsed")
	}
	if full.Engine.Host != "proxy.local" || full.Engine.Port != 8443 || full.Engine.Path != "/openai" {
		t.Errorf("engine = %+v", *full.Engine)
	}

	partial, _ := cfg.Node("published")
	if partial.Engine == nil || partial.Engine.Port != 18080 {
		t.Fatalf("partial override lost its port: %+v", partial.Engine)
	}
	if partial.Engine.Host != "" {
		t.Errorf("an undeclared host should stay empty so it falls back, got %q", partial.Engine.Host)
	}

	if plain, _ := cfg.Node("plain"); plain.Engine != nil {
		t.Errorf("a node with no engine block should carry none, got %+v", plain.Engine)
	}
}

// The engine key is a reference like the daemon token, resolved the same way,
// and the two are independent credentials.
func TestEngineTokenResolution(t *testing.T) {
	path := writeFleet(t, `
nodes:
  - name: gated
    host: gated.local
    tokenEnv: NODE_TOKEN
    engineTokenEnv: NODE_ENGINE_KEY
  - name: open
    host: open.local
`, "NODE_ENGINE_KEY=from-dotenv\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	gated, _ := cfg.Node("gated")

	// The .env beside the file fills a gap.
	got, err := cfg.EngineToken(gated)
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-dotenv" {
		t.Errorf("engine token = %q, want the .env value", got)
	}

	// An exported value wins, as everywhere else in outfit.
	t.Setenv("NODE_ENGINE_KEY", "exported")
	if got, err := cfg.EngineToken(gated); err != nil || got != "exported" {
		t.Errorf("engine token = %q, %v; want the exported value", got, err)
	}

	// A node naming no engine variable needs no engine key.
	open, _ := cfg.Node("open")
	if got, err := cfg.EngineToken(open); err != nil || got != "" {
		t.Errorf("token = %q, %v; want empty and no error", got, err)
	}
}

func TestEngineTokenUnsetNamesTheVariable(t *testing.T) {
	path := writeFleet(t, `
nodes:
  - name: gated
    host: gated.local
    engineTokenEnv: NOWHERE_ENGINE_KEY
`, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	node, _ := cfg.Node("gated")
	_, err = cfg.EngineToken(node)
	if err == nil {
		t.Fatal("an unset engine token variable should be a config error")
	}
	for _, want := range []string{"NOWHERE_ENGINE_KEY", "gated"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %s, got %q", want, err)
		}
	}
}

func TestPreferSetting(t *testing.T) {
	cases := map[string]Prefer{
		"prefer: active\n": PreferActive,
		"prefer: idle\n":   PreferIdle,
		"":                 "", // absent: the selector applies the idle default
	}
	for decl, want := range cases {
		t.Run(strings.TrimSpace(decl), func(t *testing.T) {
			cfg, err := Load(writeFleet(t, decl+"nodes:\n  - name: a\n    host: a.local\n", ""))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Prefer != want {
				t.Errorf("Prefer = %q, want %q", cfg.Prefer, want)
			}
		})
	}
}

func TestPreferRejectsUnknownValue(t *testing.T) {
	_, err := Load(writeFleet(t, "prefer: whatever\nnodes:\n  - name: a\n    host: a.local\n", ""))
	if err == nil {
		t.Fatal("an unknown prefer value should fail to parse")
	}
	for _, want := range []string{"idle", "active"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %q, got %q", want, err)
		}
	}
}
