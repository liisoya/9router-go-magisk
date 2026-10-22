package db

import (
	"context"
	"strings"
	"testing"
	"time"

	"9router/proxy/internal/handlerutil"
)

// seedLedgerRow inserts one usageHistory row with exact tokens and timestamps.
func seedLedgerRow(t *testing.T, repo *Repo, id int64, clientKey, apiKey string, prompt, compl int, at time.Time) {
	t.Helper()
	ts := at.UTC().Truncate(time.Second).Format(time.RFC3339)
	var q string
	var args []any
	if id > 0 {
		q = `INSERT INTO usageHistory (id, timestamp, provider, model, connectionId, apiKey, clientKey, endpoint, promptTokens, completionTokens, cost, status, tokens, meta)
		     VALUES (?, ?, 'openai', 'gpt-4o', 'conn-1', ?, ?, '/v1/chat/completions', ?, ?, 0.01, 'success', '{}', '')`
		args = []any{id, ts, apiKey, clientKey, prompt, compl}
	} else {
		q = `INSERT INTO usageHistory (timestamp, provider, model, connectionId, apiKey, clientKey, endpoint, promptTokens, completionTokens, cost, status, tokens, meta)
		     VALUES (?, 'openai', 'gpt-4o', 'conn-1', ?, ?, '/v1/chat/completions', ?, ?, 0.01, 'success', '{}', '')`
		args = []any{ts, apiKey, clientKey, prompt, compl}
	}
	if _, err := repo.RawDB().Exec(q, args...); err != nil {
		t.Fatalf("seed usageHistory: %v", err)
	}
}

// TestPruneUsageHistory_SpendInvariant proves the core property: a key's
// GetApiKeyTokenUsage produces identical numbers before and after a prune that
// removes rows inside its budget range.
func TestPruneUsageHistory_SpendInvariant(t *testing.T) {
	repo := retentionTestRepo(t)
	ctx := context.Background()

	const rawKey = "sk-client-secret-alpha-998877"
	masked := handlerutil.MaskAPIKey(rawKey)
	if err := repo.CreateApiKey("key-1", rawKey, "Client 1", "mach-1"); err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)

	// Rows written under the key:
	// - 100d old (will be pruned): 300 prompt + 50 compl = 350
	// - 40d old (will be pruned): 100 prompt + 25 compl = 125
	// - 2d old (will survive): 40 prompt + 10 compl = 50
	// Total lifetime spend = 525 tokens.
	seedLedgerRow(t, repo, 1, masked, "upstream-cred", 300, 50, now.Add(-100*24*time.Hour))
	seedLedgerRow(t, repo, 2, masked, "upstream-cred", 100, 25, now.Add(-40*24*time.Hour))
	seedLedgerRow(t, repo, 3, masked, "upstream-cred", 40, 10, now.Add(-2*24*time.Hour))

	// An unrelated row from another client to verify isolation
	seedLedgerRow(t, repo, 4, "other-client-masked", "upstream-cred", 1000, 500, now.Add(-50*24*time.Hour))

	before, err := repo.GetApiKeyTokenUsage(rawKey)
	if err != nil {
		t.Fatalf("usage before prune: %v", err)
	}
	if before != 525 {
		t.Fatalf("usage before prune = %d, want 525", before)
	}

	// Prune older than 30 days: drops rows 1, 2, and 4 (3 rows).
	res, err := repo.PruneUsageHistory(ctx, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("PruneUsageHistory: %v", err)
	}
	if res.Rows != 3 {
		t.Errorf("pruned rows = %d, want 3", res.Rows)
	}
	// Row 1 (350) + Row 2 (125) carried for key-1 = 475 tokens
	if res.CarriedTokens != 475 {
		t.Errorf("carried tokens = %d, want 475", res.CarriedTokens)
	}

	// INVARIANT: spend after prune must match spend before prune exactly.
	after, err := repo.GetApiKeyTokenUsage(rawKey)
	if err != nil {
		t.Fatalf("usage after prune: %v", err)
	}
	if after != before {
		t.Fatalf("INVARIANT VIOLATED: usage after prune = %d, want %d (must match before prune)", after, before)
	}

	// Dashboard reader (by id) must agree with the limiter (by key)
	afterByID, err := repo.GetApiKeyTokenUsageByID(ctx, "key-1")
	if err != nil {
		t.Fatalf("usage by id: %v", err)
	}
	if afterByID != before {
		t.Fatalf("usage by id = %d, want %d", afterByID, before)
	}

	// Survivor check: only row 3 should remain in usageHistory
	if got := countRows(t, repo, "usageHistory"); got != 1 {
		t.Errorf("usageHistory remaining rows = %d, want 1", got)
	}
}

