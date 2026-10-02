//go:build integration

package integration

import (
	"net/http"
	"testing"
	"time"

	"9router/proxy/internal/auth"
)

// TestHealthIsPublic pins that the liveness probe answers before any
// credential exists — a client that cannot authenticate still needs to know
// the process is up.
func TestHealthIsPublic(t *testing.T) {
	env := newEnv(t)

	res := env.Get(t, "/health", WithoutAPIKey())
	if res.Status != http.StatusOK {
		t.Fatalf("GET /health = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
	}
	var body struct {
		Status string `json:"status"`
	}
	res.Decode(t, &body)
	if body.Status != "ok" {
		t.Errorf("GET /health status = %q, want \"ok\"", body.Status)
	}
}

// TestEngineRoutesRejectMissingCredentials covers the three ways an engine
// request can arrive without a usable key. Each must be a 401, never a 200 and
// never a 405: a bypass here would serve provider calls to anonymous callers,
// and a wrong status would mean the request never reached the auth guard.
func TestEngineRoutesRejectMissingCredentials(t *testing.T) {
	env := newEnv(t)
	upstream := env.NewUpstream(t, chatCompletionResponder())
	env.AddConnection(t, "conn-deepseek", "deepseek", "DeepSeek Integration", upstream, "sk-upstream")

	if err := env.Repo.CreateApiKey("disabled-key", "sk-integration-disabled", "Disabled", ""); err != nil {
		t.Fatalf("seed disabled api key: %v", err)
	}
	if err := env.Repo.SetApiKeyStatus("disabled-key", false); err != nil {
		t.Fatalf("deactivate api key: %v", err)
	}

	tests := []struct {
		name    string
		present string
		method  string
		path    string
		body    any
	}{
		{
			name:    "no credential at all",
			present: "",
			method:  http.MethodPost,
			path:    "/v1/chat/completions",
			body:    ChatBody("deepseek/deepseek-chat", false),
		},
		{
			name:    "unknown client key",
			present: "sk-not-a-real-key",
			method:  http.MethodPost,
			path:    "/v1/chat/completions",
			body:    ChatBody("deepseek/deepseek-chat", false),
		},
		{
			name:    "deactivated client key",
			present: "sk-integration-disabled",
			method:  http.MethodGet,
			path:    "/v1/models",
			body:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := env.Do(t, tt.method, tt.path, tt.body, WithAPIKey(tt.present))
			if res.Status != http.StatusUnauthorized {
				t.Fatalf("%s %s = %d, want 401 (body: %s)", tt.method, tt.path, res.Status, truncate(res.Body))
			}
			// Clients render this envelope, so the 401 has to be typed as an
			// authentication failure rather than a generic bad request.
			var envelope struct {
				Error struct {
					Message string `json:"message"`
					Type    string `json:"type"`
					Code    string `json:"code"`
				} `json:"error"`
			}
			res.Decode(t, &envelope)
			if envelope.Error.Type != "authentication_error" {
				t.Errorf("%s %s error type = %q, want authentication_error", tt.method, tt.path, envelope.Error.Type)
			}
			if envelope.Error.Code != "invalid_api_key" {
				t.Errorf("%s %s error code = %q, want invalid_api_key", tt.method, tt.path, envelope.Error.Code)
			}
			if envelope.Error.Message == "" {
				t.Errorf("%s %s returned a 401 with no error message", tt.method, tt.path)
			}
		})
	}

	if got := upstream.Count(); got != 0 {
		t.Errorf("upstream received %d requests from rejected credentials, want 0", got)
	}
}

