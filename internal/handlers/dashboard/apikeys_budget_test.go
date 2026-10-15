package dashboard

import (
	"bytes"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestUpdateApiKeyBudgetRoundTrip proves the two governance fields added
// alongside the per-window rates survive a PUT and are reflected in the list,
// and that an update omitting them leaves the stored values alone.
func TestUpdateApiKeyBudgetRoundTrip(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	if err := repo.UpdateSettingsRaw(map[string]any{"requireLogin": false}); err != nil {
		t.Fatalf("disable requireLogin: %v", err)
	}
	router := setupTestRouter(repo)

	id, _ := createTestKey(t, router, "Budget Key")

	// Seed a rate limit first so the "omitted field is preserved" assertion has
	// something to protect.
	seed, _ := json.Marshal(map[string]any{
		"rateLimitRpm":         60,
		"rateLimitTpm":         10000,
		"rateLimitConcurrency": 4,
		"tokenQuota":           500000,
		"queueTimeoutMs":       15000,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/keys/"+id, bytes.NewReader(seed))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var limits struct {
		Limits map[string]int `json:"limits"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &limits); err != nil {
		t.Fatalf("unmarshal update response: %v", err)
	}
	if limits.Limits["tokenQuota"] != 500000 {
		t.Errorf("response tokenQuota = %d, want 500000", limits.Limits["tokenQuota"])
	}
	if limits.Limits["queueTimeoutMs"] != 15000 {
		t.Errorf("response queueTimeoutMs = %d, want 15000", limits.Limits["queueTimeoutMs"])
	}

	row := listKeys(t, router)
	if got := intFromJSON(row["tokenQuota"]); got != 500000 {
		t.Errorf("listed tokenQuota = %d, want 500000", got)
	}
	if got := intFromJSON(row["queueTimeoutMs"]); got != 15000 {
		t.Errorf("listed queueTimeoutMs = %d, want 15000", got)
	}

	// A partial update that only renames must not wipe the budget columns or
	// the rate limits: every policy field is optional by design.
	rename, _ := json.Marshal(map[string]any{"name": "Renamed Key"})
	req = httptest.NewRequest(http.MethodPut, "/api/keys/"+id, bytes.NewReader(rename))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	row = listKeys(t, router)
	if got := intFromJSON(row["tokenQuota"]); got != 500000 {
		t.Errorf("tokenQuota after rename = %d, want 500000 (must be preserved)", got)
	}
	if got := intFromJSON(row["queueTimeoutMs"]); got != 15000 {
		t.Errorf("queueTimeoutMs after rename = %d, want 15000 (must be preserved)", got)
	}
	if got := intFromJSON(row["rateLimitRpm"]); got != 60 {
		t.Errorf("rateLimitRpm after rename = %d, want 60 (must be preserved)", got)
	}
	if got := intFromJSON(row["rateLimitConcurrency"]); got != 4 {
		t.Errorf("rateLimitConcurrency after rename = %d, want 4 (must be preserved)", got)
	}
}

// TestUpdateApiKeyRejectsNegativeBudget proves a negative quota or queue timeout
// is refused rather than persisted as something the limiter would read as
// "unlimited" while the dashboard showed a configured limit.
func TestUpdateApiKeyRejectsNegativeBudget(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	if err := repo.UpdateSettingsRaw(map[string]any{"requireLogin": false}); err != nil {
		t.Fatalf("disable requireLogin: %v", err)
	}
	router := setupTestRouter(repo)

	id, _ := createTestKey(t, router, "Negative Key")

	for _, body := range []map[string]any{
		{"tokenQuota": -1},
		{"queueTimeoutMs": -5},
	} {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPut, "/api/keys/"+id, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %v: status = %d, want 400", body, rec.Code)
		}
	}
}

// intFromJSON reads a JSON number as an int, treating absent/null as 0.
func intFromJSON(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	}
	return 0
}
