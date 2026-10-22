package handlerutil

import (
	"testing"
	"unicode/utf8"
)

// TestMaskAPIKeyStaysValidUTF8 guards the same byte-slicing defect that
// corrupted GET /api/connections: MaskAPIKey is persisted into usage rows and
// then serialized back out, so a cut taken inside a multi-byte rune yields
// invalid UTF-8 that encoding/json/v2 rejects mid-encode.
func TestMaskAPIKeyStaysValidUTF8(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want string
	}{
		{name: "ascii key unchanged shape", key: "sk-machineid-abcdef", want: "sk-machi***cdef"},
		{name: "too short collapses", key: "short", want: "***"},
		{name: "rune straddling the tail cut", key: "sk-machineid-abcdef😀z", want: "sk-machi***ef😀z"},
		{name: "rune straddling the head cut", key: "日本語の接続名abcdefghijklmnop", want: "日本語の接続名a***mnop"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MaskAPIKey(tt.key)
			if !utf8.ValidString(got) {
				t.Errorf("MaskAPIKey(%q) = %q, invalid UTF-8", tt.key, got)
			}
			if got != tt.want {
				t.Errorf("MaskAPIKey(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestRunePrefixSuffixRejectsShortInput(t *testing.T) {
	tests := []struct {
		name string
		fn   func() string
		want string
	}{
		{name: "prefix beyond length returns whole", fn: func() string { return RunePrefix("abc", 10) }, want: "abc"},
		{name: "suffix beyond length returns whole", fn: func() string { return RuneSuffix("abc", 10) }, want: "abc"},
		{name: "prefix cuts on runes", fn: func() string { return RunePrefix("日本語", 2) }, want: "日本"},
		{name: "suffix cuts on runes", fn: func() string { return RuneSuffix("日本語", 2) }, want: "本語"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.fn(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
