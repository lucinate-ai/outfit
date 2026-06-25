package outfit

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want Selection
	}{
		{
			name: "provider and family",
			in:   "PROVIDER openrouter\nFAMILY deepseek-v4\n",
			want: Selection{Provider: "openrouter", Family: "deepseek-v4"},
		},
		{
			name: "case-insensitive keywords",
			in:   "provider ollama\nFamily llama\n",
			want: Selection{Provider: "ollama", Family: "llama"},
		},
		{
			name: "model only",
			in:   "PROVIDER llamacpp\nMODEL gemma-4-12b-it\n",
			want: Selection{Provider: "llamacpp", Model: "gemma-4-12b-it"},
		},
		{
			name: "comments, blanks, and inline comments",
			in:   "# my Outfit\n\nPROVIDER openrouter   # the provider\nMODEL  m1\t# inline tab comment\n",
			want: Selection{Provider: "openrouter", Model: "m1"},
		},
		{
			name: "extra whitespace and tabs as separator",
			in:   "PROVIDER\tollama\nFAMILY     llama\n",
			want: Selection{Provider: "ollama", Family: "llama"},
		},
		{
			name: "context, output, and base url",
			in:   "PROVIDER llamacpp\nMODEL gemma\nCONTEXT 128k\nOUTPUT 32k\nBASEURL http://localhost:9090/v1\n",
			want: Selection{Provider: "llamacpp", Model: "gemma", Context: "128k", Output: "32k", BaseURL: "http://localhost:9090/v1"},
		},
		{
			name: "base url aliases",
			in:   "PROVIDER openai-compatible\nMODEL m\nURL https://gw/v1\n",
			want: Selection{Provider: "openai-compatible", Model: "m", BaseURL: "https://gw/v1"},
		},
		{
			name: "alias and preset",
			in:   "PROVIDER llamacpp\nMODEL unsloth/Qwen:Q4_K_M\nALIAS qwen\nPRESET ./preset.ini\n",
			want: Selection{Provider: "llamacpp", Model: "unsloth/Qwen:Q4_K_M", Alias: "qwen", Preset: "./preset.ini"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse([]byte(tc.in))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParse_Errors(t *testing.T) {
	cases := map[string]string{
		"missing provider":  "FAMILY llama\n",
		"unknown keyword":   "PROVIDER ollama\nFLAVOUR vanilla\n",
		"keyword no value":  "PROVIDER\n",
		"too many values":   "PROVIDER a b\n",
		"duplicate keyword": "PROVIDER a\nPROVIDER b\n",
		"duplicate alias":   "PROVIDER a\nMODEL m\nBASEURL u1\nURL u2\n",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(in)); err == nil {
				t.Errorf("expected error for %q", in)
			}
		})
	}
}

func TestFormatRoundTrip(t *testing.T) {
	sel := Selection{
		Provider: "openrouter",
		Family:   "deepseek-v4",
		Model:    "deepseek/deepseek-v4-pro",
		Alias:    "deepseek",
		Context:  "128000",
		Output:   "32000",
		BaseURL:  "https://gateway.example/v1",
		Preset:   "./preset.ini",
	}
	out := Format(sel)
	if !strings.HasPrefix(out, "PROVIDER openrouter\n") {
		t.Errorf("export not canonical:\n%s", out)
	}
	got, err := Parse([]byte(out))
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if got != sel {
		t.Errorf("round-trip changed selection: %+v -> %+v", sel, got)
	}
}
