package chat

import "testing"

// Antigravity's login writes the first access token into apiKey too, and a
// refresh rotates only accessToken. extractAPIKey prefers apiKey, so the stale
// copy went out on every request: 401, force refresh, retry — every turn.
// Access-token-only providers must send the access token.
func TestResolveProviderAuthToken_AccessTokenOnlyProviders(t *testing.T) {
	for _, provider := range []string{"antigravity", "gemini-cli"} {
		t.Run(provider+" prefers the refreshed access token", func(t *testing.T) {
			conn := &ConnectionData{APIKey: "stale-copy", AccessToken: "fresh-token"}
			if got := resolveProviderAuthToken(provider, conn, extractAPIKey(conn)); got != "fresh-token" {
				t.Errorf("got %q, want fresh-token", got)
			}
		})
		t.Run(provider+" without access token keeps current", func(t *testing.T) {
			conn := &ConnectionData{APIKey: "only-key"}
			if got := resolveProviderAuthToken(provider, conn, "only-key"); got != "only-key" {
				t.Errorf("got %q, want only-key", got)
			}
		})
	}
}
