package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"9router/proxy/internal/auth"
	"9router/proxy/internal/db"
)

func okHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
}

func TestRequireDashboardAuth(t *testing.T) {
	t.Setenv("JWT_SECRET", "middleware-test-secret")
	t.Setenv("DATA_DIR", t.TempDir())
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)

	gate := RequireDashboardAuth(repo)(okHandler())

	serve := func(req *http.Request) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		gate.ServeHTTP(rec, req)
		return rec
	}

	// No credentials, requireLogin unset (defaults on) → rejected.
	if rec := serve(httptest.NewRequest(http.MethodGet, "/api/connections", nil)); rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without credentials, got %d", rec.Code)
	}

	// Seeded valid API key bypasses the gate (CLI / built-in dashboard key).
	apiKeyReq := httptest.NewRequest(http.MethodGet, "/api/connections", nil)
	apiKeyReq.Header.Set("Authorization", "Bearer valid-token")
	if rec := serve(apiKeyReq); rec.Code != http.StatusOK {
		t.Errorf("expected valid API key to pass, got %d", rec.Code)
	}

	// The derived CLI token bypasses the gate; an arbitrary value must not.
	cliReq := httptest.NewRequest(http.MethodGet, "/api/connections", nil)
	cliReq.Header.Set(auth.CLITokenHeader, auth.CLIToken())
	if rec := serve(cliReq); rec.Code != http.StatusOK {
		t.Errorf("expected CLI token to pass, got %d", rec.Code)
	}
	bogusReq := httptest.NewRequest(http.MethodGet, "/api/connections", nil)
	bogusReq.Header.Set(auth.CLITokenHeader, "any-local-cli-token")
	if rec := serve(bogusReq); rec.Code != http.StatusUnauthorized {
		t.Errorf("expected bogus CLI token to be rejected, got %d", rec.Code)
	}

	// A tampered cookie is not a session.
	badCookieReq := httptest.NewRequest(http.MethodGet, "/api/connections", nil)
	badCookieReq.AddCookie(&http.Cookie{Name: auth.CookieName, Value: "not-a-token"})
	if rec := serve(badCookieReq); rec.Code != http.StatusUnauthorized {
		t.Errorf("expected bad cookie to be rejected, got %d", rec.Code)
	}

	// A real signed cookie passes.
	token, err := auth.Sign(auth.Secret(), time.Now())
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	goodCookieReq := httptest.NewRequest(http.MethodGet, "/api/connections", nil)
	goodCookieReq.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
	if rec := serve(goodCookieReq); rec.Code != http.StatusOK {
		t.Errorf("expected signed cookie to pass, got %d", rec.Code)
	}

	// requireLogin disabled → open, no credentials needed.
	if err := repo.UpdateSettingsRaw(map[string]any{"requireLogin": false}); err != nil {
		t.Fatalf("update settings: %v", err)
	}
	if rec := serve(httptest.NewRequest(http.MethodGet, "/api/connections", nil)); rec.Code != http.StatusOK {
		t.Errorf("expected open dashboard when requireLogin=false, got %d", rec.Code)
	}
	// Always-protected routes reject client API key even if key is valid.
	apiDbReq := httptest.NewRequest(http.MethodGet, "/api/settings/database", nil)
	apiDbReq.Header.Set("Authorization", "Bearer valid-token")
	if rec := serve(apiDbReq); rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 when API key accesses always-protected route, got %d", rec.Code)
	}

	// Always-protected routes reject unauthenticated even when requireLogin=false.
	anonDbReq := httptest.NewRequest(http.MethodGet, "/api/settings/database", nil)
	if rec := serve(anonDbReq); rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for always-protected route even when requireLogin=false, got %d", rec.Code)
	}

	// Always-protected routes pass with valid session cookie.
	cookieDbReq := httptest.NewRequest(http.MethodGet, "/api/settings/database", nil)
	cookieDbReq.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
	if rec := serve(cookieDbReq); rec.Code != http.StatusOK {
		t.Errorf("expected 200 for always-protected route with signed cookie, got %d", rec.Code)
	}

	// Always-protected routes pass with valid CLI token.
	cliDbReq := httptest.NewRequest(http.MethodGet, "/api/settings/database", nil)
	cliDbReq.Header.Set(auth.CLITokenHeader, auth.CLIToken())
	if rec := serve(cliDbReq); rec.Code != http.StatusOK {
		t.Errorf("expected 200 for always-protected route with CLI token, got %d", rec.Code)
	}
}

