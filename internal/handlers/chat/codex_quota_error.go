package chat

import (
	json "encoding/json/v2"
	"net/http"
	"time"
)

// NoteCodexQuotaError records a Codex 429 against the connection and returns
// the reset time the upstream body promised, if any. Codex answers an
// exhausted quota with {"error":{"type":"usage_limit_reached","resets_at":…}}
// or resets_in_seconds (upstream open-sse/executors/codex.js parseError), which
// is more precise than the generic cooldown classification and than anything
// extractResetDuration can read out of that body.
//
// A 429 without the usage_limit_reached marker is a per-request or burst limit,
// not an exhausted window, and must not cache a block that outlives it.
func NoteCodexQuotaError(connectionID string, status int, body []byte) *time.Time {
	if connectionID == "" || status != http.StatusTooManyRequests {
		return nil
	}
	resetAt, ok := codexQuotaResetAt(body)
	if !ok {
		return nil
	}
	BlockCodexConnectionUntil(connectionID, resetAt)
	return &resetAt
}

// codexQuotaResetAt pulls the reset instant out of a usage_limit_reached body.
// resets_at is unix seconds; resets_in_seconds is relative to now. Both are
// ignored unless they resolve to a time in the future.
func codexQuotaResetAt(body []byte) (time.Time, bool) {
	var parsed struct {
		ResetsAt any `json:"resets_at"`
		Error    struct {
			Type            string `json:"type"`
			ResetsAt        any    `json:"resets_at"`
			ResetsInSeconds any    `json:"resets_in_seconds"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return time.Time{}, false
	}
	if parsed.Error.Type != "usage_limit_reached" {
		return time.Time{}, false
	}

	now := time.Now().UTC()
	if t, ok := codexAbsoluteReset(parsed.Error.ResetsAt); ok && t.After(now) {
		return t, true
	}
	if t, ok := codexAbsoluteReset(parsed.ResetsAt); ok && t.After(now) {
		return t, true
	}
	if secs, ok := codexFiniteNumber(parsed.Error.ResetsInSeconds); ok && secs > 0 {
		return now.Add(time.Duration(secs * float64(time.Second))), true
	}
	return time.Time{}, false
}

func codexAbsoluteReset(v any) (time.Time, bool) {
	secs, ok := codexFiniteNumber(v)
	if !ok || secs <= 0 {
		return time.Time{}, false
	}
	return time.Unix(int64(secs), 0).UTC(), true
}

func codexFiniteNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		if !isNaNOrInf(n) {
			return n, true
		}
	case string:
		if f, ok := parseFloatStrict(n); ok {
			return f, true
		}
	}
	return 0, false
}
