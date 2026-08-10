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
		"no nodes":   "nodes: []\n",
		"no name":    "nodes:\n  - host: a.local\n",
		"no host":    "nodes:\n  - name: studio\n",
		"other kind": "nodes:\n  - name: prod\n    host: a.local\n    kind: remote\n",
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
	_, err := Load(writeFleet(t, "nodes:\n  - name: prod\n    host: a.local\n    kind: remote\n", ""))
	if err == nil || !strings.Contains(err.Error(), "remote") {
		t.Fatalf("error = %v, want one naming the unsupported kind", err)
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
