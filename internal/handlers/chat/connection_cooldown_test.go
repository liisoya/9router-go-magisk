package chat

import (
	"database/sql"
	json "encoding/json/v2"
	"strings"
	"testing"
	"time"

	"9router/proxy/internal/db"
)

func cooldownData(t *testing.T, until *time.Time) string {
	t.Helper()
	data := map[string]any{"apiKey": "sk-test-deepseek-key"}
	if until != nil {
		data["rateLimitedUntil"] = until.UTC().Format(time.RFC3339)
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal data: %v", err)
	}
	return string(raw)
}

func insertDeepseekConn(t *testing.T, database *sql.DB, id string, priority int, data string) {
	t.Helper()
	_, err := database.Exec(
		`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt)
		 VALUES (?, 'deepseek', 'apikey', ?, ?, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`,
		id, id, priority, data)
	if err != nil {
		t.Fatalf("insert %s: %v", id, err)
	}
}

// dropSeededConn removes the shared conn-1 the harness seeds, so each test
// controls the whole candidate set and the priority order is unambiguous.
func dropSeededConn(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.Exec(`DELETE FROM providerConnections WHERE id = 'conn-1'`); err != nil {
		t.Fatalf("drop seeded connection: %v", err)
	}
}

// An account whose quota is spent must not be handed out again by rotation.
// Before the fix the only cooldown was the per-model lock, so a cooling
// account kept being selected until a live request failed and locked it.
func TestGetBestConnection_SkipsAccountInCooldown(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	dropSeededConn(t, database)
	inTenMinutes := time.Now().UTC().Add(10 * time.Minute)
	insertDeepseekConn(t, database, "conn-cooling", 1, cooldownData(t, &inTenMinutes))
	insertDeepseekConn(t, database, "conn-healthy", 2, cooldownData(t, nil))

	handler := NewChatHandler(db.NewRepo(database))
	conn, _, err := handler.getBestConnection("deepseek", "", nil, "deepseek-chat")
	if err != nil {
		t.Fatalf("getBestConnection: %v", err)
	}
	if conn.ID == "conn-cooling" {
		t.Error("selected an account that is still in cooldown")
	}
	if conn.ID != "conn-healthy" {
		t.Errorf("selected %q, want conn-healthy", conn.ID)
	}
}

// A cooldown that has already run out must not keep the account out.
func TestGetBestConnection_ExpiredCooldownIsStillEligible(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	dropSeededConn(t, database)
	anHourAgo := time.Now().UTC().Add(-time.Hour)
	insertDeepseekConn(t, database, "conn-recovered", 1, cooldownData(t, &anHourAgo))

	handler := NewChatHandler(db.NewRepo(database))
	conn, _, err := handler.getBestConnection("deepseek", "", nil, "deepseek-chat")
	if err != nil {
		t.Fatalf("getBestConnection: %v", err)
	}
	if conn.ID != "conn-recovered" {
		t.Errorf("selected %q, want conn-recovered (its cooldown has expired)", conn.ID)
	}
}

// The cooldown is account-scoped, so it has to apply even when the request
// carries no model. The per-model lock filter only runs when model != "", which
// is why a model-less request had no cooldown filter at all.
func TestGetBestConnection_CooldownAppliesWithoutModel(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	dropSeededConn(t, database)
	inFiveMinutes := time.Now().UTC().Add(5 * time.Minute)
	insertDeepseekConn(t, database, "conn-cooling", 1, cooldownData(t, &inFiveMinutes))

	handler := NewChatHandler(db.NewRepo(database))
	conn, _, err := handler.getBestConnection("deepseek", "", nil, "")
	if err == nil && conn.ID == "conn-cooling" {
		t.Error("a model-less request still selected an account in cooldown")
	}
}

// When every candidate is cooling, the caller needs to know when to come back
// rather than a bare "all excluded" it cannot act on.
func TestGetBestConnection_AllInCooldownReportsEarliestReset(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	dropSeededConn(t, database)
	inTenMinutes := time.Now().UTC().Add(10 * time.Minute)
	inTwoMinutes := time.Now().UTC().Add(2 * time.Minute)
	insertDeepseekConn(t, database, "conn-later", 1, cooldownData(t, &inTenMinutes))
	insertDeepseekConn(t, database, "conn-sooner", 2, cooldownData(t, &inTwoMinutes))

	handler := NewChatHandler(db.NewRepo(database))
	_, _, err := handler.getBestConnection("deepseek", "", nil, "deepseek-chat")
	if err == nil {
		t.Fatal("expected an error when every account is cooling")
	}
	if !containsAll(err.Error(), "cooldown", inTwoMinutes.UTC().Format(time.RFC3339)) {
		t.Errorf("error should name the earliest reset, got: %v", err)
	}
}

// A client-pinned connection that is in cooldown must fall through to the
// strategy, matching how upstream resolves the pin inside its availability
// filter.
func TestGetBestConnection_PinnedInCooldownFallsThrough(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	dropSeededConn(t, database)
	inTenMinutes := time.Now().UTC().Add(10 * time.Minute)
	insertDeepseekConn(t, database, "conn-cooling", 1, cooldownData(t, &inTenMinutes))
	insertDeepseekConn(t, database, "conn-healthy", 2, cooldownData(t, nil))

	handler := NewChatHandler(db.NewRepo(database))
	conn, _, err := handler.getBestConnection("deepseek", "conn-cooling", nil, "deepseek-chat")
	if err != nil {
		t.Fatalf("getBestConnection: %v", err)
	}
	if conn.ID == "conn-cooling" {
		t.Error("served a pinned account that is in cooldown")
	}
	if conn.ID != "conn-healthy" {
		t.Errorf("selected %q, want conn-healthy", conn.ID)
	}
}

// A malformed cooldown field must never take routing down: the selector
// treats "cannot tell" as "not in cooldown".
func TestGetBestConnection_MalformedCooldownDoesNotBlockSelection(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	dropSeededConn(t, database)
	insertDeepseekConn(t, database, "conn-garbage", 1, `{"apiKey":"sk-x","rateLimitedUntil":"soon"}`)

	handler := NewChatHandler(db.NewRepo(database))
	conn, _, err := handler.getBestConnection("deepseek", "", nil, "deepseek-chat")
	if err != nil {
		t.Fatalf("getBestConnection: %v", err)
	}
	if conn.ID != "conn-garbage" {
		t.Errorf("selected %q, want conn-garbage", conn.ID)
	}
}

func containsAll(haystack string, needles ...string) bool {
	for _, n := range needles {
		if !strings.Contains(haystack, n) {
			return false
		}
	}
	return true
}
