package providers

import "testing"

// TestMergeAnthropicBeta covers the union the gateway's own flag list and the
// caller's are combined into. A client asking for a beta the gateway does not
// list used to be refused without ever being told why; dropping the flag
// instead is what upstream now does. Port of the upstream
// mergeAnthropicBeta unit behaviour.
func TestMergeAnthropicBeta(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   string
	}{
		{
			name:   "the caller's flags are appended",
			values: []string{"claude-code-20250219,effort-2025-11-24", "some-new-beta"},
			want:   "claude-code-20250219,effort-2025-11-24,some-new-beta",
		},
		{
			name:   "a flag both sides carry appears once",
			values: []string{"claude-code-20250219", "claude-code-20250219,other"},
			want:   "claude-code-20250219,other",
		},
		{
			name:   "whitespace and empty entries are dropped",
			values: []string{" a , ,b ", ""},
			want:   "a,b",
		},
		{name: "nothing at all", values: nil, want: ""},
		{name: "only empty values", values: []string{"", " , "}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MergeAnthropicBeta(tt.values...); got != tt.want {
				t.Errorf("MergeAnthropicBeta(%q) = %q, want %q", tt.values, got, tt.want)
			}
		})
	}
}

// TestWithHeader_DoesNotMutateSharedMap guards the reason both header helpers
// copy: the map in KnownProviders is shared by every request, so a
// request-scoped edit that mutated it would leak into later requests.
func TestWithHeader_DoesNotMutateSharedMap(t *testing.T) {
	shared := map[string]string{"Anthropic-Beta": "a"}

	got := WithHeader(shared, "x-claude-code-session-id", "sess-1")
	if got["x-claude-code-session-id"] != "sess-1" {
		t.Errorf("header not set on the copy: %v", got)
	}
	if _, leaked := shared["x-claude-code-session-id"]; leaked {
		t.Error("the shared registry map was mutated")
	}
	if got["Anthropic-Beta"] != "a" {
		t.Errorf("existing headers were lost: %v", got)
	}
}
