package main

import "testing"

func TestParseContextSize_Lenient(t *testing.T) {
	// Each input must parse to the same 128000-token window, exercising the
	// suffix, separator, whitespace, fraction, and trailing-word leniency.
	for _, in := range []string{
		"128000",
		"128,000",
		"128_000",
		"128k",
		"128K",
		"  128 k ",
		"128 K tokens",
		"128ktok",
		"0.128m",
	} {
		got, err := parseContextSize(in)
		if err != nil {
			t.Errorf("parseContextSize(%q) errored: %v", in, err)
			continue
		}
		if got != 128000 {
			t.Errorf("parseContextSize(%q) = %d, want 128000", in, got)
		}
	}
}

func TestParseContextSize_Suffixes(t *testing.T) {
	cases := map[string]int{
		"200000": 200000,
		"1m":     1_000_000,
		"1M":     1_000_000,
		"1.5m":   1_500_000,
		"2g":     2_000_000_000,
		"3b":     3_000_000_000,
		"1t":     1_000_000_000_000,
		"32k":    32_000,
	}
	for in, want := range cases {
		got, err := parseContextSize(in)
		if err != nil {
			t.Errorf("parseContextSize(%q) errored: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseContextSize(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseContextSize_Invalid(t *testing.T) {
	for _, in := range []string{
		"",        // empty
		"   ",     // blank
		"abc",     // not a number
		"k",       // suffix only, no value
		"12x",     // unknown suffix folds into the number and fails
		"0",       // below one token
		"-5k",     // negative
		"0.0001k", // rounds below one token
	} {
		if got, err := parseContextSize(in); err == nil {
			t.Errorf("parseContextSize(%q) = %d, want error", in, got)
		}
	}
}

func TestApplyContextSize(t *testing.T) {
	// A model with an existing limit must keep sibling limit keys.
	models := map[string]any{
		"a": map[string]any{"name": "A"},
		"b": map[string]any{"name": "B", "limit": map[string]any{"output": 8192}},
		"c": "not-a-map", // hardened against unexpected shapes
	}
	applyContextSize(models, 200000)

	a := models["a"].(map[string]any)["limit"].(map[string]any)
	if a["context"] != 200000 {
		t.Errorf("model a context = %v, want 200000", a["context"])
	}
	b := models["b"].(map[string]any)["limit"].(map[string]any)
	if b["context"] != 200000 {
		t.Errorf("model b context = %v, want 200000", b["context"])
	}
	if b["output"] != 8192 {
		t.Errorf("model b output limit not preserved: %v", b["output"])
	}
	c := models["c"].(map[string]any)["limit"].(map[string]any)
	if c["context"] != 200000 {
		t.Errorf("non-map model c was not normalised: %v", c)
	}
}
