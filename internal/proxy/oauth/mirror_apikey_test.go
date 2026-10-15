package oauth

import "testing"

func TestSyncMirroredAPIKey(t *testing.T) {
	cases := []struct {
		name       string
		provider   string
		apiKey     string
		prevAccess string
		want       string
	}{
		// Login wrote the same token into both fields: rotate the copy.
		{"mirror rotates", "kiro", "old", "old", "new"},
		// An independent credential (iflow HMAC key, Kiro api_key) stays.
		{"independent key kept", "iflow", "platform-key", "old", "platform-key"},
		// Antigravity rows that already drifted heal on the next refresh.
		{"drifted antigravity heals", "antigravity", "older", "old", "new"},
		{"drifted gemini-cli heals", "gemini-cli", "older", "old", "new"},
		// No apiKey stored: nothing to add.
		{"empty apiKey untouched", "antigravity", "", "old", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			existing := map[string]any{"accessToken": tc.prevAccess}
			if tc.apiKey != "" {
				existing["apiKey"] = tc.apiKey
			}
			SyncMirroredAPIKey(existing, tc.provider, tc.prevAccess, "new")
			got, _ := existing["apiKey"].(string)
			if got != tc.want {
				t.Errorf("apiKey = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSyncMirroredAPIKey_EmptyNewTokenIsNoop(t *testing.T) {
	existing := map[string]any{"apiKey": "old", "accessToken": "old"}
	SyncMirroredAPIKey(existing, "antigravity", "old", "")
	if existing["apiKey"] != "old" {
		t.Errorf("an empty refreshed token must not clear apiKey, got %v", existing["apiKey"])
	}
}
