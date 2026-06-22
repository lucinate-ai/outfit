package main

import (
	"fmt"
	"strconv"
	"strings"
)

// parseContextSize parses a human-friendly context window size into a token
// count. It is deliberately lenient: surrounding whitespace, commas, and
// underscores are ignored, an optional k/m/g/t suffix is honoured
// (case-insensitive, decimal — k=1e3, m=1e6, g/b=1e9, t=1e12), a fractional
// value may precede a suffix (e.g. "1.5m"), and a trailing "tokens"/"tok" word
// is tolerated. So "128k", "128,000", "128_000", "  128 K tokens ", and
// "0.128m" all parse to 128000.
func parseContextSize(s string) (int, error) {
	orig := s
	// Normalise: drop separators, lowercase, strip a trailing "tokens" word.
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer(",", "", "_", "", " ", "").Replace(s)
	for _, suf := range []string{"tokens", "token", "toks", "tok"} {
		if strings.HasSuffix(s, suf) {
			s = strings.TrimSuffix(s, suf)
			break
		}
	}

	mult := 1.0
	if n := len(s); n > 0 {
		switch s[n-1] {
		case 'k':
			mult, s = 1e3, s[:n-1]
		case 'm':
			mult, s = 1e6, s[:n-1]
		case 'g', 'b':
			mult, s = 1e9, s[:n-1]
		case 't':
			mult, s = 1e12, s[:n-1]
		}
	}

	if s == "" {
		return 0, fmt.Errorf("invalid context size %q", orig)
	}
	val, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid context size %q", orig)
	}
	tokens := val * mult
	if tokens < 1 {
		return 0, fmt.Errorf("context size %q must be at least 1 token", orig)
	}
	return int(tokens), nil
}

// applyContextSize sets limit.context on every model in models, merging into
// any existing limit map rather than replacing it. The model values are the
// map[string]any entries produced by buildProviderBlock.
func applyContextSize(models map[string]any, ctx int) {
	for k, v := range models {
		m, ok := v.(map[string]any)
		if !ok {
			m = map[string]any{}
		}
		limit, ok := m["limit"].(map[string]any)
		if !ok {
			limit = map[string]any{}
		}
		limit["context"] = ctx
		m["limit"] = limit
		models[k] = m
	}
}
