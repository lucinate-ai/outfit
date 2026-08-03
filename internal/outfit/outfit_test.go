package outfit

import (
	"reflect"
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
			name: "provider and model",
			in:   "PROVIDER openrouter\nMODEL deepseek/deepseek-v4-pro\n",
			want: Selection{Provider: "openrouter", Model: "deepseek/deepseek-v4-pro"},
		},
		{
			name: "case-insensitive keywords",
			in:   "provider ollama\nModel llama3.2\n",
			want: Selection{Provider: "ollama", Model: "llama3.2"},
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
			in:   "PROVIDER\tollama\nMODEL     llama3.2\n",
			want: Selection{Provider: "ollama", Model: "llama3.2"},
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
		{
			name: "repeated env",
			in:   "PROVIDER llamacpp\nMODEL m\nENV AWS_PROFILE=dev\nENV AWS_REGION=eu-west-2\n",
			want: Selection{Provider: "llamacpp", Model: "m", Env: []EnvVar{{Key: "AWS_PROFILE", Value: "dev"}, {Key: "AWS_REGION", Value: "eu-west-2"}}},
		},
		{
			name: "env with empty value",
			in:   "PROVIDER llamacpp\nMODEL m\nENV AWS_PROFILE=\n",
			want: Selection{Provider: "llamacpp", Model: "m", Env: []EnvVar{{Key: "AWS_PROFILE", Value: ""}}},
		},
		{
			name: "env value keeps later equals signs",
			in:   "PROVIDER llamacpp\nMODEL m\nENV TOKEN=a=b=c\n",
			want: Selection{Provider: "llamacpp", Model: "m", Env: []EnvVar{{Key: "TOKEN", Value: "a=b=c"}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse([]byte(tc.in))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParse_Errors(t *testing.T) {
	cases := map[string]string{
		"missing provider":      "MODEL llama3.2\n",
		"unknown keyword":       "PROVIDER ollama\nFLAVOUR vanilla\n",
		"family retired":        "PROVIDER openrouter\nFAMILY deepseek-v4\n",
		"keyword no value":      "PROVIDER\n",
		"too many values":       "PROVIDER a b\n",
		"duplicate keyword":     "PROVIDER a\nPROVIDER b\n",
		"duplicate alias":       "PROVIDER a\nMODEL m\nBASEURL u1\nURL u2\n",
		"env without equals":    "PROVIDER a\nMODEL m\nENV JUST_A_NAME\n",
		"env empty key":         "PROVIDER a\nMODEL m\nENV =value\n",
		"env value with spaces": "PROVIDER a\nMODEL m\nENV KEY=hello world\n",
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
		Model:    "deepseek/deepseek-v4-pro",
		Alias:    "deepseek",
		Context:  "128000",
		Output:   "32000",
		BaseURL:  "https://gateway.example/v1",
		Preset:   "./preset.ini",
		Env:      []EnvVar{{Key: "AWS_PROFILE", Value: "dev"}, {Key: "AWS_REGION", Value: "eu-west-2"}},
	}
	out := Format(sel)
	if !strings.HasPrefix(out, "PROVIDER openrouter\n") {
		t.Errorf("export not canonical:\n%s", out)
	}
	if !strings.Contains(out, "ENV      AWS_PROFILE=dev") {
		t.Errorf("Format should emit ENV lines, got:\n%s", out)
	}
	got, err := Parse([]byte(out))
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if !reflect.DeepEqual(got, sel) {
		t.Errorf("round-trip changed selection: %+v -> %+v", sel, got)
	}
}

func TestParse_Remote(t *testing.T) {
	sel, err := Parse([]byte("PROVIDER openai-compatible\nREMOTE ./remote.json\n"))
	if err != nil {
		t.Fatal(err)
	}
	if sel.Remote != "./remote.json" {
		t.Errorf("Remote = %q, want ./remote.json", sel.Remote)
	}
	if out := Format(sel); !strings.Contains(out, "REMOTE   ./remote.json") {
		t.Errorf("Format should emit REMOTE, got:\n%s", out)
	}
	if _, err := Parse([]byte("PROVIDER x\nREMOTE a\nREMOTE b\n")); err == nil {
		t.Error("duplicate REMOTE should error")
	}
}
