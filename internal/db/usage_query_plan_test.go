package db

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

// #138: both usageHistory readers ordered by `rowid`, which no index can
// satisfy, so SQLite ran a full table scan on every dashboard poll — measured at
// 9.5ms on a 100k-row table against 0.0ms for the identical query ordered by
// `timestamp`, which idx_uh_ts covers. The cost grew with the ledger while the
// dashboard's Overview tab polls it every 5 seconds.
//
// These tests pin the two halves of the contract: the plan the three readers
// actually pay for, measured against the production schema, and the row order
// callers rely on (usage_window_cache folds `since` newest-first, `between`
// oldest-first).

// seedUsageHistoryRows inserts n rows one second apart, oldest first.
func seedUsageHistoryRows(t *testing.T, database *sql.DB, n int) {
	t.Helper()

	tx, err := database.Begin()
	if err != nil {
		t.Fatalf("begin seed transaction: %v", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO usageHistory
		(timestamp, provider, model, endpoint, promptTokens, completionTokens, cost, status, tokens)
		VALUES (?, 'openai', 'gpt-5.5', '/v1/chat/completions', 10, 5, 0.1, 'success', '{}')`)
	if err != nil {
		t.Fatalf("prepare seed statement: %v", err)
	}
	defer stmt.Close()

	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		ts := base.Add(time.Duration(i) * time.Second).Format(time.RFC3339)
		if _, err := stmt.Exec(ts); err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit seed: %v", err)
	}
}

// explainQueryPlan returns the plan rows SQLite reports for a statement, run
// with its real bound arguments.
func explainQueryPlan(t *testing.T, database *sql.DB, query string, args ...any) []string {
	t.Helper()

	rows, err := database.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
	}
	defer rows.Close()

	var details []string
	for rows.Next() {
		var (
			id, parent, notused int
			detail              string
		)
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan plan row: %v", err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate plan: %v", err)
	}
	if len(details) == 0 {
		t.Fatal("EXPLAIN QUERY PLAN returned no rows")
	}
	return details
}

func TestUsageHistoryReadsUseTheTimestampIndex(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	if err := EnsureCoreSchema(database); err != nil {
		t.Fatalf("EnsureCoreSchema: %v", err)
	}
	seedUsageHistoryRows(t, database, 5000)

	tests := []struct {
		name  string
		query string
		args  []any
	}{
		{name: "GetUsageHistorySince", query: usageHistorySinceQuery, args: []any{"2026-03-01T01:00:00Z"}},
		{name: "GetUsageHistoryBetween", query: usageHistoryBetweenQuery, args: []any{"2026-03-01T01:00:00Z", "2026-03-01T02:00:00Z"}},
		{name: "GetRecentUsageHistory", query: usageHistoryRecentQuery, args: []any{60}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := explainQueryPlan(t, database, tt.query, tt.args...)

			usesIndex := false
			for _, detail := range plan {
				t.Logf("plan: %s", detail)
				if strings.Contains(detail, "idx_uh_ts") {
					usesIndex = true
				}
				// A plan row that scans usageHistory without an index is the
				// full table scan this issue is about.
				if strings.Contains(detail, "SCAN usageHistory") && !strings.Contains(detail, "USING INDEX") {
					t.Errorf("full table scan in plan: %q", detail)
				}
			}
			if !usesIndex {
				t.Errorf("plan %v never names idx_uh_ts, so the ORDER BY cannot be served by the index", plan)
			}
		})
	}
}

// TestUsageHistoryReadersOrderByTimestampNotInsertion is the behavioural half:
// with `rowid` order the readers returned rows in the order they were written,
// which is indistinguishable from timestamp order in a clean test and wildly
// different in production, where a late retry writes an older timestamp after a
// newer one. The rows below are written in that shape.
func TestUsageHistoryReadersOrderByTimestampNotInsertion(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	if err := EnsureCoreSchema(database); err != nil {
		t.Fatalf("EnsureCoreSchema: %v", err)
	}

	// Insertion order (and therefore rowid order) is deliberately the reverse of
	// timestamp order.
	insert := func(ts string) {
		t.Helper()
		if _, err := database.Exec(`INSERT INTO usageHistory
			(timestamp, provider, model, endpoint, promptTokens, completionTokens, cost, status, tokens)
			VALUES (?, 'openai', 'gpt-5.5', '/v1/chat/completions', 10, 5, 0.1, 'success', '{}')`, ts); err != nil {
			t.Fatalf("insert %s: %v", ts, err)
		}
	}
	insert("2026-03-01T00:00:00Z") // oldest, written first
	insert("2026-03-03T00:00:00Z") // newest, written second
	insert("2026-03-02T00:00:00Z") // middle, written last

	repo := NewRepo(database)

	since, err := repo.GetUsageHistorySince("2026-02-28T00:00:00Z")
	if err != nil {
		t.Fatalf("GetUsageHistorySince: %v", err)
	}
	if got := timestampsOf(since); !equalStrings(got, []string{
		"2026-03-03T00:00:00Z", "2026-03-02T00:00:00Z", "2026-03-01T00:00:00Z",
	}) {
		t.Errorf("GetUsageHistorySince order = %v, want newest-first by timestamp", got)
	}

	recent, err := repo.GetRecentUsageHistory(2)
	if err != nil {
		t.Fatalf("GetRecentUsageHistory: %v", err)
	}
	if got := timestampsOf(recent); !equalStrings(got, []string{"2026-03-03T00:00:00Z", "2026-03-02T00:00:00Z"}) {
		t.Errorf("GetRecentUsageHistory order = %v, want the two newest timestamps", got)
	}

	between, err := repo.GetUsageHistoryBetween("2026-03-01T00:00:00Z", "2026-03-03T00:00:00Z")
	if err != nil {
		t.Fatalf("GetUsageHistoryBetween: %v", err)
	}
	if got := timestampsOf(between); !equalStrings(got, []string{"2026-03-02T00:00:00Z", "2026-03-03T00:00:00Z"}) {
		t.Errorf("GetUsageHistoryBetween order = %v, want oldest-first by timestamp", got)
	}
}

func timestampsOf(rows []UsageHistoryRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Timestamp)
	}
	return out
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
