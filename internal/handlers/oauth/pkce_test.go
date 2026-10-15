package oauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlePKCEAuthorize_shapes(t *testing.T) {
	handler := NewOAuthHandler(nil)
	cases := map[string][]string{
		"claude": {"claude.ai/oauth/authorize", "code_challenge=", "code_challenge_method=S256", "code=true", "9d1c250a"},
		"codex":  {"auth.openai.com/oauth/authorize", "code_challenge=", "originator=codex_cli_rs", "app_EMoamEEZ73f0CkXaXp7hrann"},
		"xai":    {"auth.x.ai", "code_challenge=", "plan=generic", "referrer=cli-proxy-api"},
		"gitlab": {"gitlab.com/oauth/authorize", "code_challenge=", "scope=api"},
	}
	for provider, wants := range cases {
		req := httptest.NewRequest("GET", "/api/oauth/pkce/authorize?provider="+provider, nil)
		rec := httptest.NewRecorder()
		handler.HandlePKCEAuthorize(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d: %s", provider, rec.Code, rec.Body.String())
		}
		for _, want := range wants {
			if !strings.Contains(rec.Body.String(), want) {
				t.Errorf("%s: response missing %q: %s", provider, want, rec.Body.String())
			}
		}
	}
}

func TestHandlePKCEAuthorize_badProvider(t *testing.T) {
	handler := NewOAuthHandler(nil)
	req := httptest.NewRequest("GET", "/api/oauth/pkce/authorize?provider=google", nil)
	rec := httptest.NewRecorder()
	handler.HandlePKCEAuthorize(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandlePKCEExchange_variants(t *testing.T) {
	// Claude: JSON exchange body.
	claudeMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("claude: want JSON, got %s", ct)
		}
		w.Write([]byte(`{"access_token":"ca","refresh_token":"cr","expires_in":3600,"scope":"s"}`))
	}))
	defer claudeMock.Close()
	// Codex: form exchange body. The mock returns an id_token carrying the
	// account claims the real endpoint provides; without them the login now
	// fails fast (see validateCodexConnectionData).
	idToken := makeIDToken(t, map[string]any{
		"email": "dev@example.com",
		codexAuthClaimKey: map[string]any{
			"chatgpt_account_id": "acct-1",
		},
	})
	codexMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("codex: want form, got %s", ct)
		}
		w.Write([]byte(`{"access_token":"xa","refresh_token":"xr","expires_in":3600,"id_token":"` + idToken + `"}`))
	}))
	defer codexMock.Close()

	oldClaude, oldCodex := pkceProviders["claude"], pkceProviders["codex"]
	pkceProviders["claude"] = &pkceConfig{providers: []string{"claude"}, clientID: "cid", tokenURL: claudeMock.URL, scope: "s", exchangeJSON: true, includeState: true, connPrefix: "claude-", display: "Claude"}
	pkceProviders["codex"] = &pkceConfig{providers: []string{"codex"}, clientID: "cid", tokenURL: codexMock.URL, scope: "s", connPrefix: "cx-", display: "Codex", emailFromIDTok: true}
	defer func() { pkceProviders["claude"], pkceProviders["codex"] = oldClaude, oldCodex }()

	handler := NewOAuthHandler(nil)
	for _, tc := range []struct{ provider, payload string }{
		{"claude", `{"provider":"claude","code":"c","codeVerifier":"v","state":"s"}`},
		{"codex", `{"provider":"codex","code":"c","codeVerifier":"v"}`},
	} {
		req := httptest.NewRequest("POST", "/api/oauth/pkce/exchange", strings.NewReader(tc.payload))
		rec := httptest.NewRecorder()
		handler.HandlePKCEExchange(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d: %s", tc.provider, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"status":"authorized"`) {
			t.Errorf("%s: not authorized: %s", tc.provider, rec.Body.String())
		}
	}
}
