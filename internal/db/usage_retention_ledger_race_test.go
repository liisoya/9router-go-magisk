package db

import (
	"testing"
	"time"

	"9router/proxy/internal/handlerutil"
)

// A reset that commits while a prune is running must not be undone by the prune.
//
// PruneUsageHistory reads every key's reset mark once, up front, and each chunk
// is folded against that snapshot. ResetApiKeyQuota raises the mark past the
// doomed rows and clears the key's carryover in one transaction — so a reset
// landing between the snapshot and a later chunk used to have its carryover
// re-banked by that chunk, against the older mark, and the key was charged again
// for spend it had just been forgiven.
//
// The sequence below is the race without goroutines or sleeps: snapshot, reset,
// then fold-and-delete the next chunk, which is exactly the order the two
// concurrent operations produce.
func TestPruneUsageHistory_HonoursResetThatLandsMidPrune(t *testing.T) {
	repo := openEphemeralSchemaDB(t)
	ctx := t.Context()

	const rawKey = "sk-ledger-mid-prune-reset-01"
	if err := repo.CreateApiKey("kr", rawKey, "mid-prune reset key", "m"); err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}

	seedLedgerRow(t, repo, 0, handlerutil.MaskAPIKey(rawKey), handlerutil.MaskAPIKey(rawKey), 500, 0,
		time.Now().UTC().Add(-40*24*time.Hour))

	// The prune's up-front snapshot of every key's reset mark.
	watched, err := repo.ledgerWatchedKeys(ctx)
	if err != nil {
		t.Fatalf("ledgerWatchedKeys: %v", err)
	}

	// A reset commits after that snapshot: the mark moves past the doomed row
	// and the key's carryover is cleared.
	if _, err := repo.ResetApiKeyQuota(ctx, "kr"); err != nil {
		t.Fatalf("ResetApiKeyQuota: %v", err)
	}

	// The prune keeps going and folds the doomed row against its stale snapshot.
	chunk, err := repo.readLedgerChunk(ctx, 0, ledgerCutoff(30*24*time.Hour))
	if err != nil {
		t.Fatalf("readLedgerChunk: %v", err)
	}
	if len(chunk) != 1 {
		t.Fatalf("chunk rows = %d, want 1", len(chunk))
	}
	if _, err := repo.deleteLedgerChunk(ctx, chunk, watched); err != nil {
		t.Fatalf("deleteLedgerChunk: %v", err)
	}

	used, err := repo.GetApiKeyTokenUsageByID(ctx, "kr")
	if err != nil {
		t.Fatalf("GetApiKeyTokenUsageByID: %v", err)
	}
	if used != 0 {
		t.Errorf("usage after a reset that landed mid-prune = %d, want 0: the prune re-banked spend the reset forgave", used)
	}

	// And the row itself is gone, so the prune still did its job.
	if got := countRows(t, repo, "usageHistory"); got != 0 {
		t.Errorf("usageHistory rows = %d, want 0", got)
	}
}

// The same reset, this time landing before the snapshot, must keep working —
// that path was already correct and this pins it so a fix for the race above
// cannot regress it.
func TestPruneUsageHistory_HonoursResetBeforePrune(t *testing.T) {
	repo := openEphemeralSchemaDB(t)
	ctx := t.Context()

	const rawKey = "sk-ledger-pre-prune-reset-02"
	if err := repo.CreateApiKey("kr", rawKey, "pre-prune reset key", "m"); err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}
	seedLedgerRow(t, repo, 0, handlerutil.MaskAPIKey(rawKey), handlerutil.MaskAPIKey(rawKey), 500, 0,
		time.Now().UTC().Add(-40*24*time.Hour))

	if _, err := repo.ResetApiKeyQuota(ctx, "kr"); err != nil {
		t.Fatalf("ResetApiKeyQuota: %v", err)
	}
	if _, err := repo.PruneUsageHistory(ctx, 30*24*time.Hour); err != nil {
		t.Fatalf("PruneUsageHistory: %v", err)
	}

	used, err := repo.GetApiKeyTokenUsageByID(ctx, "kr")
	if err != nil {
		t.Fatalf("GetApiKeyTokenUsageByID: %v", err)
	}
	if used != 0 {
		t.Errorf("usage after reset-then-prune = %d, want 0", used)
	}
}