// TestPruneUsageHistory_ResetExcludesPreResetSpend proves a key that was reset
// before the prune does NOT carry over spend that predates its reset mark.
func TestPruneUsageHistory_ResetExcludesPreResetSpend(t *testing.T) {
	repo := retentionTestRepo(t)
	ctx := context.Background()

	const rawKey = "sk-reset-test-key-12345678"
	masked := handlerutil.MaskAPIKey(rawKey)
	if err := repo.CreateApiKey("key-r", rawKey, "Reset Key", "m1"); err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)

	// Old spend: row 1 at 50d old
	seedLedgerRow(t, repo, 1, masked, "cred", 500, 100, now.Add(-50*24*time.Hour))

	// Reset the key at row 1: mark = 1
	mark, err := repo.ResetApiKeyQuota(ctx, "key-r")
	if err != nil || mark != 1 {
		t.Fatalf("ResetApiKeyQuota: mark=%d, err=%v", mark, err)
	}

	// Fresh spend after mark: row 2 at 40d old (still older than 30d window)
	seedLedgerRow(t, repo, 2, masked, "cred", 20, 5, now.Add(-40*24*time.Hour))
	// Fresh spend inside window: row 3 at 1d old
	seedLedgerRow(t, repo, 3, masked, "cred", 10, 2, now.Add(-24*time.Hour))

	// Budget before prune: only rows after mark count (row 2 + row 3 = 25 + 12 = 37)
	before, err := repo.GetApiKeyTokenUsage(rawKey)
	if err != nil {
		t.Fatalf("usage before: %v", err)
	}
	if before != 37 {
		t.Fatalf("usage before = %d, want 37 (row 1 was reset)", before)
	}

	// Prune older than 30 days: drops row 1 and row 2.
	// Only row 2 (25 tokens) should be carried for key-r; row 1 was <= mark.
	res, err := repo.PruneUsageHistory(ctx, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("PruneUsageHistory: %v", err)
	}
	if res.Rows != 2 {
		t.Errorf("pruned rows = %d, want 2", res.Rows)
	}
	if res.CarriedTokens != 25 {
		t.Errorf("carried tokens = %d, want 25 (row 1 must not be carried)", res.CarriedTokens)
	}

	// Invariant check: spend stays 37
	after, err := repo.GetApiKeyTokenUsage(rawKey)
	if err != nil {
		t.Fatalf("usage after: %v", err)
	}
	if after != before {
		t.Fatalf("usage after = %d, want %d", after, before)
	}
}

// TestPruneUsageHistory_ResetClearsCarryover proves a reset performed after a
// prune clears the accumulated carryover, so the budget starts fresh from zero.
func TestPruneUsageHistory_ResetClearsCarryover(t *testing.T) {
	repo := retentionTestRepo(t)
	ctx := context.Background()

	const rawKey = "sk-carryover-reset-test"
	masked := handlerutil.MaskAPIKey(rawKey)
	if err := repo.CreateApiKey("key-c", rawKey, "Carryover Key", "m1"); err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)

	// Seed old row, prune it into carryover
	seedLedgerRow(t, repo, 10, masked, "cred", 400, 100, now.Add(-60*24*time.Hour))
	if _, err := repo.PruneUsageHistory(ctx, 30*24*time.Hour); err != nil {
		t.Fatalf("PruneUsageHistory: %v", err)
	}

	usage, err := repo.GetApiKeyTokenUsage(rawKey)
	if err != nil || usage != 500 {
		t.Fatalf("usage from carryover = %d, want 500", usage)
	}

	// Reset the key: must clear the carryover table entry for this key
	if _, err := repo.ResetApiKeyQuota(ctx, "key-c"); err != nil {
		t.Fatalf("ResetApiKeyQuota: %v", err)
	}

	afterReset, err := repo.GetApiKeyTokenUsage(rawKey)
	if err != nil {
		t.Fatalf("usage after reset: %v", err)
	}
	if afterReset != 0 {
		t.Fatalf("usage after reset = %d, want 0 (carryover was not cleared!)", afterReset)
	}

	// Ensure the carryover row itself was deleted
	var carryCount int
	if err := repo.RawDB().QueryRow(`SELECT COUNT(*) FROM usageLedgerCarryover WHERE keyId = 'key-c'`).Scan(&carryCount); err != nil {
		t.Fatalf("count carryover: %v", err)
	}
	if carryCount != 0 {
		t.Errorf("carryover rows for key-c = %d, want 0", carryCount)
	}
}

// TestPruneUsageHistory_ChunkBoundaries proves that a prune spanning multiple
// chunks carries every row and leaves no stranded rows.
func TestPruneUsageHistory_ChunkBoundaries(t *testing.T) {
	repo := retentionTestRepo(t)
	ctx := context.Background()

	// Set small chunk size to force multiple chunks
	prevChunk := retentionChunkSize
	retentionChunkSize = 3
	t.Cleanup(func() { retentionChunkSize = prevChunk })

	const rawKey = "sk-chunk-key-1"
	masked := handlerutil.MaskAPIKey(rawKey)
	if err := repo.CreateApiKey("key-chunk", rawKey, "Chunk Key", "m1"); err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	// Seed 8 old rows (will span 3 chunks: 3 + 3 + 2) and 2 new rows
	for i := range 8 {
		seedLedgerRow(t, repo, int64(100+i), masked, "cred", 10, 0, now.Add(-40*24*time.Hour))
	}
	for i := range 2 {
		seedLedgerRow(t, repo, int64(200+i), masked, "cred", 5, 0, now.Add(-1*time.Hour))
	}

	res, err := repo.PruneUsageHistory(ctx, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("PruneUsageHistory: %v", err)
	}
	if res.Rows != 8 {
		t.Errorf("pruned rows = %d, want 8", res.Rows)
	}
	if res.CarriedTokens != 80 {
		t.Errorf("carried tokens = %d, want 80", res.CarriedTokens)
	}

	usage, err := repo.GetApiKeyTokenUsage(rawKey)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	// 80 carried + 10 surviving = 90 tokens
	if usage != 90 {
		t.Errorf("total usage = %d, want 90", usage)
	}
	if got := countRows(t, repo, "usageHistory"); got != 2 {
		t.Errorf("remaining rows = %d, want 2", got)
	}
}

