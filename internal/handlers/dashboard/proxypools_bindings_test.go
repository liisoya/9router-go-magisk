package dashboard

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	"9router/proxy/internal/db"
)

func TestProxyPoolBindings_CountProviderStrategy(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	if _, err := repo.RawDB().Exec(`CREATE TABLE IF NOT EXISTS proxyPools (
		id TEXT PRIMARY KEY,
		isActive INTEGER DEFAULT 1,
		testStatus TEXT,
		data TEXT NOT NULL,
		createdAt TEXT NOT NULL,
		updatedAt TEXT NOT NULL
	);`); err != nil {
		t.Fatalf("create proxyPools: %v", err)
	}

	pool, err := repo.InsertProxyPool(db.ProxyPoolData{
		Name: "warp", ProxyURL: "http://172.17.0.1:8001", Type: "http",
	})
	if err != nil {
		t.Fatalf("insert pool: %v", err)
	}
	poolID, _ := pool["id"].(string)

	if err := repo.UpdateSettingsRaw(map[string]any{
		"providerStrategies": map[string]any{
			"opencode": map[string]any{"rotateStrategy": "round-robin"},
		},
	}); err != nil {
		t.Fatalf("seed rotation strategy: %v", err)
	}
	if err := repo.SetProviderStrategy("opencode", db.ProviderStrategy{
		ProxyPoolID: poolID, RotateStrategy: "round-robin",
	}); err != nil {
		t.Fatalf("bind pool to provider: %v", err)
	}

	router := setupTestRouter(repo)
	req := httptest.NewRequest(http.MethodGet, "/api/proxy-pools?includeUsage=true", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list pools expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		ProxyPools []map[string]any `json:"proxyPools"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode pools: %v", err)
	}
	if len(body.ProxyPools) != 1 {
		t.Fatalf("want 1 pool, got %d", len(body.ProxyPools))
	}
	if got, _ := body.ProxyPools[0]["boundConnectionCount"].(float64); got != 1 {
		t.Fatalf("boundConnectionCount = %v, want 1 (provider-level binding)", got)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/proxy-pools/"+poolID, nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete provider-bound pool expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}
