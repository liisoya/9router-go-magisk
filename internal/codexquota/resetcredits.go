package codexquota

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Reset-credit support ports the contract of OmniRoute's
// src/lib/usage/codexResetCredits.ts. The behaviour is preserved — endpoints,
// payload shapes, the availability filter, the expiry ordering and the typed
// error codes — but the implementation is native Go, not a transliteration.

// ResetCreditsURL lists the credits on an account. Settable so tests can point
// it at an httptest server.
var ResetCreditsURL = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits"

// ConsumeResetCreditsURL redeems one credit.
var ConsumeResetCreditsURL = ResetCreditsURL + "/consume"

// ResetCredit is one redeemable credit as the dashboard shows it. ID is
// deliberately not serialised outward: the browser must not be able to forge a
// credit id, so the handler hands back a SelectionToken and the server maps it
// back (see PublicCredit).
type ResetCredit struct {
	ID        string
	ResetType string
	Status    string
	GrantedAt string
	ExpiresAt string
	Title     string
	Desc      string
}

// ResetCreditList is the parsed list response.
type ResetCreditList struct {
	Credits []ResetCredit

	AvailableCount int
}

// normalizeOutcome folds an outcome token to lower-case alphanumerics so
// "already_redeemed", "alreadyRedeemed" and "ALREADY REDEEMED" all compare
// equal, matching OmniRoute's normalizeOutcome.
func normalizeOutcome(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// extractOutcome reads the outcome from a payload: either the payload is
// itself a bare token, or it carries one under a known key.
func extractOutcome(payload any) string {
	if s, ok := payload.(string); ok {
		if n := normalizeOutcome(s); n != "" {
			return n
		}
	}
	record, ok := payload.(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range []string{"code", "outcome", "status", "result", "type"} {
		if s, ok := record[key].(string); ok {
			if n := normalizeOutcome(s); n != "" {
				return n
			}
		}
	}
	return ""
}

// stringField returns the first key holding a non-empty string.
func stringField(record map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := record[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// ResetCreditError is a typed, client-readable failure. OmniRoute raises the
// same status/code pairs, and the dashboard renders `code` in a toast, so the
// distinction between "no credit available" and "nothing to reset" survives the
// port instead of collapsing into one generic error.
type ResetCreditError struct {
	Status  int
	Code    string
	Message string
}

func (e *ResetCreditError) Error() string {
	return fmt.Sprintf("codexquota: %s (%s)", e.Message, e.Code)
}

// resetError builds a ResetCreditError.
func resetError(status int, code, message string) *ResetCreditError {
	return &ResetCreditError{Status: status, Code: code, Message: message}
}

// unavailableResetStatuses are the states that make a credit unusable
// regardless of its expiry date.
var unavailableResetStatuses = map[string]bool{
	"consumed": true, "redeeming": true, "redeemed": true,
	"used": true, "expired": true, "unavailable": true,
}

// isUnavailableResetCredit reports whether a credit must be hidden from the
// chooser. OmniRoute hides the same set, which is why the dashboard must not
// offer a credit the server will then refuse to redeem.
func isUnavailableResetCredit(record map[string]any) bool {
	status := normalizeOutcome(stringField(record, "status", "state", "outcome", "result", "code"))
	if status != "" && unavailableResetStatuses[status] {
		return true
	}
	if consumed, ok := record["consumed"].(bool); ok && consumed {
		return true
	}
	if redeemed, ok := record["redeemed"].(bool); ok && redeemed {
		return true
	}
	if available, ok := record["available"].(bool); ok && !available {
		return true
	}
	return false
}

// parseResetCredit converts one raw credit. now decides expiry, so a test can
// pin the clock instead of depending on the wall clock.
func parseResetCredit(value any, now time.Time) (ResetCredit, bool) {
	record, ok := value.(map[string]any)
	if !ok || len(record) == 0 || isUnavailableResetCredit(record) {
		return ResetCredit{}, false
	}
	id := stringField(record, "credit_id", "creditId", "id")
	if id == "" {
		return ResetCredit{}, false
	}
	credit := ResetCredit{
		ID:        id,
		ResetType: stringField(record, "reset_type", "resetType"),
		Status:    stringField(record, "status", "state"),
		GrantedAt: stringField(record, "granted_at", "grantedAt"),
		Title:     stringField(record, "title"),
		Desc:      stringField(record, "description"),
	}
	if expiresAt, present := optionalTimestamp(record, "expires_at", "expiresAt", "expiration_at", "expirationAt"); present && expiresAt != "" {
		if parsed, err := time.Parse(time.RFC3339, expiresAt); err == nil && !parsed.After(now) {
			return ResetCredit{}, false
		}
		credit.ExpiresAt = expiresAt
	}
	return credit, true
}

// optionalTimestamp distinguishes "key absent" from "key present but null",
// so an explicit null expiry is not mistaken for a missing one.
func optionalTimestamp(record map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		value, present := record[key]
		if !present {
			continue
		}
		if value == nil {
			return "", true
		}
		if s, ok := value.(string); ok {
			return strings.TrimSpace(s), true
		}
	}
	return "", false
}

// resetCreditCandidates finds the credit array in a list payload. Codex has
// returned both a bare array and several wrapped shapes, so all of them are
// accepted rather than pinned to one.
func resetCreditCandidates(payload any) []any {
	if list, ok := payload.([]any); ok {
		return list
	}
	record, ok := payload.(map[string]any)
	if !ok {
		return nil
	}
	for _, key := range []string{
		"credits", "reset_credits", "resetCredits",
		"rate_limit_reset_credits", "rateLimitResetCredits", "items", "data",
	} {
		if list, ok := record[key].([]any); ok {
			return list
		}
	}
	return nil
}

// ParseAvailableResetCredits turns a decoded list payload into the credits the
// user may actually redeem, soonest expiry first. A credit with no expiry
// sorts last rather than first, so the one that frees a quota soonest is the
// one the dashboard offers by default.
func ParseAvailableResetCredits(payload any, now time.Time) (ResetCreditList, bool) {
	credits := make([]ResetCredit, 0, 8)
	for _, item := range resetCreditCandidates(payload) {
		if credit, ok := parseResetCredit(item, now); ok {
			credits = append(credits, credit)
		}
	}
	sort.SliceStable(credits, func(i, j int) bool {
		return expirySortValue(credits[i]).Before(expirySortValue(credits[j]))
	})

	count := len(credits)
	if record, ok := payload.(map[string]any); ok {
		if reported, ok := numericField(record, "available_count", "availableCount"); ok {
			count = int(reported)
			if count < 0 {
				count = 0
			}
		}
	}
	return ResetCreditList{Credits: credits, AvailableCount: count}, true
}

// expirySortValue orders a credit, treating a missing or unparseable expiry as
// infinitely far away.
func expirySortValue(credit ResetCredit) time.Time {
	if credit.ExpiresAt == "" {
		return time.Unix(1<<62, 0)
	}
	parsed, err := time.Parse(time.RFC3339, credit.ExpiresAt)
	if err != nil {
		return time.Unix(1<<62, 0)
	}
	return parsed
}

// numericField reads a count that may arrive as a JSON number or a string.
func numericField(record map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		switch v := record[key].(type) {
		case float64:
			return v, true
		case string:
			if parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
				return parsed, true
			}
		}
	}
	return 0, false
}

// SelectResetCredit resolves the credit to redeem. A requested id must still
// be available, otherwise the request is refused rather than silently
// redeeming a different credit than the user picked.
func SelectResetCredit(list ResetCreditList, requestedID string) (ResetCredit, error) {
	if requestedID = strings.TrimSpace(requestedID); requestedID != "" {
		for _, credit := range list.Credits {
			if credit.ID == requestedID {
				return credit, nil
			}
		}
		return ResetCredit{}, resetError(409, "selected_credit_unavailable",
			"The selected Codex reset credit is no longer available.")
	}
	if len(list.Credits) > 0 {
		return list.Credits[0], nil
	}
	return ResetCredit{}, resetError(409, "no_credit",
		"No Codex reset credits are available.")
}

// Consume outcomes. "alreadyRedeemed" is a success, not a failure: the
// window was already reset, which is what the user asked for.
const (
	OutcomeReset           = "reset"
	OutcomeAlreadyRedeemed = "alreadyRedeemed"
)

// ParseConsumeOutcome maps a consume response onto an outcome, raising the
// typed errors OmniRoute defines. "no credit" and "nothing to reset" stay
// distinct so the dashboard can say which one happened.
func ParseConsumeOutcome(payload any) (string, error) {
	switch outcome := extractOutcome(payload); outcome {
	case "reset":
		return OutcomeReset, nil
	case "alreadyredeemed":
		return OutcomeAlreadyRedeemed, nil
	case "nocredit", "nocredits":
		return "", resetError(409, "no_credit", "No Codex reset credits are available.")
	case "nothingtoreset":
		return "", resetError(409, "nothing_to_reset",
			"No exhausted Codex usage limit can be reset right now.")
	default:
		return "", resetError(502, "unknown_reset_credit_response",
			"Codex returned an unknown reset-credit response.")
	}
}

// knownConsumeError raises the two refusals that are meaningful on their own.
// An upstream error that is neither is left to the caller's generic
// status handling, so a real 500 is not mislabelled as "no credit".
func knownConsumeError(payload any) error {
	switch extractOutcome(payload) {
	case "nocredit", "nocredits":
		return resetError(409, "no_credit", "No Codex reset credits are available.")
	case "nothingtoreset":
		return resetError(409, "nothing_to_reset",
			"No exhausted Codex usage limit can be reset right now.")
	}
	return nil
}
