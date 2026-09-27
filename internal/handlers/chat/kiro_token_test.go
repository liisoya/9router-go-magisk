package chat

import (
	"testing"
)

// A Kiro connection can carry BOTH an apiKey and an OAuth accessToken. Sending
// the apiKey as Authorization made CodeWhisperer answer 403 "The bearer token
// included in the request is invalid." even though the access token worked —
// upstream (open-sse/executors/kiro.js buildHeaders) only uses the apiKey for
// `authMethod: "api_key"` connections.
func TestResolveProviderAuthToken_Kiro(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		conn     *ConnectionData
		current  string
		want     string
	}{
		{
			name:     "imported connection prefers the access token",
			provider: "kiro",
			conn: &ConnectionData{
				APIKey:               "stale-api-key",
				AccessToken:          "valid-access-token",
				ProviderSpecificData: map[string]any{"authMethod": "imported"},
			},
			current: "stale-api-key",
			want:    "valid-access-token",
		},
		{
			name:     "builder-id connection prefers the access token",
			provider: "kiro",
			conn: &ConnectionData{
				APIKey:               "stale-api-key",
				AccessToken:          "valid-access-token",
				ProviderSpecificData: map[string]any{"authMethod": "builder-id"},
			},
			current: "stale-api-key",
			want:    "valid-access-token",
		},
		{
			name:     "api_key connection keeps the api key",
			provider: "kiro",
			conn: &ConnectionData{
				APIKey:               "kc-real-key",
				AccessToken:          "ignored-token",
				ProviderSpecificData: map[string]any{"authMethod": "api_key"},
			},
			current: "kc-real-key",
			want:    "kc-real-key",
		},
		{
			name:     "access token only",
			provider: "kiro",
			conn:     &ConnectionData{AccessToken: "only-token"},
			want:     "only-token",
		},
		{
			name:     "api key only",
			provider: "kiro",
			conn:     &ConnectionData{APIKey: "only-key"},
			want:     "only-key",
		},
		{
			name:     "neither falls back to the current token",
			provider: "kiro",
			conn:     &ConnectionData{},
			current:  "default-api-key",
			want:     "default-api-key",
		},
		{
			name:     "other providers are untouched",
			provider: "codex",
			conn:     &ConnectionData{APIKey: "sk-key", AccessToken: "token"},
			current:  "sk-key",
			want:     "sk-key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveProviderAuthToken(tt.provider, tt.conn, tt.current); got != tt.want {
				t.Errorf("resolveProviderAuthToken(%q) = %q, want %q", tt.provider, got, tt.want)
			}
		})
	}
}
