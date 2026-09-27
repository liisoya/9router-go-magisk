package db

import (
	"fmt"
	"time"
)

// GetUsageDaily returns the daily usage JSON data for a given date key.
func (r *Repo) GetUsageDaily(dateKey string) (string, error) {
	var data string
	err := r.db.QueryRow(`SELECT data FROM usageDaily WHERE dateKey = ?`, dateKey).Scan(&data)
	if err != nil {
		return "", fmt.Errorf("get daily usage %s: %w", dateKey, err)
	}
	return data, nil
}

// InsertUsageHistory logs a single request's token usage to the usageHistory table.
func (r *Repo) InsertUsageHistory(provider, model, connectionID, apiKey, endpoint string, promptTokens, completionTokens int, cost float64, status string, totalTokens int, meta string, tokensJSON string) error {
	timestamp := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.Exec(
		`INSERT INTO usageHistory (timestamp, provider, model, connectionId, apiKey, endpoint, promptTokens, completionTokens, cost, status, tokens, meta)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		timestamp, provider, model, connectionID, apiKey, endpoint, promptTokens, completionTokens, cost, status, tokensJSON, meta,
	)
	if err != nil {
		return fmt.Errorf("insert usage history: %w", err)
	}
	return nil
}

// UpsertUsageDaily inserts or replaces a daily usage aggregation record.
// The data parameter should be a JSON string matching the 9router-go daily aggregation format.
// NOTE: INSERT OR REPLACE is an atomic full-row replace of the pre-merged JSON
// blob. Merging happens in-process (see handlers/chat/usage.go dailyUsageMu), so
// concurrent writers from MULTIPLE processes can still clobber each other. This
// is documented as single-writer unless the aggregation moves SQL-side.
func (r *Repo) UpsertUsageDaily(dateKey string, data string) error {
	_, err := r.db.Exec(
		`INSERT OR REPLACE INTO usageDaily (dateKey, data) VALUES (?, ?)`,
		dateKey, data,
	)
	if err != nil {
		return fmt.Errorf("upsert daily usage %s: %w", dateKey, err)
	}
	return nil
}

// InsertRequestDetail logs a request detail record for the Recent Requests dashboard tab.
func (r *Repo) InsertRequestDetail(id, provider, model, connectionID, status string, data string) error {
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	_, err := r.db.Exec(
		`INSERT OR IGNORE INTO requestDetails (id, timestamp, provider, model, connectionId, status, data) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, timestamp, provider, model, connectionID, status, data,
	)
	if err != nil {
		return fmt.Errorf("insert request detail %s: %w", id, err)
	}
	return nil
}

// UpdateConnectionLastUsed updates the lastUsedAt timestamp and increments
// consecutiveUseCount for the given provider connection.
func (r *Repo) UpdateConnectionLastUsed(connectionID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.Exec(
		`UPDATE providerConnections SET lastUsedAt = ?, consecutiveUseCount = COALESCE(consecutiveUseCount, 0) + 1 WHERE id = ?`,
		now, connectionID,
	)
	if err != nil {
		return fmt.Errorf("update connection last used %s: %w", connectionID, err)
	}
	return nil
}

// RotationTimestampFormat is a fixed-width RFC3339 with nanoseconds. The
// round-robin selector compares lastUsedAt as a string to stay allocation-free
// on the hot path, and that only works if the fractional part is zero-padded to
// a constant width: time.RFC3339Nano trims trailing zeros, which would make
// "…:00.5Z" sort after "…:00.500000001Z". Seconds are not enough either — two
// picks landing in the same second tie, and the tie-break would hand the same
// account back on every request.
const RotationTimestampFormat = "2006-01-02T15:04:05.000000000Z07:00"

// TouchConnectionRotation stamps a connection as the one just selected by the
// persistent round-robin and sets its consecutive-use counter to exactly
// `consecutive` (1 for a fresh pick, previous+1 while a sticky window holds).
// It deliberately sets the counter rather than incrementing it: the selector
// decides the value from the row it read, and a lost race should converge on a
// fresh window rather than drift upward forever.
func (r *Repo) TouchConnectionRotation(connectionID string, consecutive int) error {
	now := time.Now().UTC().Format(RotationTimestampFormat)
	_, err := r.db.Exec(
		`UPDATE providerConnections SET lastUsedAt = ?, consecutiveUseCount = ? WHERE id = ?`,
		now, consecutive, connectionID,
	)
	if err != nil {
		return fmt.Errorf("touch connection rotation %s: %w", connectionID, err)
	}
	return nil
}

