package chat

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"9router/proxy/internal/db"
)

func TestHandleModels_IncludesTokenLimits(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	repo := db.NewRepo(database)
	// Upstream parity: alias targets are merged into the owning provider's
	// list, so the anthropic connection has to exist.
	if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
		('conn-ant-limits', 'anthropic', 'apikey', 'Anthropic Limits', 1, 1, '{"apiKey":"sk-test-ant"}', '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`); err != nil {
		t.Fatalf("seed anthropic connection: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO kv (scope, key, value) VALUES ('modelAliases', 'claude-sonnet-4-6', '"anthropic/claude-sonnet-4-6"')`); err != nil {
		t.Fatalf("insert model alias: %v", err)
	}

	h := NewChatHandler(repo)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/models", nil)

	h.HandleModels(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Upstream emits context_length / max_completion_tokens at the top level
	// and contextWindow / maxOutput inside capabilities.
	var resp struct {
		Object string `json:"object"`
		Data   []struct {
			ID                  string `json:"id"`
			ContextLength       *int   `json:"context_length"`
			MaxCompletionTokens *int   `json:"max_completion_tokens"`
			Capabilities        *struct {
				ContextWindow int `json:"contextWindow"`
				MaxOutput     int `json:"maxOutput"`
			} `json:"capabilities"`
		} `json:"data"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if len(resp.Data) == 0 {
		t.Fatal("expected at least 1 model in /v1/models")
	}

	found := false
	for _, m := range resp.Data {
		if !strings.HasSuffix(m.ID, "/claude-sonnet-4-6") {
			continue
		}
		found = true
		if m.ContextLength == nil || *m.ContextLength <= 0 {
			t.Errorf("expected positive context_length for %s, got %v", m.ID, m.ContextLength)
		}
		if m.MaxCompletionTokens == nil || *m.MaxCompletionTokens <= 0 {
			t.Errorf("expected positive max_completion_tokens for %s, got %v", m.ID, m.MaxCompletionTokens)
		}
		if m.Capabilities == nil || m.Capabilities.ContextWindow <= 0 {
			t.Errorf("expected positive capabilities.contextWindow for %s, got %+v", m.ID, m.Capabilities)
		}
	}
	if !found {
		t.Error("claude-sonnet-4-6 (alias target of a connected provider) not found in /v1/models response")
	}
}
