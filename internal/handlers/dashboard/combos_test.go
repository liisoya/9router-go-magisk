package dashboard

import (
	"bytes"
	"database/sql"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"9router/proxy/internal/db"
	"9router/proxy/internal/models"
	"9router/proxy/internal/providers"
)

// seedConnection inserts one providerConnections row so the auto free-tier
// combo has a reachable provider to draw models from. The id is derived from
// the provider so a test can seed several providers without a key collision.
func seedConnection(t *testing.T, db *sql.DB, provider string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO providerConnections (id, provider, authType, name, data, createdAt, updatedAt)
		 VALUES ('c-' || ?, ?, 'api-key', 'test', '{}', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		provider, provider,
	); err != nil {
		t.Fatalf("seed connection: %v", err)
	}
}

func postJSON(t *testing.T, router interface {
	ServeHTTP(w http.ResponseWriter, r *http.Request)
}, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func putJSON(t *testing.T, router interface {
	ServeHTTP(w http.ResponseWriter, r *http.Request)
}, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestAutoFreeComboCreateRebuildAndEdit(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	// A bazaarlink connection exists: its registry list has exactly one
	// free-tier model ("auto:free"), which is what the combo must contain.
	const providerID = "bazaarlink"
	seedConnection(t, repo.RawDB(), providerID)

	rec := postJSON(t, router, "/api/combos/auto-free", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("auto-free expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	models, _ := created["models"].([]any)
	if len(models) != 1 {
		t.Fatalf("expected 1 free model, got %v", models)
	}
	if got := models[0]; got != "bzl/auto:free" {
		t.Errorf("expected bzl/auto:free, got %v", got)
	}

	// A generated combo is an ordinary row: renaming it must succeed, because
	// the user is expected to make this combo addressable under their own name.
	rec = putJSON(t, router, "/api/combos/"+AutoFreeComboID, map[string]any{
		"name": "Free Saya",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("rename expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if combo, _ := repo.GetComboById(AutoFreeComboID); combo == nil || combo.Name != "Free Saya" {
		t.Fatalf("rename did not persist: %+v", combo)
	}

	// Editing the model set must succeed too — a locked model set meant the
	// rebuild was the only way to change what the combo points at.
	rec = putJSON(t, router, "/api/combos/"+AutoFreeComboID, map[string]any{
		"models": []string{"bzl/auto:free", "bzl/claude-opus-4.7"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("model-set change expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	members, err := comboModels(mustCombo(t, repo, AutoFreeComboID).Models)
	if err != nil {
		t.Fatalf("comboModels: %v", err)
	}
	if len(members) != 2 {
		t.Errorf("expected the edited model set to persist, got %v", members)
	}

	// The card's strategy dropdown sends no models field at all. A partial
	// update must not clobber the model set edited just above.
	rec = putJSON(t, router, "/api/combos/"+AutoFreeComboID, map[string]any{
		"strategy": "round-robin",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("strategy-only update expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	members, err = comboModels(mustCombo(t, repo, AutoFreeComboID).Models)
	if err != nil {
		t.Fatalf("comboModels after strategy-only update: %v", err)
	}
	if len(members) != 2 {
		t.Errorf("strategy-only update clobbered the model set: %v", members)
	}

	// Delete must succeed: the combo is user state, not system state.
	req := httptest.NewRequest(http.MethodDelete, "/api/combos/"+AutoFreeComboID, nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if combo, _ := repo.GetComboById(AutoFreeComboID); combo != nil {
		t.Fatalf("combo survived delete: %+v", combo)
	}

	// Rebuilding after the delete recreates it — the delete is not a "never
	// again" state, only a way to drop the combo until it is wanted again.
	rec = postJSON(t, router, "/api/combos/auto-free", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rebuild after delete expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rebuilt := mustCombo(t, repo, AutoFreeComboID)
	if rebuilt.Name != "Auto Free Tier" {
		t.Errorf("rebuild must restore the generated name, got %q", rebuilt.Name)
	}
	members, err = comboModels(rebuilt.Models)
	if err != nil {
		t.Fatalf("comboModels after rebuild: %v", err)
	}
	if len(members) != 1 || members[0] != "bzl/auto:free" {
		t.Errorf("rebuild must restore the registry model set, got %v", members)
	}
}

func mustCombo(t *testing.T, repo *db.Repo, id string) *models.Combo {
	t.Helper()
	combo, err := repo.GetComboById(id)
	if err != nil {
		t.Fatalf("GetComboById(%s): %v", id, err)
	}
	if combo == nil {
		t.Fatalf("combo %s not found", id)
	}
	return combo
}

func TestAutoFreeComboEmptyWithoutConnections(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	rec := postJSON(t, router, "/api/combos/auto-free", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("no free models expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAutoFreeComboExcludesNonChatFreeModels(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	// ":free" is a naming convention, not a service kind. openrouter's only
	// free-suffixed registry model is the embedding one
	// ("nvidia/llama-nemotron-embed-vl-1b-v2:free"), so a combo built from it
	// alone must report nothing usable rather than a chat chain that cannot
	// answer a single turn.
	seedConnection(t, repo.RawDB(), "openrouter")
	rec := postJSON(t, router, "/api/combos/auto-free", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("embedding-only free models expected 409, got %d: %s", rec.Code, rec.Body.String())
	}

	// Adding a provider that does have a free chat model keeps the combo, and
	// the embedding entry must not be in it.
	seedConnection(t, repo.RawDB(), "bazaarlink")
	rec = postJSON(t, router, "/api/combos/auto-free", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("auto-free expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	members, err := comboModels(mustCombo(t, repo, AutoFreeComboID).Models)
	if err != nil {
		t.Fatalf("comboModels: %v", err)
	}
	if want := []string{"bzl/auto:free"}; !slices.Equal(members, want) {
		t.Errorf("combo members = %v, want %v (the embedding model must be excluded)", members, want)
	}
}

func TestIsFreeTierModel(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  bool
	}{
		{"colon free suffix", "deepseek-v4.1-flash:free", true},
		{"slash free suffix", "kilo-auto/free", true},
		{"dash free suffix", "mimo-v2.5-free", true},
		{"paid model", "claude-opus-4.7", false},
		{"free inside name only", "free-tier-proxy", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := providers.IsFreeTierModel(tt.model); got != tt.want {
				t.Errorf("IsFreeTierModel(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}