func TestRequireConsoleLogAuth(t *testing.T) {
	t.Setenv("JWT_SECRET", "middleware-test-secret")
	t.Setenv("DATA_DIR", t.TempDir())
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	gate := RequireConsoleLogAuth(repo)(okHandler())

	anonymous := httptest.NewRequest(http.MethodGet, "/api/translator/console-logs", nil)
	rec := httptest.NewRecorder()
	gate.ServeHTTP(rec, anonymous)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous console status = %d", rec.Code)
	}

	keyReq := httptest.NewRequest(http.MethodGet, "/api/translator/console-logs", nil)
	keyReq.Header.Set("Authorization", "Bearer valid-token")
	rec = httptest.NewRecorder()
	gate.ServeHTTP(rec, keyReq)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("client API key console status = %d", rec.Code)
	}

	token, err := auth.Sign(auth.Secret(), time.Now())
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	cookieReq := httptest.NewRequest(http.MethodGet, "/api/translator/console-logs", nil)
	cookieReq.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
	rec = httptest.NewRecorder()
	gate.ServeHTTP(rec, cookieReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard session console status = %d", rec.Code)
	}
}

func TestRequireDashboardAuth_ProtectsAntigravityExchange(t *testing.T) {
	t.Setenv("JWT_SECRET", "middleware-test-secret")
	t.Setenv("DATA_DIR", t.TempDir())
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	gate := RequireDashboardAuth(repo)(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/antigravity/exchange", nil)
	rec := httptest.NewRecorder()
	gate.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, expected 401", rec.Code)
	}
}

func TestRequireAdminAuth(t *testing.T) {
	t.Setenv("JWT_SECRET", "middleware-test-secret")
	t.Setenv("DATA_DIR", t.TempDir())

	gate := RequireAdminAuth()(okHandler())
	serve := func(req *http.Request) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		gate.ServeHTTP(rec, req)
		return rec
	}

	// Unauthenticated → 401
	if rec := serve(httptest.NewRequest(http.MethodPost, "/api/version/shutdown", nil)); rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauthenticated shutdown, got %d", rec.Code)
	}

	// API key → 401 (API keys cannot perform admin shutdown/update)
	apiKeyReq := httptest.NewRequest(http.MethodPost, "/api/version/shutdown", nil)
	apiKeyReq.Header.Set("Authorization", "Bearer any-key")
	if rec := serve(apiKeyReq); rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for API key shutdown, got %d", rec.Code)
	}

	// Valid CLI token → 200 OK
	cliReq := httptest.NewRequest(http.MethodPost, "/api/version/shutdown", nil)
	cliReq.Header.Set(auth.CLITokenHeader, auth.CLIToken())
	if rec := serve(cliReq); rec.Code != http.StatusOK {
		t.Errorf("expected 200 for CLI token shutdown, got %d", rec.Code)
	}

	// Valid signed session cookie → 200 OK
	token, err := auth.Sign(auth.Secret(), time.Now())
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	cookieReq := httptest.NewRequest(http.MethodPost, "/api/version/shutdown", nil)
	cookieReq.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
	if rec := serve(cookieReq); rec.Code != http.StatusOK {
		t.Errorf("expected 200 for session cookie shutdown, got %d", rec.Code)
	}
}

func TestRequireDashboardPage_Redirects(t *testing.T) {
	t.Setenv("JWT_SECRET", "middleware-test-secret")
	t.Setenv("DATA_DIR", t.TempDir())
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)

	page := RequireDashboardPage(repo)(okHandler())

	rec := httptest.NewRecorder()
	page.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302 to /login, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("expected Location /login, got %q", loc)
	}

	// With requireLogin off the page is served directly.
	if err := repo.UpdateSettingsRaw(map[string]any{"requireLogin": false}); err != nil {
		t.Fatalf("update settings: %v", err)
	}
	rec = httptest.NewRecorder()
	page.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 when requireLogin=false, got %d", rec.Code)
	}
}