// TestPruneUsageHistory_QueryPlan proves the chunk read uses idx_uh_ts covering
// or rowid monotonic walk without scanning the whole table.
func TestPruneUsageHistory_QueryPlan(t *testing.T) {
	repo := retentionTestRepo(t)
	// Seed 10 rows
	now := time.Now().UTC()
	for i := range 10 {
		seedLedgerRow(t, repo, int64(500+i), "k", "c", 10, 5, now.Add(-time.Duration(i)*time.Hour))
	}

	cutoff := now.Add(-5 * time.Hour).Format(time.RFC3339)
	rows, err := repo.RawDB().Query(
		`EXPLAIN QUERY PLAN
		 SELECT rowid, COALESCE(clientKey, ''), COALESCE(apiKey, ''),
		        CAST(COALESCE(promptTokens, 0) AS INTEGER) + CAST(COALESCE(completionTokens, 0) AS INTEGER)
		   FROM usageHistory
		  WHERE rowid > ? AND timestamp < ?
		  ORDER BY rowid
		  LIMIT ?`, 0, cutoff, 5,
	)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()

	var details []string
	for rows.Next() {
		var selectid, order, from int
		var detail string
		if err := rows.Scan(&selectid, &order, &from, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate plan rows: %v", err)
	}

	planStr := strings.Join(details, " | ")
	// SQLite should use INTEGER PRIMARY KEY / rowid seek or idx_uh_ts
	if strings.Contains(planStr, "SCAN usageHistory") && !strings.Contains(planStr, "USING") {
		t.Errorf("query plan has full unindexed scan: %s", planStr)
	}
}

// TestCountExpiredUsageHistory_MatchesActualPrune proves dryRun reports the exact
// row count that PruneUsageHistory subsequently removes.
func TestCountExpiredUsageHistory_MatchesActualPrune(t *testing.T) {
	repo := retentionTestRepo(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	seedLedgerRow(t, repo, 1, "k", "c", 10, 0, now.Add(-50*24*time.Hour))
	seedLedgerRow(t, repo, 2, "k", "c", 20, 0, now.Add(-40*24*time.Hour))
	seedLedgerRow(t, repo, 3, "k", "c", 30, 0, now.Add(-1*time.Hour))

	count, cutoff, err := repo.CountExpiredUsageHistory(ctx, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("CountExpiredUsageHistory: %v", err)
	}
	if count != 2 {
		t.Errorf("dry-run count = %d, want 2", count)
	}
	if cutoff == "" {
		t.Error("cutoff is empty")
	}

	// Dry run did not delete anything
	if got := countRows(t, repo, "usageHistory"); got != 3 {
		t.Fatalf("usageHistory count changed after dry run: got %d, want 3", got)
	}

	res, err := repo.PruneUsageHistory(ctx, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("PruneUsageHistory: %v", err)
	}
	if res.Rows != count {
		t.Errorf("actual prune removed %d rows, dryRun predicted %d", res.Rows, count)
	}
}

// TestDatabaseStats_ReportsUsageBounds proves GetDatabaseStats includes the
// oldest and newest timestamps for the ledger.
func TestDatabaseStats_ReportsUsageBounds(t *testing.T) {
	repo := retentionTestRepo(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	oldest := now.Add(-10 * 24 * time.Hour).Format(time.RFC3339)
	newest := now.Add(-1 * time.Hour).Format(time.RFC3339)

	seedLedgerRow(t, repo, 1, "k", "c", 10, 0, now.Add(-10*24*time.Hour))
	seedLedgerRow(t, repo, 2, "k", "c", 10, 0, now.Add(-1*time.Hour))

	stats, err := repo.GetDatabaseStats(ctx)
	if err != nil {
		t.Fatalf("GetDatabaseStats: %v", err)
	}
	if stats.UsageHistoryCount != 2 {
		t.Errorf("usageHistoryCount = %d, want 2", stats.UsageHistoryCount)
	}
	if stats.UsageHistoryOldestTimestamp != oldest {
		t.Errorf("oldest = %q, want %q", stats.UsageHistoryOldestTimestamp, oldest)
	}
	if stats.UsageHistoryNewestTimestamp != newest {
		t.Errorf("newest = %q, want %q", stats.UsageHistoryNewestTimestamp, newest)
	}
}
