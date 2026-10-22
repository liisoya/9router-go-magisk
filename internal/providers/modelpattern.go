package providers

import (
	"regexp"
	"strings"
)

// MatchModelPattern reports whether a model id matches a single-segment glob
// pattern: `*` stands for any run of characters that does not cross a `/`, the
// whole string must match, and the comparison is case-insensitive.
//
// It is the exported door onto the resolver's own matcher (matchPattern, used
// for the per-model capability patterns) so callers outside this package cannot
// drift into a second glob with different semantics. path.Match backs both,
// which is why `*` is segment-local: `openai/*` covers `openai/gpt-4o` but not
// `openai/x/gpt-4o`.
func MatchModelPattern(pattern, modelID string) bool {
	return matchPattern(pattern, modelID)
}

// MatchAllowPattern reports whether a model id matches an ALLOWLIST pattern.
//
// It is deliberately not MatchModelPattern: an operator writing a per-key rule
// means "any model whose id mentions claude" when they write `*claude*`, and
// the resolver's segment-local glob never reaches `anthropic/claude-sonnet`
// because its single `*` cannot cross the `/` (#365, upstream parity with
// decolua/9router keyAccess.js). Here `*` spans separators.
//
// A pattern without `*` still matches whole-string and case-insensitively,
// which is exactly what MatchModelPattern answers for it, so the two matchers
// only disagree on patterns that carry a `*`. Anchored at both ends; `*` alone
// matches anything. The pattern is treated as a literal except for `*`, so it
// can never smuggle in regex syntax of its own.
func MatchAllowPattern(pattern, modelID string) bool {
	pattern = strings.TrimSpace(pattern)
	target := strings.TrimSpace(modelID)
	if pattern == "" || target == "" {
		return false
	}
	if !strings.Contains(pattern, "*") {
		return strings.EqualFold(pattern, target)
	}
	if pattern == "*" {
		return true
	}
	var b strings.Builder
	b.WriteString("(?i)^")
	for i, part := range strings.Split(pattern, "*") {
		if i > 0 {
			b.WriteString(".*")
		}
		b.WriteString(regexp.QuoteMeta(part))
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		// Unreachable: every literal run is quoted and `*` is the only
		// metacharacter left. Fail closed rather than allow on a parse error.
		return false
	}
	return re.MatchString(target)
}

// IsDenyEntry reports whether an allowlist entry is a deny rule (`!term` or
// `-term`) and returns the term without its sigil.
//
// A lone `!` or `-` is not a rule — it is a model id that happens to start with
// the sigil — so it stays a positive entry. An entry of only a sigil and spaces
// is neither, and callers read it as blank.
func IsDenyEntry(entry string) (string, bool) {
	trimmed := strings.TrimSpace(entry)
	if len(trimmed) < 2 {
		return "", false
	}
	if trimmed[0] != '!' && trimmed[0] != '-' {
		return "", false
	}
	term := strings.TrimSpace(trimmed[1:])
	if term == "" {
		return "", false
	}
	return term, true
}

// MatchDenyEntry reports whether a deny term rejects a candidate id.
//
// A term rejects on a glob match OR as a substring of the candidate, because an
// operator writing `-haiku` means to exclude every haiku model without spelling
// out an asterisk — upstream parity (#365).
func MatchDenyEntry(term, candidate string) bool {
	term = strings.TrimSpace(term)
	candidate = strings.TrimSpace(candidate)
	if term == "" || candidate == "" {
		return false
	}
	if MatchAllowPattern(term, candidate) {
		return true
	}
	return strings.Contains(strings.ToLower(candidate), strings.ToLower(term))
}
