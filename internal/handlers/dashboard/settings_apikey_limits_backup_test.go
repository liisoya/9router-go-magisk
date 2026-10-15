package dashboard

import (
	"bytes"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	"9router/proxy/internal/auth"
)

// TestBackupPreservesApiKeyLimits proves a database backup carries every
// per-key governance column — the per-window rates and the two this change
// added — through export and import. A backup that dropped them would restore
// every key as unrestricted, silently opening the throttle the operator set.
func TestBackupPreservesApiKeyLimits(t *testing.T) {
	authTestEnv(t)
	repo, cleanup := setupSettingsTestDB(t)
	defer cleanup()

	if err := repo.CreateApiKey("bk1", "sk-backup-abcdefghijklmnop", "backup key", "m"); err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}
	if err := repo.SetApiKeyRateLimit("bk1", 120, 250000, 8); err != nil {
		t.Fatalf("SetApiKeyRateLimit: %v", err)
	}
	if err := repo.SetApiKeyBudget("bk1", 9000000, 45000); err != nil {
		t.Fatalf("SetApiKeyBudget: %v", err)
	}

	router := setupTestRouter(repo)

	// Export.
	req := httptest.NewRequest(http.MethodGet, "/api/settings/database", nil)
	req.Header.Set(cliTokenHeader, auth.CLIToken())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("export failed: %d %s", rec.Code, rec.Body.String())
	}
	exported := rec.Body.Bytes()

	// Wipe the key, then restore the backup.
	if _, err := repo.RawDB().Exec(`DELETE FROM apiKeys`); err != nil {
		t.Fatalf("wipe failed: %v", err)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/settings/database", bytes.NewReader(exported))
	req.Header.Set(cliTokenHeader, auth.CLIToken())
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("import failed: %d %s", rec.Code, rec.Body.String())
	}

	restored, err := repo.GetApiKeyByID("bk1")
	if err != nil {
		t.Fatalf("GetApiKeyByID: %v", err)
	}
	if restored == nil {
		t.Fatal("key was not restored")
	}
	checks := map[string]struct {
		got  *int
		want int
	}{
		"rateLimitRPM":         {restored.RateLimitRPM, 120},
		"rateLimitTPM":         {restored.RateLimitTPM, 250000},
		"rateLimitConcurrency": {restored.RateLimitConcurrency, 8},
		"tokenQuota":           {restored.TokenQuota, 9000000},
		"queueTimeoutMs":       {restored.QueueTimeoutMs, 45000},
	}
	for name, c := range checks {
		if c.got == nil {
			t.Errorf("%s: nil after restore, want %d", name, c.want)
			continue
		}
		if *c.got != c.want {
			t.Errorf("%s = %d after restore, want %d", name, *c.got, c.want)
		}
	}
}

// TestBackupExportIncludesApiKeyLimitKeys guards the export shape directly: the
// columns must be present in each exported apiKeys row, not merely derivable.
func TestBackupExportIncludesApiKeyLimitKeys(t *testing.T) {
	authTestEnv(t)
	repo, cleanup := setupSettingsTestDB(t)
	defer cleanup()

	if err := repo.CreateApiKey("bk2", "sk-export-abcdefghijklmnop", "export key", "m"); err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}
	if err := repo.SetApiKeyBudget("bk2", 777, 1234); err != nil {
		t.Fatalf("SetApiKeyBudget: %v", err)
	}

	router := setupTestRouter(repo)
	req := httptest.NewRequest(http.MethodGet, "/api/settings/database", nil)
	req.Header.Set(cliTokenHeader, auth.CLIToken())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("export failed: %d %s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal export: %v", err)
	}
	rows, _ := payload["apiKeys"].([]any)
	if len(rows) != 1 {
		t.Fatalf("exported %d api keys, want 1", len(rows))
	}
	row, _ := rows[0].(map[string]any)
	for _, key := range []string{"tokenQuota", "queueTimeoutMs", "rateLimitRPM", "rateLimitTPM", "rateLimitConcurrency"} {
		if _, ok := row[key]; !ok {
			t.Errorf("exported api key is missing %q", key)
		}
	}
}
