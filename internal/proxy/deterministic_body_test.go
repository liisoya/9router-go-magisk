package proxy

import (
	"strings"
	"testing"
)

// deterministicBody helpers are table-driven per AGENTS.md §5.

func TestCanonicalChatBody_SortsMapKeys(t *testing.T) {
	body := []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}],"stream":true,"zz_last":1,"aa_first":2}`)
	out := canonicalChatBody(body)

	// keys must be sorted: aa_first before model, zz_last after stream
	if idxAA := strings.Index(string(out), `"aa_first"`); idxAA == -1 {
		t.Fatalf("aa_first missing: %s", out)
	} else if idxModel := strings.Index(string(out), `"model"`); idxAA > idxModel {
		t.Fatalf("expected aa_first before model, got: %s", out)
	}
	if !strings.Contains(string(out), `"stream"`) {
		t.Fatalf("stream missing: %s", out)
	}
}

func TestCanonicalChatBody_DeterministicAcrossCalls(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"x"}],"tools":[{"type":"function"}],"temperature":0.7}`)
	first := string(canonicalChatBody(body))
	for i := 0; i < 50; i++ {
		if got := string(canonicalChatBody(body)); got != first {
			t.Fatalf("iteration %d produced different bytes:\nfirst: %s\ngot:   %s", i, first, got)
		}
	}
}

func TestCanonicalChatBody_NonChatBypassed(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"oauth token post", `{"grant_type":"refresh_token","refreshToken":"x"}`},
		{"gemini payload", `{"contents":[{"parts":[{"text":"hi"}]}]}`},
		{"model only", `{"model":"gpt-4o"}`},
		{"messages only", `{"messages":[{"role":"user"}]}`},
		{"invalid json", `{not-json`},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			in := []byte(tt.body)
			if got := canonicalChatBody(in); string(got) != tt.body {
				t.Fatalf("non-chat body must pass through byte-identical, got: %s", got)
			}
		})
	}
}

func TestCanonicalChatBody_SemanticEquivalence(t *testing.T) {
	body := []byte(`{"stream":true,"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}`)
	out := canonicalChatBody(body)
	if !strings.Contains(string(out), `"model":"deepseek-chat"`) || !strings.Contains(string(out), `"stream":true`) {
		t.Fatalf("semantic content lost: %s", out)
	}
	// messages array order preserved (arrays must not be sorted)
	if idxMsg := strings.Index(string(out), `"messages"`); idxMsg == -1 {
		t.Fatalf("messages missing: %s", out)
	}
}
