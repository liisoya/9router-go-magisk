package dashboard

import (
	"testing"
	"unicode/utf8"

	json "encoding/json/v2"
)

// TestMaskOutputStaysValidUTF8 pins the failure mode behind a corrupted
// GET /api/connections: json.MarshalWrite streams the response as it encodes,
// so once the 200 header is out, a value holding invalid UTF-8 aborts encoding
// and the handler appends its error sentinel to a half-written body. The client
// cannot parse it, and a populated connection list renders as 0/0.
//
// The masks used to cut by byte (secret[len-4:], name[:8]), which lands inside
// a multi-byte rune whenever the key or name does not end on an ASCII byte.
func TestMaskOutputStaysValidUTF8(t *testing.T) {
	tests := []struct {
		name string
		run  func() string
	}{
		{
			// 4-byte rune straddles the len-4 cut point.
			name: "secret ending mid-rune",
			run:  func() string { return maskConnectionSecret("sk-abcdefghijklmnop😀z") },
		},
		{
			// Multi-byte runes inside the first 8 bytes, plus a 32+ ASCII run
			// so the token branch (and its name[:8] cut) is actually taken.
			name: "token-like name starting multi-byte",
			run: func() string {
				return maskConnectionName("日本語の接続名aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
			},
		},
		{
			name: "client key ending mid-rune",
			run:  func() string { return maskClientKey("sk-abcdefghijklmnop😀z") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.run()
			if !utf8.ValidString(got) {
				t.Errorf("mask produced invalid UTF-8: %q", got)
			}
			if _, err := json.Marshal(map[string]any{"masked": got}); err != nil {
				t.Errorf("marshal fails, which corrupts the response body: %v", err)
			}
		})
	}
}
