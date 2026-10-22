package providers

import "testing"

// TestMatchAllowPattern pins the allowlist glob's contract (#365): `*` spans
// separators, matching is case-insensitive and anchored, and a pattern without
// a `*` stays an exact whole-string match. It also pins that the resolver's own
// segment-local matcher keeps its old answer for the same inputs, so the two
// globs cannot be collapsed into one by accident.
func TestMatchAllowPattern(t *testing.T) {
	cases := []struct {
		pattern string
		model   string
		want    bool
	}{
		{"*claude*", "anthropic/claude-sonnet-4", true},
		{"*claude*", "claude-sonnet-4", true},
		{"*claude*", "openai/gpt-4o", false},
		{"*haiku*", "anthropic/claude-haiku-4", true},
		{"claude*", "claude-sonnet-4", true},
		{"claude*", "anthropic/claude-sonnet-4", false},
		{"*", "anything/at-all", true},
		{"openai/*", "openai/gpt-4o", true},
		{"openai/*", "openai/x/gpt-4o", true},
		{"oc/*", "oc/gpt-5", true},
		{"oc/*", "openai/gpt-4o", false},
		{"*CLAUDE*", "anthropic/claude-sonnet-4", true},
		{"anthropic/claude-sonnet-4", "anthropic/claude-sonnet-4", true},
		{"anthropic/claude-sonnet-4", "anthropic/CLAUDE-sonnet-4", true},
		{"anthropic/claude-sonnet-4", "anthropic/claude-sonnet-4.5", false},
		{"gpt-4o", "openai/gpt-4o", false},
		{"  *claude*  ", "anthropic/claude-sonnet-4", true},
		{"*claude*", "  ", false},
		{"", "openai/gpt-4o", false},
		// Regex metacharacters in a pattern are literals, not syntax.
		{"gpt-4.1", "gpt-4.1", true},
		{"gpt-4.1", "gpt-4x1", false},
		{"a+b", "a+b", true},
	}
	for _, tc := range cases {
		if got := MatchAllowPattern(tc.pattern, tc.model); got != tc.want {
			t.Errorf("MatchAllowPattern(%q, %q) = %v, want %v", tc.pattern, tc.model, got, tc.want)
		}
	}

	// The resolver's matcher is unchanged: a single `*` still stops at the `/`.
	if MatchModelPattern("*claude*", "anthropic/claude-sonnet-4") {
		t.Error("MatchModelPattern gained cross-segment wildcards; the resolver's capability glob must stay segment-local")
	}
	if !MatchModelPattern("anthropic/*", "anthropic/claude-sonnet-4") {
		t.Error("MatchModelPattern lost its own segment-local match")
	}
}

func TestIsDenyEntry(t *testing.T) {
	cases := []struct {
		entry string
		term  string
		ok    bool
	}{
		{"!opus", "opus", true},
		{"-haiku", "haiku", true},
		{"!  opus  ", "opus", true},
		{"!*opus*", "*opus*", true},
		{"opus", "", false},
		{"!", "", false},
		{"-", "", false},
		{"- ", "", false},
		{"", "", false},
		{"   ", "", false},
		// A model id that merely starts with the sigil is not a rule.
		{"gpt-4o", "", false},
	}
	for _, tc := range cases {
		term, ok := IsDenyEntry(tc.entry)
		if ok != tc.ok || term != tc.term {
			t.Errorf("IsDenyEntry(%q) = (%q, %v), want (%q, %v)", tc.entry, term, ok, tc.term, tc.ok)
		}
	}
}

func TestMatchDenyEntry(t *testing.T) {
	cases := []struct {
		term      string
		candidate string
		want      bool
	}{
		// A bare term is a substring rule, which is the whole point of `-haiku`.
		{"haiku", "anthropic/claude-haiku-4", true},
		{"opus", "google/opus-3", true},
		{"haiku", "anthropic/claude-sonnet-4", false},
		{"haiku", "HAIKU-4", true},
		// A term carrying `*` is a glob, anchored at the start of the id it is
		// compared against. Callers compare every spelling of the model, so
		// `gpt-*` still catches a request whose bare id is `gpt-4o`.
		{"*opus*", "google/opus-3", true},
		{"*opus*", "anthropic/claude-sonnet-4", false},
		{"gpt-*", "gpt-4o", true},
		{"gpt-*", "openai/gpt-4o", false},
		{"*gpt-*", "openai/gpt-4o", true},
		{"gpt-*", "anthropic/claude-sonnet-4", false},
		{"", "openai/gpt-4o", false},
		{"opus", "", false},
	}
	for _, tc := range cases {
		if got := MatchDenyEntry(tc.term, tc.candidate); got != tc.want {
			t.Errorf("MatchDenyEntry(%q, %q) = %v, want %v", tc.term, tc.candidate, got, tc.want)
		}
	}
}
