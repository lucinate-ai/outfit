package preset

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	in := `
# a comment
[*]
ctx-size = 0
mmap     = 1

[Qwen3.5-4B]
hf       = unsloth/Qwen3.5-4B-GGUF:Q4_K_M
ctx-size = 262144
temp     = 1.0
`
	p, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(p.Global) != 2 {
		t.Fatalf("global params = %d, want 2", len(p.Global))
	}
	if p.Global[0].Key != "ctx-size" || p.Global[0].Value != "0" {
		t.Errorf("global[0] = %+v", p.Global[0])
	}
	if len(p.Sections) != 1 {
		t.Fatalf("sections = %d, want 1", len(p.Sections))
	}
	sec := p.Sections[0]
	if sec.Name != "Qwen3.5-4B" {
		t.Errorf("section name = %q", sec.Name)
	}
	if got := sec.Params[0]; got.Key != "hf" || got.Value != "unsloth/Qwen3.5-4B-GGUF:Q4_K_M" {
		t.Errorf("section param[0] = %+v", got)
	}
}

func TestParse_InlineCommentsAndJSON(t *testing.T) {
	in := "[m]\n" +
		"ngl = 99    # offload everything\n" +
		"chat-template-kwargs = {\"reasoning_effort\": \"high\"}  ; keep the JSON\n"
	p, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	params := p.Sections[0].Params
	if params[0].Value != "99" {
		t.Errorf("inline comment not stripped: %q", params[0].Value)
	}
	// The `:` inside the JSON must survive — it is not the key/value separator,
	// and the `;` comment after it must be removed without touching the braces.
	if params[1].Value != `{"reasoning_effort": "high"}` {
		t.Errorf("JSON value corrupted: %q", params[1].Value)
	}
}

func TestParse_GlobalAlias(t *testing.T) {
	p, err := Parse([]byte("[global]\nmmap = 1\n[m]\nhf = a/b\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(p.Global) != 1 || len(p.Sections) != 1 {
		t.Fatalf("got %d global, %d sections", len(p.Global), len(p.Sections))
	}
}

func TestParse_Errors(t *testing.T) {
	cases := map[string]string{
		"unterminated header": "[oops\nhf = a/b\n",
		"empty header":        "[]\nhf = a/b\n",
		"no separator":        "[m]\njust-a-key\n",
		"empty key":           "[m]\n = value\n",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(in)); err == nil {
				t.Errorf("expected error for %q", in)
			}
		})
	}
}

func TestSelect(t *testing.T) {
	two, _ := Parse([]byte("[a]\nhf = a/a\n[b]\nhf = b/b\n"))
	one, _ := Parse([]byte("[only]\nhf = o/o\n"))
	none, _ := Parse([]byte("[*]\nmmap = 1\n"))

	t.Run("by name", func(t *testing.T) {
		s, err := two.Select("b")
		if err != nil || s.Name != "b" {
			t.Fatalf("Select(b) = %q, %v", s.Name, err)
		}
	})
	t.Run("name is case-insensitive", func(t *testing.T) {
		s, err := two.Select("A")
		if err != nil || s.Name != "a" {
			t.Fatalf("Select(A) = %q, %v", s.Name, err)
		}
	})
	t.Run("single section without name", func(t *testing.T) {
		s, err := one.Select("")
		if err != nil || s.Name != "only" {
			t.Fatalf("Select on single = %q, %v", s.Name, err)
		}
	})
	t.Run("single section ignores a mismatched label", func(t *testing.T) {
		s, err := one.Select("some-label")
		if err != nil || s.Name != "only" {
			t.Fatalf("Select(some-label) = %q, %v", s.Name, err)
		}
	})
	t.Run("multiple without name errors", func(t *testing.T) {
		if _, err := two.Select(""); err == nil {
			t.Error("expected error selecting among multiple with no model")
		}
	})
	t.Run("multiple with unknown name errors", func(t *testing.T) {
		if _, err := two.Select("c"); err == nil {
			t.Error("expected error for unknown section among multiple")
		}
	})
	t.Run("no sections errors", func(t *testing.T) {
		if _, err := none.Select(""); err == nil {
			t.Error("expected error when preset has no model sections")
		}
	})
}

func TestArgs(t *testing.T) {
	p, _ := Parse([]byte(`
[*]
ctx-size = 0
mmap     = 1
parallel = 4

[Qwen]
hf       = unsloth/Qwen:Q4_K_M
ctx-size = 262144
temp     = 1.0
`))
	sec, _ := p.Select("Qwen")
	got := strings.Join(p.Args(sec), " ")
	// Global ctx-size is overridden in place by the section's value, mmap is a
	// bare boolean, hf normalises to --hf-repo.
	want := "--ctx-size 262144 --mmap --parallel 4 --hf-repo unsloth/Qwen:Q4_K_M --temp 1.0"
	if got != want {
		t.Errorf("Args =\n  %q\nwant\n  %q", got, want)
	}
}

func TestFlagFor(t *testing.T) {
	cases := []struct {
		key, value string
		want       []string
	}{
		{"ctx-size", "4096", []string{"--ctx-size", "4096"}},
		{"hf", "a/b:Q4", []string{"--hf-repo", "a/b:Q4"}},
		{"ngl", "99", []string{"--n-gpu-layers", "99"}},
		{"fa", "on", []string{"--flash-attn", "on"}},
		{"ctk", "q8_0", []string{"--cache-type-k", "q8_0"}},
		{"mmap", "1", []string{"--mmap"}},
		{"mmap", "0", nil},
		{"jinja", "true", []string{"--jinja"}},
		{"kv-unified", "false", nil},
		{"top-k", "0", []string{"--top-k", "0"}},
		{"c", "8192", []string{"--ctx-size", "8192"}},
		{"x", "", []string{"-x"}},                        // unknown single char → short flag
		{"unknown-long", "", []string{"--unknown-long"}}, // valueless passthrough
	}
	for _, tc := range cases {
		got := flagFor(tc.key, tc.value)
		if strings.Join(got, " ") != strings.Join(tc.want, " ") {
			t.Errorf("flagFor(%q,%q) = %v, want %v", tc.key, tc.value, got, tc.want)
		}
	}
}

func TestFormatCommand(t *testing.T) {
	argv := []string{"llama-server", "--temp", "1.0", "--chat-template-kwargs", `{"reasoning_effort": "high"}`}
	got := FormatCommand(argv)
	want := `llama-server --temp 1.0 --chat-template-kwargs '{"reasoning_effort": "high"}'`
	if got != want {
		t.Errorf("FormatCommand =\n  %s\nwant\n  %s", got, want)
	}
}

func TestCommand(t *testing.T) {
	p, _ := Parse([]byte("[m]\nhf = a/b\n"))
	sec, _ := p.Select("m")
	argv := p.Command("llama-server", sec)
	if len(argv) == 0 || argv[0] != "llama-server" {
		t.Fatalf("Command argv[0] = %v", argv)
	}
	if strings.Join(argv[1:], " ") != "--hf-repo a/b" {
		t.Errorf("Command args = %v", argv[1:])
	}
}