// UsageHistoryRow represents a record from the usageHistory table.
type UsageHistoryRow struct {
	Timestamp        string
	Provider         string
	Model            string
	ConnectionID     string
	APIKey           string
	Endpoint         string
	PromptTokens     int
	CompletionTokens int
	Cost             float64
	Status           string
	Tokens           string
}

// GetUsageDailyRecent returns the most recent daily usage records up to limit.
func (r *Repo) GetUsageDailyRecent(limit int) ([]string, error) {
	rows, err := r.db.Query(`SELECT data FROM usageDaily ORDER BY dateKey DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent usageDaily: %w", err)
	}
	defer rows.Close()

	var res []string
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			continue
		}
		res = append(res, data)
	}
	return res, nil
}

// GetUsageHistorySince returns usage history records since the cutoff timestamp.
func (r *Repo) GetUsageHistorySince(cutoff string) ([]UsageHistoryRow, error) {
	rows, err := r.db.Query(`
		SELECT timestamp, COALESCE(provider, ''), COALESCE(model, ''), COALESCE(connectionId, ''),
		       COALESCE(apiKey, ''), COALESCE(endpoint, ''), COALESCE(promptTokens, 0),
		       COALESCE(completionTokens, 0), COALESCE(cost, 0.0), COALESCE(status, 'ok'), COALESCE(tokens, '{}')
		FROM usageHistory
		WHERE timestamp >= ?
		ORDER BY rowid DESC
	`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("query usageHistory since %s: %w", cutoff, err)
	}
	defer rows.Close()

	var res []UsageHistoryRow
	for rows.Next() {
		var row UsageHistoryRow
		if err := rows.Scan(
			&row.Timestamp, &row.Provider, &row.Model, &row.ConnectionID,
			&row.APIKey, &row.Endpoint, &row.PromptTokens, &row.CompletionTokens,
			&row.Cost, &row.Status, &row.Tokens,
		); err != nil {
			continue
		}
		res = append(res, row)
	}
	return res, nil
}

// GetRecentUsageHistory returns the latest N usage history records.
func (r *Repo) GetRecentUsageHistory(limit int) ([]UsageHistoryRow, error) {
	rows, err := r.db.Query(`
		SELECT timestamp, COALESCE(provider, ''), COALESCE(model, ''), COALESCE(connectionId, ''),
		       COALESCE(apiKey, ''), COALESCE(endpoint, ''), COALESCE(promptTokens, 0),
		       COALESCE(completionTokens, 0), COALESCE(cost, 0.0), COALESCE(status, 'ok'), COALESCE(tokens, '{}')
		FROM usageHistory
		ORDER BY rowid DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent usageHistory: %w", err)
	}
	defer rows.Close()

	var res []UsageHistoryRow
	for rows.Next() {
		var row UsageHistoryRow
		if err := rows.Scan(
			&row.Timestamp, &row.Provider, &row.Model, &row.ConnectionID,
			&row.APIKey, &row.Endpoint, &row.PromptTokens, &row.CompletionTokens,
			&row.Cost, &row.Status, &row.Tokens,
		); err != nil {
			continue
		}
		res = append(res, row)
	}
	return res, nil
}

// GetRequestDetailsPaged returns paged raw json strings and total count from requestDetails.
func (r *Repo) GetRequestDetailsPaged(limit, offset int) ([]string, int, error) {
	var total int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM requestDetails`).Scan(&total); err != nil {
		total = 0
	}

	rows, err := r.db.Query(`
		SELECT data FROM requestDetails
		ORDER BY timestamp DESC
		LIMIT ? OFFSET ?
	`, limit, offset)
	if err != nil {
		return nil, total, fmt.Errorf("query requestDetails paged: %w", err)
	}
	defer rows.Close()

	var res []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			continue
		}
		res = append(res, d)
	}
	return res, total, nil
}
