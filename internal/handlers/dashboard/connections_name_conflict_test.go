package dashboard

import (
	json "encoding/json/v2"
	"net/http"
	"testing"
)

// TestHandleCreateConnection_NameConflict covers the guard added for #4311: a
// create that reuses a name already in use used to replace the stored key
// without a word, so a script naming rows "Key 1", "Key 2", … kept wiping the
// existing pool entries and got a success back. A caller that means "change the
// key behind this name" says so explicitly; everyone else gets a typed 409.
func TestHandleCreateConnection_NameConflict(t *testing.T) {
	t.Run("a colliding name is refused with a typed 409", func(t *testing.T) {
		repo, cleanup := setupTestDB(t)
		defer cleanup()
		router := setupTestRouter(repo)

		first := postConnection(t, router, `{"provider":"deepseek","name":"Key 1","apiKey":"original"}`)
		if first.Code != http.StatusOK {
			t.Fatalf("first create = %d, body %s", first.Code, first.Body)
		}
		var created map[string]any
		if err := json.Unmarshal(first.Body.Bytes(), &created); err != nil {
			t.Fatalf("decode first: %v", err)
		}
		firstID, _ := created["id"].(string)

		second := postConnection(t, router, `{"provider":"deepseek","name":"Key 1","apiKey":"intruder"}`)
		if second.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d: %s", second.Code, second.Body)
		}
		var conflict map[string]any
		if err := json.Unmarshal(second.Body.Bytes(), &conflict); err != nil {
			t.Fatalf("decode conflict: %v", err)
		}
		if got, _ := conflict["code"].(string); got != "PROVIDER_NAME_CONFLICT" {
			t.Errorf("code = %q, want PROVIDER_NAME_CONFLICT", got)
		}
		if got, _ := conflict["existingId"].(string); got != firstID {
			t.Errorf("existingId = %q, want %q", got, firstID)
		}
		if got, _ := conflict["existingName"].(string); got != "Key 1" {
			t.Errorf("existingName = %q, want Key 1", got)
		}
		if msg, _ := conflict["error"].(string); msg == "" {
			t.Error("the 409 must name the connection that would have been replaced")
		}

		// The stored key must be the one the caller never asked to change.
		stored, err := repo.GetProviderConnectionByID(firstID)
		if err != nil || stored == nil {
			t.Fatalf("re-read connection: %v", err)
		}
		if !containsKey(stored.Data, "original") {
			t.Errorf("stored key changed: %s", stored.Data)
		}
	})

	t.Run("allowOverwrite rewrites the row in place", func(t *testing.T) {
		repo, cleanup := setupTestDB(t)
		defer cleanup()
		router := setupTestRouter(repo)

		first := postConnection(t, router, `{"provider":"deepseek","name":"Key 1","apiKey":"original"}`)
		var created map[string]any
		_ = json.Unmarshal(first.Body.Bytes(), &created)
		firstID, _ := created["id"].(string)

		second := postConnection(t, router,
			`{"provider":"deepseek","name":"Key 1","apiKey":"replacement","allowOverwrite":true}`)
		if second.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", second.Code, second.Body)
		}
		var replaced map[string]any
		if err := json.Unmarshal(second.Body.Bytes(), &replaced); err != nil {
			t.Fatalf("decode replace: %v", err)
		}
		if got, _ := replaced["id"].(string); got != firstID {
			t.Errorf("id = %q, want the existing %q — an overwrite keeps the row", got, firstID)
		}

		stored, err := repo.GetProviderConnectionByID(firstID)
		if err != nil || stored == nil {
			t.Fatalf("re-read connection: %v", err)
		}
		if !containsKey(stored.Data, "replacement") {
			t.Errorf("key was not replaced: %s", stored.Data)
		}
		// One row, not two with the same name.
		again, err := repo.GetProviderConnectionByName("deepseek", "apikey", "Key 1")
		if err != nil || again == nil || again.ID != firstID {
			t.Errorf("expected the single row %q, got %+v (err %v)", firstID, again, err)
		}
	})

	t.Run("the legacy overwrite spelling also works", func(t *testing.T) {
		repo, cleanup := setupTestDB(t)
		defer cleanup()
		router := setupTestRouter(repo)

		postConnection(t, router, `{"provider":"deepseek","name":"Key 1","apiKey":"original"}`)
		second := postConnection(t, router, `{"provider":"deepseek","name":"Key 1","apiKey":"x","overwrite":true}`)
		if second.Code != http.StatusOK {
			t.Errorf("overwrite:true = %d, want 200: %s", second.Code, second.Body)
		}
	})

	t.Run("a free name, another auth type, and an explicit id are all unaffected", func(t *testing.T) {
		repo, cleanup := setupTestDB(t)
		defer cleanup()
		router := setupTestRouter(repo)

		postConnection(t, router, `{"provider":"deepseek","name":"Key 1","apiKey":"k1"}`)

		if rec := postConnection(t, router, `{"provider":"deepseek","name":"Key 2","apiKey":"k2"}`); rec.Code != http.StatusOK {
			t.Errorf("a free name = %d, want 200: %s", rec.Code, rec.Body)
		}
		if rec := postConnection(t, router, `{"provider":"deepseek","authType":"oauth","name":"Key 1"}`); rec.Code != http.StatusOK {
			t.Errorf("another auth type = %d, want 200: %s", rec.Code, rec.Body)
		}
		// An id makes it an explicit edit, which is never name-guarded.
		if rec := postConnection(t, router, `{"id":"fixed-id","provider":"deepseek","name":"Key 1","apiKey":"k3"}`); rec.Code != http.StatusOK {
			t.Errorf("explicit id = %d, want 200: %s", rec.Code, rec.Body)
		}
	})
}

// TestGetProviderConnectionByName checks the lookup the guard is built on: the
// free/busy answer has to distinguish a real collision from an empty name and
// from a different provider.
func TestGetProviderConnectionByName(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	postConnection(t, router, `{"provider":"deepseek","name":"Key 1","apiKey":"k1"}`)

	tests := []struct {
		name             string
		provider         string
		authType, connID string
	}{
		{name: "the same name on another provider", provider: "deepgram", authType: "apikey"},
		{name: "an empty name never matches", provider: "deepseek", authType: "apikey"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := repo.GetProviderConnectionByName(tt.provider, tt.authType, tt.connID)
			if err != nil {
				t.Fatalf("lookup: %v", err)
			}
			if got != nil {
				t.Errorf("expected no match, got %+v", got)
			}
		})
	}

	got, err := repo.GetProviderConnectionByName("deepseek", "apikey", "Key 1")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got == nil || got.Name == nil || *got.Name != "Key 1" {
		t.Errorf("expected the stored row, got %+v", got)
	}
}

// containsKey reports whether a stored connection payload carries value.
func containsKey(dataJSON, value string) bool {
	var data map[string]any
	if err := json.Unmarshal([]byte(dataJSON), &data); err != nil {
		return false
	}
	key, _ := data["apiKey"].(string)
	return key == value
}
