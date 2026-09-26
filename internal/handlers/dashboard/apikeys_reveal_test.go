package dashboard

import (
	"bytes"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestApiKeysRevealForDashboardSession(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	createBody := map[string]any{"name": "Reveal Key"}
	bodyBytes, _ := json.Marshal(createBody)
	req := httptest.NewRequest(http.MethodPost, "/api/keys", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create apiKey expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	full, _ := created["key"].(string)
	if full == "" {
		t.Fatalf("expected full key at creation, got %v", created)
	}

	// Upstream parity: a dashboard caller without requireLogin sees the same
	// full value, so media Run can use it directly as Bearer credential.
	if err := repo.UpdateSettingsRaw(map[string]any{"requireLogin": false}); err != nil {
		t.Fatalf("disable login: %v", err)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/keys", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %s", rec.Body.String())
	}
	var listed []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("unmarshal keys: %v", err)
	}
	if len(listed) != 1 || listed[0]["key"] != full {
		t.Fatalf("expected listed full key %q, got %v", full, listed)
	}
}
