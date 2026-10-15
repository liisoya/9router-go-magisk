package dashboard

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleDatabaseMaintenance(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := repo.RawDB().Exec(`
		CREATE TABLE IF NOT EXISTS requestDetails (
			id TEXT PRIMARY KEY,
			provider TEXT,
			model TEXT,
			connectionId TEXT,
			status TEXT,
			data TEXT,
			timestamp TEXT
		);
		CREATE TABLE IF NOT EXISTS usageHistory (
			id TEXT PRIMARY KEY,
			timestamp TEXT
		);
	`)
	if err != nil {
		t.Fatalf("create test tables: %v", err)
	}

	router := setupTestRouter(repo)

	// 1. Stats
	reqStats := httptest.NewRequest(http.MethodGet, "/api/settings/database/stats", nil)
	recStats := httptest.NewRecorder()
	router.ServeHTTP(recStats, reqStats)

	if recStats.Code != http.StatusOK {
		t.Fatalf("expected 200 from stats, got %d: %s", recStats.Code, recStats.Body.String())
	}
	var stats map[string]any
	if err := json.Unmarshal(recStats.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	if _, ok := stats["databaseSizeBytes"]; !ok {
		t.Errorf("missing databaseSizeBytes in stats: %v", stats)
	}

	// 2. Prune
	reqPrune := httptest.NewRequest(http.MethodPost, "/api/settings/database/prune", nil)
	recPrune := httptest.NewRecorder()
	router.ServeHTTP(recPrune, reqPrune)

	if recPrune.Code != http.StatusOK {
		t.Fatalf("expected 200 from prune, got %d: %s", recPrune.Code, recPrune.Body.String())
	}

	// 3. Vacuum
	reqVac := httptest.NewRequest(http.MethodPost, "/api/settings/database/vacuum", nil)
	recVac := httptest.NewRecorder()
	router.ServeHTTP(recVac, reqVac)

	if recVac.Code != http.StatusOK {
		t.Fatalf("expected 200 from vacuum, got %d: %s", recVac.Code, recVac.Body.String())
	}
}
