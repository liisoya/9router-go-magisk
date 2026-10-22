package handlerutil

import "unicode/utf8"

// RunePrefix returns the first n runes of s, and RuneSuffix the last n.
//
// The cut is taken in runes, never in bytes. A byte slice can land inside a
// multi-byte character and leave a dangling continuation byte, and
// encoding/json/v2 rejects invalid UTF-8 — but json.MarshalWrite streams the
// response out as it encodes, so the 200 header and everything before the bad
// value are already on the wire. The handler then appends its error sentinel to
// a half-written body, the client fails to parse it, and a populated list
// renders as empty (issue: dashboard showed 0/0 connections because a masked
// API key ended on a multi-byte rune).
//
// A requested length longer than the string returns s unchanged, so callers
// never panic on a short input.
func RunePrefix(s string, n int) string {
	r := []rune(s)
	if n >= len(r) {
		return s
	}
	return string(r[:n])
}

// RuneSuffix is the trailing half of RunePrefix; see it for why runes.
func RuneSuffix(s string, n int) string {
	r := []rune(s)
	if n >= len(r) {
		return s
	}
	return string(r[len(r)-n:])
}

// RuneCount reports how many runes s holds, for length gates that must agree
// with the rune-based cuts above rather than with byte length.
func RuneCount(s string) int {
	return utf8.RuneCountInString(s)
}
