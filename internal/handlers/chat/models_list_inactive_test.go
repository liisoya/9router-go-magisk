package chat

import (
	"strings"
	"testing"

	"9router/proxy/internal/db"
)

// Issue #46 reported that /v1/models dropped to zero models once every
// connection was disabled, and that ?connected=1 answered the same way. The
// second half is covered by the noAuth catalog branch; this pins the first:
// the default mode must not mistake a disabled connection row for a
// configured install and answer with an empty list while ?all=1 still serves
// the full catalog.
func TestHandleModels_DefaultModeIgnoresOnlyInactiveConnections(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`DELETE FROM providerConnections`); err != nil {
		t.Fatalf("delete connections: %v", err)
	}
	if _, err := database.Exec(`DELETE FROM kv WHERE scope='disabledModels'`); err != nil {
		t.Fatalf("delete disabled: %v", err)
	}

	// One kiro connection, then disabled in the dashboard (isActive = 0).
	if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
		('conn-kiro-1', 'kiro', 'oauth', 'Kiro Account', 1, 1, '{"apiKey":"tok"}', '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`); err != nil {
		t.Fatalf("seed active kiro: %v", err)
	}

	h := NewChatHandler(db.NewRepo(database))

	active := fetchModels(t, h, "")
	if len(active.Data) == 0 {
		t.Fatal("an active connection must produce models")
	}

	if _, err := database.Exec(`UPDATE providerConnections SET isActive = 0 WHERE id = 'conn-kiro-1'`); err != nil {
		t.Fatalf("disable kiro: %v", err)
	}

	disabled := fetchModels(t, h, "")
	catalog := fetchModels(t, h, "?all=1")

	if disabled.Connections != 0 {
		t.Errorf("connections = %d after disabling the only connection, want 0", disabled.Connections)
	}

	// The reported symptom, verbatim: an empty list here made a configured
	// install look identical to a broken one, while the catalog behind ?all=1
	// stayed fully intact.
	if len(disabled.Data) == 0 {
		t.Errorf("default mode answered %d models with no active connection left, while ?all=1 served %d; an empty list makes a fresh install look like a broken one",
			len(disabled.Data), len(catalog.Data))
	}

	// Not the whole catalog either: rows exist and every one of them is
	// disabled, so this is a configured install the operator switched off, not
	// a fresh one. The answer is the noAuth subset the issue asked for — the
	// providers callable without a connection.
	if hasPrefixID(disabled.idSet(), "kr/") {
		t.Errorf("the disabled connection's own models must stay out:\n%s", firstLines(strings.Join(disabled.ids(), "\n"), 20))
	}
	connected := fetchModels(t, h, "?connected=1")
	if len(disabled.Data) != len(connected.Data) {
		t.Errorf("with no active connection the default mode (%d models) must match ?connected=1 (%d models) — the issue asks for the noAuth subset, not a different list",
			len(disabled.Data), len(connected.Data))
	}

	// ?all=1 promises to ignore connections entirely, so it must NOT follow the
	// default mode into the noAuth subset just because every connection is off.
	// It exists precisely to reach past that state and dump the whole catalog.
	if len(catalog.Data) <= len(connected.Data) {
		t.Errorf("?all=1 returned %d models, which is not more than the connected subset (%d) — catalog mode must stay unfiltered",
			len(catalog.Data), len(connected.Data))
	}
}
