package db

import (
	"context"
	"database/sql"
	"fmt"

	"9router/proxy/internal/handlerutil"
)

// Resetting a key's token budget (#365).
//
// tokenQuota is a lifetime ceiling and the spend it is measured against is
// derived from usageHistory rather than kept as a counter. That derivation is
// the property worth keeping — there is no counter to drift, replay or
// double-count under concurrency — but it also meant an operator had no way to
// lift an exhausted key without deleting its usage rows.
//
// quotaResetId is that missing knob: a high-water mark on the key row, holding
// the usageHistory row id the key's budget was last reset at. Spend is summed
// from that mark forward, so a reset is one small write and the history stays
// intact for analytics.
//
// It is a row id and NOT a timestamp on purpose. usageHistory.timestamp is
// RFC3339 at second precision, so a reset issued in the same second as the
// spend that exhausted the key would compare equal and keep charging the key —
// exactly the case an operator hits when they reset a key they just watched
// fail. The autoincrement id is monotonic per insert, so the boundary is exact
// at any rate, and `id > ?` needs no clock agreement between the two writes.
const quotaResetColumn = "quotaResetId"

// ResetApiKeyQuota marks a key as fresh from the current end of its history and
// returns the mark. Requests from now on spend against a new budget while
// usageHistory keeps recording every row.
//
// The read and the write share one transaction so a request landing between
// them cannot slip its usage row on the wrong side of the boundary: the mark
// always covers everything already recorded, and never anything after it.
//
// A key that does not exist is reported as such rather than silently accepted:
// the operator asked for a specific key, and a 200 for a key that is not there
// would hide a stale dashboard row.
func (r *Repo) ResetApiKeyQuota(ctx context.Context, keyID string) (int, error) {
	if r == nil || r.db == nil {
		return 0, fmt.Errorf("reset api key quota: nil repo")
	}
	if keyID == "" {
		return 0, fmt.Errorf("reset api key quota: empty apiKey id")
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("reset api key quota %s: begin: %w", keyID, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful Commit

	var watermark int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM usageHistory`).Scan(&watermark); err != nil {
		return 0, fmt.Errorf("reset api key quota %s: read history mark: %w", keyID, err)
	}

	res, err := tx.ExecContext(ctx, `UPDATE apiKeys SET `+quotaResetColumn+` = ? WHERE id = ?`, watermark, keyID)
	if err != nil {
		return 0, fmt.Errorf("reset api key quota %s: %w", keyID, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return 0, errNoSuchApiKey
	}

	// The carryover holds spend that lived in rows the ledger prune deleted. A
	// reset says "measure from here", and "here" is after everything already
	// recorded — including that carried spend. Leaving it would charge the key
	// for tokens it had just been forgiven, so it is cleared in the same
	// transaction as the mark: the reader can never observe a new mark with the
	// old carryover still counted.
	if _, err := tx.ExecContext(ctx, `DELETE FROM usageLedgerCarryover WHERE keyId = ?`, keyID); err != nil {
		return 0, fmt.Errorf("reset api key quota %s: clear carried usage: %w", keyID, err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("reset api key quota %s: commit: %w", keyID, err)
	}
	return int(watermark), nil
}

// QuotaResetID returns the usageHistory row id a key's budget is measured from,
// or 0 when it has never been reset. An unknown key, and a database created
// before this column existed, both answer 0 — which is the pre-#365 lifetime
// sum, not an error.
func (r *Repo) QuotaResetID(ctx context.Context, keyID string) (int, error) {
	if r == nil || r.db == nil || keyID == "" {
		return 0, nil
	}
	var mark sql.NullInt64
	err := r.db.QueryRowContext(ctx,
		`SELECT `+quotaResetColumn+` FROM apiKeys WHERE id = ? LIMIT 1`, keyID,
	).Scan(&mark)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, nil
	}
	if mark.Valid && mark.Int64 > 0 {
		return int(mark.Int64), nil
	}
	return 0, nil
}

// quotaResetIDForKey resolves the mark by the key's own secret column, which is
// what the limiter holds. An unknown value has no mark and keeps the lifetime
// sum.
func (r *Repo) quotaResetIDForKey(ctx context.Context, rawKey string) (int, error) {
	if r == nil || r.db == nil || rawKey == "" {
		return 0, nil
	}
	var mark sql.NullInt64
	err := r.db.QueryRowContext(ctx,
		`SELECT `+quotaResetColumn+` FROM apiKeys WHERE key = ? LIMIT 1`, rawKey,
	).Scan(&mark)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, nil
	}
	if mark.Valid && mark.Int64 > 0 {
		return int(mark.Int64), nil
	}
	return 0, nil
}

// GetApiKeyTokenUsageByID is the dashboard's reader: it resolves the key's
// history spellings and reset mark from the row the id names, so the
// operator's progress bar and the limiter's own refusal are computed from the
// same numbers.
func (r *Repo) GetApiKeyTokenUsageByID(ctx context.Context, keyID string) (int, error) {
	if r == nil || r.db == nil || keyID == "" {
		return 0, nil
	}
	row, err := r.GetApiKeyByID(keyID)
	if err != nil {
		return 0, err
	}
	if row == nil {
		return 0, nil
	}
	mark, err := r.QuotaResetID(ctx, keyID)
	if err != nil {
		return 0, err
	}
	return r.tokenUsageSince(ctx, row.Key, mark)
}

// tokenUsageSince sums a key's prompt+completion tokens recorded after a reset
// mark. A mark of 0 means "the key's whole life", which is the pre-#365 answer.
//
// Matching is by the raw key's masked form as well, because that is what this
// gateway writes into usageHistory (maskAPIKey) and rows the upstream Next.js
// dashboard wrote keep the full key: both spellings are summed.
//
// clientKey is the column this gateway writes the caller's own key into (#365).
// The apiKey terms keep rows written by the upstream Next.js dashboard counting:
// that ledger stored the client key in apiKey, where this one stores the upstream
// credential, so both spellings are summed.
//
// The carryover term is what keeps the sum exact once an operator prunes the
// ledger (usage_retention_ledger.go): spend that lived in deleted rows is kept in
// usageLedgerCarryover and added back here. It is resolved by the same key
// secret this query already holds rather than by an id, so no caller has to know
// one. A key whose row is absent resolves to no carryover, which is the same
// answer as a key that never had one — and the ledger prune drops orphaned
// carryover rows, so that only happens for a key created after a prune.
//
// The history sum and the carryover are read in one statement so a prune landing
// between two reads cannot report a spend that is neither the before nor the
// after value.
//
// No index is added for the history predicate: it is an OR across two columns,
// which no single-column index serves, and the alternative — a join on the
// apiKeys table — costs more than the scan it replaces. The carryover lookup is a
// primary-key hit on a table with one row per client key.
func (r *Repo) tokenUsageSince(ctx context.Context, rawKey string, mark int) (int, error) {
	if rawKey == "" {
		return 0, nil
	}
	history := `COALESCE(SUM(COALESCE(promptTokens,0) + COALESCE(completionTokens,0)), 0)
	   FROM usageHistory WHERE (clientKey = ? OR apiKey = ? OR apiKey = ?)`
	args := []any{handlerutil.MaskAPIKey(rawKey), rawKey, handlerutil.MaskAPIKey(rawKey)}
	if mark > 0 {
		history += ` AND id > ?`
		args = append(args, mark)
	}
	query := `SELECT (SELECT ` + history + `)
	   + COALESCE((SELECT tokens FROM usageLedgerCarryover
	                WHERE keyId = (SELECT id FROM apiKeys WHERE key = ? LIMIT 1)), 0)`
	args = append(args, rawKey)

	var used int64
	if err := r.db.QueryRowContext(ctx, query, args...).Scan(&used); err != nil {
		return 0, fmt.Errorf("get api key token usage: %w", err)
	}
	return int(used), nil
}
