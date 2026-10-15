package chat

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"9router/proxy/internal/db"
	"9router/proxy/internal/models"
)

func TestHandleModels_DataModelsIdentical(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	h := NewChatHandler(db.NewRepo(database))

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	h.HandleModels(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data   []ModelInfoObject `json:"data"`
		Models []ModelInfoObject `json:"models"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data) == 0 {
		t.Fatalf("expected non-empty model list")
	}
	if !reflect.DeepEqual(resp.Data, resp.Models) {
		t.Errorf("data and models differ: %d vs %d entries", len(resp.Data), len(resp.Models))
	}
}

func TestHandleModels_StableKeyOrder(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	h := NewChatHandler(db.NewRepo(database))

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	var first string
	for i := 0; i < 25; i++ {
		w := httptest.NewRecorder()
		h.HandleModels(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		body := w.Body.String()
		if i == 0 {
			first = body
			continue
		}
		if body != first {
			t.Fatalf("response %d differs from the first: %q vs %q", i, body[:60], first[:60])
		}
	}
}

func BenchmarkHandleModels(b *testing.B) {
	database, cleanup := setupChatTestDB(b)
	defer cleanup()
	h := NewChatHandler(db.NewRepo(database))
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	h.HandleModels(w, req)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		h.HandleModels(w, req)
	}
}

func TestAggregateComboCapabilities_UsesLoadedRow(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`INSERT INTO combos (id, name, kind, models, createdAt, updatedAt) VALUES
		('combo-1', 'test-combo', 'llm', '["deepseek/deepseek-chat"]', '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`); err != nil {
		t.Fatalf("seed combo: %v", err)
	}
	repo := db.NewRepo(database)
	h := NewChatHandler(repo)

	combos, err := repo.GetCombos()
	if err != nil || len(combos) == 0 {
		t.Fatalf("GetCombos: %v (n=%d)", err, len(combos))
	}
	var target *models.Combo
	for _, c := range combos {
		if c != nil && c.Name == "test-combo" {
			target = c
		}
	}
	if target == nil {
		t.Fatalf("seeded combo not found")
	}
	first, ok := h.aggregateComboCapabilities(target)
	if !ok || first == nil {
		t.Fatalf("expected capabilities for loaded combo row")
	}
	target.Models = ""
	if _, ok := h.aggregateComboCapabilities(target); ok {
		t.Errorf("expected no capabilities for empty in-memory row; function may be re-querying the DB")
	}
}
