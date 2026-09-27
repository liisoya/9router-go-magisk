package chat

import (
	"strings"
	"testing"
)

// TestExtractClaudeSessionIdFromUserId covers the id the cloak writes into
// metadata.user_id being read back out so it can be echoed in
// x-claude-code-session-id. The CLI's own "claude:" prefix must be stripped
// either way, or the API sees two different sessions for one request.
func TestExtractClaudeSessionIdFromUserId(t *testing.T) {
	tests := []struct {
		name   string
		userID string
		want   string
	}{
		{
			name:   "the json object the cloak generates",
			userID: `{"device_id":"d","account_uuid":"a","session_id":"11111111-2222-3333-4444-555555555555"}`,
			want:   "11111111-2222-3333-4444-555555555555",
		},
		{
			name:   "a bare id",
			userID: "abcdef-123456",
			want:   "abcdef-123456",
		},
		{
			name:   "the cli prefix is stripped from a bare id",
			userID: "claude:abcdef-123456",
			want:   "abcdef-123456",
		},
		{
			name:   "the prefix is matched case-insensitively",
			userID: "CLAUDE:abcdef-123456",
			want:   "abcdef-123456",
		},
		{
			// The prefix is matched at the start only, exactly like upstream's
			// anchored replace: leading space is trimmed *after* the match, so a
			// padded prefix survives. Pinned so the behaviour cannot drift.
			name:   "leading space is trimmed after the prefix match",
			userID: "  Claude:  abcdef-123456 ",
			want:   "Claude:  abcdef-123456",
		},
		{
			name:   "malformed json yields nothing rather than the raw text",
			userID: `{"session_id":`,
			want:   "",
		},
		{name: "an empty id", userID: "", want: ""},
		{name: "a prefix with nothing behind it", userID: "claude:   ", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractClaudeSessionIdFromUserId(tt.userID); got != tt.want {
				t.Errorf("extractClaudeSessionIdFromUserId(%q) = %q, want %q", tt.userID, got, tt.want)
			}
		})
	}
}

// TestGenerateFakeUserID_StripsCLIPrefix pins the other half: the session id
// baked into metadata.user_id is already clean, so the header and the body
// agree without a second pass.
func TestGenerateFakeUserID_StripsCLIPrefix(t *testing.T) {
	withPrefix := generateFakeUserID("claude:session-abc", "key")
	if !strings.Contains(withPrefix, `"session_id":"session-abc"`) {
		t.Errorf("user id kept the CLI prefix: %s", withPrefix)
	}
	if got := extractClaudeSessionIdFromUserId(withPrefix); got != "session-abc" {
		t.Errorf("session read back = %q, want session-abc", got)
	}

	// An id that is only the prefix falls back to a generated uuid rather than
	// producing an empty session.
	if generated := generateFakeUserID("claude:", "key"); strings.Contains(generated, `"session_id":""`) {
		t.Errorf("empty session was not replaced: %s", generated)
	}
}

// TestClaudeSessionIDFromBody is the wiring: the id travels body → header.
func TestClaudeSessionIDFromBody(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "a cloaked body",
			body: `{"metadata":{"user_id":"{\"session_id\":\"sess-1\"}"}}`,
			want: "sess-1",
		},
		{
			name: "a body with a bare user id",
			body: `{"metadata":{"user_id":"sess-2"}}`,
			want: "sess-2",
		},
		{name: "no metadata", body: `{"model":"claude"}`, want: ""},
		{name: "no user id", body: `{"metadata":{}}`, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := claudeSessionIDFromBody([]byte(tt.body)); got != tt.want {
				t.Errorf("claudeSessionIDFromBody = %q, want %q", got, tt.want)
			}
		})
	}
}
