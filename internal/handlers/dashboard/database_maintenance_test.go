package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"9router/proxy/internal/db"
	json "encoding/json/v2"
)

func TestHandleDatabaseMaintenance(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	if err := db.EnsureCoreSchema(repo.RawDB()); err != nil {
		t.Fatalf("EnsureCoreSchema: %v", err)
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

	// 4. Prune Usage Dry Run
	reqPruneUsageDry := httptest.NewRequest(http.MethodPost, "/api/settings/database/prune-usage?dryRun=1&days=30", nil)
	recPruneUsageDry := httptest.NewRecorder()
	router.ServeHTTP(recPruneUsageDry, reqPruneUsageDry)
	if recPruneUsageDry.Code != http.StatusOK {
		t.Fatalf("expected 200 from prune-usage dryRun, got %d: %s", recPruneUsageDry.Code, recPruneUsageDry.Body.String())
	}
	var dryRes map[string]any
	if err := json.Unmarshal(recPruneUsageDry.Body.Bytes(), &dryRes); err != nil {
		t.Fatalf("decode prune-usage dryRun: %v", err)
	}
	if dryRes["dryRun"] != true {
		t.Errorf("expected dryRun=true, got %v", dryRes["dryRun"])
	}

	// 5. Prune Usage Action (with body)
	bodyJSON := `{"days":30}`
	reqPruneUsage := httptest.NewRequest(http.MethodPost, "/api/settings/database/prune-usage", strings.NewReader(bodyJSON))
	recPruneUsage := httptest.NewRecorder()
	router.ServeHTTP(recPruneUsage, reqPruneUsage)
	if recPruneUsage.Code != http.StatusOK {
		t.Fatalf("expected 200 from prune-usage, got %d: %s", recPruneUsage.Code, recPruneUsage.Body.String())
	}
	var actRes map[string]any
	if err := json.Unmarshal(recPruneUsage.Body.Bytes(), &actRes); err != nil {
		t.Fatalf("decode prune-usage: %v", err)
	}
	if actRes["success"] != true || actRes["dryRun"] != false {
		t.Errorf("unexpected prune-usage response: %v", actRes)
	}

	// 6. Bad input rejected
	reqBad := httptest.NewRequest(http.MethodPost, "/api/settings/database/prune-usage?days=-1", nil)
	recBad := httptest.NewRecorder()
	router.ServeHTTP(recBad, reqBad)
	if recBad.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for negative days, got %d", recBad.Code)
	}

	// 7. Stats includes ledger bounds
	if _, ok := stats["usageHistoryOldestTimestamp"]; !ok {
		t.Errorf("missing usageHistoryOldestTimestamp in stats: %v", stats)
	}
	_ = time.Now()
}
