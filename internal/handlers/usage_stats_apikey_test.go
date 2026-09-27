package handlers

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	"9router/proxy/internal/db"
	"9router/proxy/internal/handlerutil"
)

// TestUsageStats_ByApiKey covers the API-key breakdown, which the response
// declared but never filled — the dashboard's per-key table was permanently
// empty. Two keys minted by one instance share the same `sk-{machineId}` head,
// so the buckets have to stay distinct and be named after their configured key.
func TestUsageStats_ByApiKey(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)

	// Same machine-id head, different tails: the collision this guards against.
	const keyA = "sk-8b71f86e0a1f2fb5-nhz496-cfa1c800"
	const keyB = "sk-8b71f86e0a1f2fb5-s59ir7-9362e34f"
	if err := repo.CreateApiKey("id-a", keyA, "Laptop", "machine-1"); err != nil {
		t.Fatalf("create api key A: %v", err)
	}
	if err := repo.CreateApiKey("id-b", keyB, "Desktop", "machine-1"); err != nil {
		t.Fatalf("create api key B: %v", err)
	}

	maskA := handlerutil.MaskAPIKey(keyA)
	maskB := handlerutil.MaskAPIKey(keyB)
	if maskA == maskB {
		t.Fatalf("test premise broken: both keys mask to %q", maskA)
	}

	rows := []struct {
		key     string
		prompt  int
		complet int
	}{
		{maskA, 10, 5},
		{maskA, 20, 6},
		{maskB, 30, 7},
		{"", 40, 8}, // a local call with no key
	}
	for _, row := range rows {
		if err := repo.InsertUsageHistory("openai", "gpt-5.5", "conn-1", row.key, "/v1/chat/completions", row.prompt, row.complet, 0.5, "success", 0, "{}", "{}"); err != nil {
			t.Fatalf("insert usage history: %v", err)
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/usage/stats?period=24h", nil)
	HandleUsageStats(repo)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}

	var resp UsageStatsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(resp.ByApiKey) != 3 {
		t.Fatalf("expected 3 buckets (keyA, keyB, no-key), got %d: %+v", len(resp.ByApiKey), resp.ByApiKey)
	}

	byName := map[string]ApiKeyUsageItem{}
	for _, item := range resp.ByApiKey {
		byName[item.KeyName] = item
	}

	tests := []struct {
		keyName     string
		wantMasked  string
		wantRequest int
		wantPrompt  int64
	}{
		{keyName: "Laptop", wantMasked: maskA, wantRequest: 2, wantPrompt: 30},
		{keyName: "Desktop", wantMasked: maskB, wantRequest: 1, wantPrompt: 30},
		{keyName: "Local (No Key)", wantMasked: "local-no-key", wantRequest: 1, wantPrompt: 40},
	}
	for _, tt := range tests {
		t.Run(tt.keyName, func(t *testing.T) {
			got, ok := byName[tt.keyName]
			if !ok {
				t.Fatalf("no bucket named %q (have %v)", tt.keyName, byName)
			}
			if got.ApiKeyMasked != tt.wantMasked {
				t.Errorf("ApiKeyMasked = %q, want %q", got.ApiKeyMasked, tt.wantMasked)
			}
			if got.ApiKeyKey != tt.wantMasked {
				t.Errorf("ApiKeyKey = %q, want %q", got.ApiKeyKey, tt.wantMasked)
			}
			if got.Requests != tt.wantRequest {
				t.Errorf("Requests = %d, want %d", got.Requests, tt.wantRequest)
			}
			if got.PromptTokens != tt.wantPrompt {
				t.Errorf("PromptTokens = %d, want %d", got.PromptTokens, tt.wantPrompt)
			}
			if got.RawModel != "gpt-5.5" || got.Provider != "openai" {
				t.Errorf("model/provider = %q/%q, want gpt-5.5/openai", got.RawModel, got.Provider)
			}
		})
	}
}