// TestAPIKeyIsAcceptedFromBearerAndHeader pins both credential carriers
// middleware.ExtractApiKey documents. Dropping either one silently breaks a
// client population that cannot be tested by hand.
func TestAPIKeyIsAcceptedFromBearerAndHeader(t *testing.T) {
	env := newEnv(t)
	upstream := env.NewUpstream(t, chatCompletionResponder())
	env.AddConnection(t, "conn-deepseek", "deepseek", "DeepSeek Integration", upstream, "sk-upstream")

	tests := []struct {
		name string
		opts []RequestOpt
	}{
		{
			name: "Authorization bearer",
			opts: []RequestOpt{WithAPIKey(env.APIKey)},
		},
		{
			name: "X-API-Key header",
			opts: []RequestOpt{WithAPIKey(""), WithHeader("X-API-Key", env.APIKey)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := env.Post(t, "/v1/chat/completions", ChatBody("deepseek/deepseek-chat", false), tt.opts...)
			if res.Status != http.StatusOK {
				t.Fatalf("POST /v1/chat/completions = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
			}
		})
	}

	if got := upstream.Count(); got != len(tests) {
		t.Errorf("upstream received %d requests, want %d", got, len(tests))
	}
}

// TestQueryParamKeyIsRejectedOnNonStream pins the deliberate asymmetry in
// middleware.ExtractApiKey: ?key= is honoured only on EventSource/SSE paths,
// because query strings leak into proxy logs and browser history. Widening it
// to every route would silently expose a client's key.
func TestQueryParamKeyIsRejectedOnNonStream(t *testing.T) {
	env := newEnv(t)

	res := env.Get(t, "/v1/models?key="+env.APIKey, WithoutAPIKey())
	if res.Status != http.StatusUnauthorized {
		t.Fatalf("GET /v1/models with ?key= = %d, want 401 (body: %s)", res.Status, truncate(res.Body))
	}
}

// TestV1AndUnversionedPathsReachTheSameHandler pins the path rewrite in
// middleware.RequestLogger: routes are registered unversioned and the /v1
// segment is stripped before chi matches, so both the documented
// OpenAI-compatible URL and the bare one must serve the same request end to
// end. If that middleware is ever dropped from the stack every documented URL
// 404s — a failure no amount of unit testing on the handler would reveal,
// because the handler is still correct.
func TestV1AndUnversionedPathsReachTheSameHandler(t *testing.T) {
	env, upstream := newProviderEnv(t)

	for _, path := range []string{"/v1/chat/completions", "/chat/completions"} {
		t.Run(path, func(t *testing.T) {
			res := env.Post(t, path, ChatBody("deepseek/deepseek-chat", false))
			if res.Status != http.StatusOK {
				t.Fatalf("POST %s = %d, want 200 (body: %s)", path, res.Status, truncate(res.Body))
			}
			if got := upstream.Last(t).Model(t); got != "deepseek-chat" {
				t.Errorf("POST %s reached the provider with model %q, want deepseek-chat", path, got)
			}
		})
	}

	if got := upstream.Count(); got != 2 {
		t.Errorf("upstream received %d requests, want 2: both URLs must reach the provider", got)
	}
}

// TestDashboardAuthGate pins the ways a dashboard call authenticates and the
// one that must not work. The rules differ from the engine routes:
// requireLogin defaults to true, a client API key is accepted for CLI
// compatibility, but an always-protected path refuses API keys outright.
func TestDashboardAuthGate(t *testing.T) {
	env := newEnv(t)
	// requireLogin is unset, so auth.RequireLogin reports true.
	if err := env.Repo.UpdateSettingsRaw(map[string]any{"requireLogin": true}); err != nil {
		t.Fatalf("set requireLogin: %v", err)
	}

	session, err := auth.Sign(auth.Secret(), time.Now())
	if err != nil {
		t.Fatalf("sign session token: %v", err)
	}
	cliToken := auth.CLIToken()

	tests := []struct {
		name     string
		opts     []RequestOpt
		wantCode int
	}{
		{
			name:     "anonymous is rejected",
			opts:     []RequestOpt{WithoutAPIKey()},
			wantCode: http.StatusUnauthorized,
		},
		{
			name:     "client api key is accepted for cli compatibility",
			opts:     nil,
			wantCode: http.StatusOK,
		},
		{
			name:     "dashboard session cookie is accepted",
			opts:     []RequestOpt{WithAPIKey(""), WithHeader("Cookie", auth.CookieName+"="+session)},
			wantCode: http.StatusOK,
		},
		{
			name:     "local cli token is accepted",
			opts:     []RequestOpt{WithAPIKey(""), WithHeader(auth.CLITokenHeader, cliToken)},
			wantCode: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := env.Get(t, "/api/connections", tt.opts...)
			if res.Status != tt.wantCode {
				t.Fatalf("GET /api/connections = %d, want %d (body: %s)", res.Status, tt.wantCode, truncate(res.Body))
			}
		})
	}

	t.Run("always-protected path refuses a client api key", func(t *testing.T) {
		res := env.Post(t, "/admin/health/reset?provider=deepseek&model=deepseek-chat", nil)
		if res.Status != http.StatusUnauthorized {
			t.Fatalf("POST /admin/health/reset with an api key = %d, want 401 (body: %s)",
				res.Status, truncate(res.Body))
		}
	})

	t.Run("requireLogin disabled opens the dashboard", func(t *testing.T) {
		if err := env.Repo.UpdateSettingsRaw(map[string]any{"requireLogin": false}); err != nil {
			t.Fatalf("disable requireLogin: %v", err)
		}
		res := env.Get(t, "/api/connections", WithoutAPIKey())
		if res.Status != http.StatusOK {
			t.Fatalf("GET /api/connections without credentials = %d, want 200 (body: %s)",
				res.Status, truncate(res.Body))
		}
	})
}
