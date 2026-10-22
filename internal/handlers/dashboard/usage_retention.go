package dashboard

import (
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"9router/proxy/internal/handlerutil"
)

// Ledger retention actions.
//
// These are deliberately separate from /prune above, which trims requestDetails
// — diagnostics nothing bills from, and therefore something the background loop
// is allowed to do on its own. usageHistory is the billing ledger: it is what a
// per-key tokenQuota is measured from and what the `all` usage period reports, so
// a deletion there is an operator's explicit act with a visible size attached,
// never a side effect of pressing "Prune Expired Records".
//
// The retention *window* is a setting (usageHistoryRetentionDays), so the
// background loop can honour it; the *action* here takes the window from the
// request body and falls back to that setting, which lets an operator do a
// one-off trim without changing configuration.

// ledgerDaysCeiling bounds the window a request may name. It exists so a typo
// in the ?days parameter cannot be read as "delete nothing" — a nonsense value is
// refused rather than silently rounded into a no-op that looks like success.
const ledgerDaysCeiling = 36500 // 100 years

var (
	errBadLedgerDays      = errors.New("days must be a positive integer")
	errLedgerDaysTooWide  = errors.New("days exceeds the 100-year ceiling")
	errBadLedgerBefore    = errors.New("before must be an RFC3339 timestamp or YYYY-MM-DD date")
	errLedgerBeforeFuture = errors.New("before must be an instant in the past")
)

type pruneUsageRequest struct {
	// Days keeps ledger rows newer than this many days. It is the primary form:
	// the number the dashboard control already speaks.
	Days *int `json:"days"`
	// Before trims to an explicit instant instead, for a caller that wants an
	// exact boundary rather than "N days from now".
	Before string `json:"before"`
}

// HandlePruneUsageHistory handles POST /api/settings/database/prune-usage.
//
// ?dryRun=1 reports what would be removed without removing it. The dry run is
// the point of the endpoint's shape: an operator about to delete millions of
// billing rows can see the row count, the window and the token total that will
// be carried into per-key budgets before committing to it.
func (h *DashboardHandler) HandlePruneUsageHistory(w http.ResponseWriter, r *http.Request) {
	if h.Repo == nil {
		handlerutil.WriteJSONError(w, http.StatusServiceUnavailable, "database repo unavailable")
		return
	}

	retention, err := h.ledgerRetention(r)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if retention <= 0 {
		handlerutil.WriteJSONError(w, http.StatusBadRequest,
			"give ?days (or {\"days\":N}) greater than zero: to keep the ledger forever, set retention to Off instead of pruning")
		return
	}

	if isDryRun(r) {
		rows, cutoff, err := h.Repo.CountExpiredUsageHistory(r.Context(), retention)
		if err != nil {
			handlerutil.WriteJSONError(w, http.StatusInternalServerError, "failed to inspect usage history: "+err.Error())
			return
		}
		handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
			"success":     true,
			"dryRun":      true,
			"wouldDelete": rows,
			"cutoff":      cutoff,
		})
		return
	}

	result, err := h.Repo.PruneUsageHistory(r.Context(), retention)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, "failed to prune usage history: "+err.Error())
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"success":       true,
		"dryRun":        false,
		"pruned":        result.Rows,
		"carriedTokens": result.CarriedTokens,
		"cutoff":        result.Cutoff,
	})
}

// ledgerRetention resolves the window the request asks for: its own body or
// query parameter first, then the configured setting, and only then the
// dashboard's default. A request that names neither is not an error — it means
// "use my configured window", which is what the button next to the setting sends.
func (h *DashboardHandler) ledgerRetention(r *http.Request) (time.Duration, error) {
	days, err := requestedLedgerDays(r)
	if err != nil {
		return 0, err
	}
	if days == 0 {
		if settings, err := h.Repo.GetSettings(); err == nil && settings != nil {
			days = settings.UsageHistoryRetentionDays
		}
	}
	if days == 0 {
		return 0, nil
	}
	return time.Duration(days) * 24 * time.Hour, nil
}

// requestedLedgerDays reads the days parameter, which may arrive either as the
// JSON body the SPA posts or as a query parameter a script uses. The query
// parameter wins when both are present so a caller can override a saved body
// without rewriting it.
func requestedLedgerDays(r *http.Request) (int, error) {
	if raw := r.URL.Query().Get("days"); raw != "" {
		return parseLedgerDays(raw)
	}
	if raw := r.URL.Query().Get("before"); raw != "" {
		return daysFromBefore(raw)
	}
	var body pruneUsageRequest
	if parsed := decodeOptionalJSON(r, &body); parsed {
		if body.Days != nil {
			if err := validateLedgerDays(*body.Days); err != nil {
				return 0, err
			}
			return *body.Days, nil
		}
		if body.Before != "" {
			return daysFromBefore(body.Before)
		}
	}
	return 0, nil
}

func parseLedgerDays(raw string) (int, error) {
	days, err := strconv.Atoi(raw)
	if err != nil {
		return 0, errBadLedgerDays
	}
	if err := validateLedgerDays(days); err != nil {
		return 0, err
	}
	return days, nil
}

func validateLedgerDays(days int) error {
	if days <= 0 {
		return errBadLedgerDays
	}
	if days > ledgerDaysCeiling {
		return errLedgerDaysTooWide
	}
	return nil
}

// daysFromBefore turns an explicit cutoff into the equivalent "older than N days"
// window the repository takes, so both request forms share one code path and one
// boundary rule. The derived window is validated against the same ceiling as a
// literal day count, so an ancient date cannot ask for a scan the ceiling exists
// to bound.
func daysFromBefore(before string) (int, error) {
	when, err := time.Parse(time.RFC3339, before)
	if err != nil {
		when, err = time.Parse("2006-01-02", before)
		if err != nil {
			return 0, errBadLedgerBefore
		}
	}
	since := time.Since(when)
	if since <= 0 {
		return 0, errLedgerBeforeFuture
	}
	// A cutoff less than a day in the past rounds to zero, which the caller reads
	// as "not configured" and the handler then refuses with the wrong
	// explanation. One day is the smallest window that means anything here.
	days := max(int(since.Hours()/24), 1)
	if err := validateLedgerDays(days); err != nil {
		return 0, err
	}
	return days, nil
}

func isDryRun(r *http.Request) bool {
	switch r.URL.Query().Get("dryRun") {
	case "1", "true", "yes":
		return true
	}
	return false
}

// decodeOptionalJSON unmarshals r.Body into v when a body was supplied, leaving
// v untouched and returning false when the body was empty.
func decodeOptionalJSON(r *http.Request, v any) bool {
	if r.Body == nil {
		return false
	}
	body, err := io.ReadAll(r.Body)
	if err != nil || len(body) == 0 {
		return false
	}
	return json.Unmarshal(body, v) == nil
}
