package chat

import (
	"context"
	json "encoding/json/v2"
	"testing"

	"9router/proxy/internal/db"
)

// The paid go lane shares its backend account with opencode-zen: with no go
// connection configured, the key is borrowed from an active zen connection
// (key only — provider, BaseURL and lanes stay go).
func TestBorrowZenKeyForGo(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	clearSeededConns(t, database)

	zenData, _ := json.Marshal(map[string]any{"apiKey": "sk-zen-borrowed"})
	if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
		('conn-zen-1', 'opencode-zen', 'apikey', 'Zen Test', 1, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`, string(zenData)); err != nil {
		t.Fatalf("seed zen connection: %v", err)
	}

	h := NewChatHandler(db.NewRepo(database))
	connID, _, _, apiKey, err := h.borrowZenKeyForGo(context.Background(), "glm-5.2")
	if err != nil {
		t.Fatalf("borrowZenKeyForGo: %v", err)
	}
	if connID != "conn-zen-1" {
		t.Errorf("connID = %q, want conn-zen-1", connID)
	}
	if apiKey != "sk-zen-borrowed" {
		t.Errorf("apiKey = %q, want sk-zen-borrowed", apiKey)
	}
}

func TestBorrowZenKeyForGo_NoZenConnection(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	clearSeededConns(t, database)

	h := NewChatHandler(db.NewRepo(database))
	if _, _, _, _, err := h.borrowZenKeyForGo(context.Background(), "glm-5.2"); err == nil {
		t.Fatal("expected an error with no zen connection, got nil")
	}
}
