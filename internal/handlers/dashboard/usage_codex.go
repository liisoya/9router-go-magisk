package dashboard

import (
	"context"
	"errors"
	"fmt"
	"time"

	"9router/proxy/internal/codexquota"
)

// Codex usage fetcher — port of open-sse/services/usage/codex.js
// (getCodexUsage). Codex reports its quota through the wham/usage endpoint
// rather than response headers, so without this case the tracker showed
// "Account active. No quota limits tracked." for every Codex OAuth account.

func fetchCodexUsage(ctx context.Context, accessToken string) usageResult {
	if accessToken == "" {
		return usageResult{message: "Codex access token not available. Please re-authorize the connection."}
	}

	usage, err := codexquota.Fetch(ctx, nil, accessToken)
	if err != nil {
		// Upstream returns a message with NO quotas key on a non-2xx, so the
		// dashboard hides the table rather than drawing an empty one.
		var statusErr *codexquota.StatusError
		if errors.As(err, &statusErr) {
			return usageResult{
				message: fmt.Sprintf("Codex connected. Usage API temporarily unavailable (%d).", statusErr.Status),
				bare:    true,
			}
		}
		return usageResult{message: fmt.Sprintf("Failed to fetch Codex usage: %v", err)}
	}

	quotas := make(map[string]any, 6)
	addCodexWindow(quotas, "session", usage.Session)
	addCodexWindow(quotas, "weekly", usage.Weekly)
	addCodexWindow(quotas, "review_session", usage.ReviewSession)
	addCodexWindow(quotas, "review_weekly", usage.ReviewWeekly)
	addCodexWindow(quotas, "spark_session", usage.SparkSession)
	addCodexWindow(quotas, "spark_weekly", usage.SparkWeekly)

	if len(quotas) == 0 {
		return usageResult{plan: usage.Plan, message: "Codex connected, but no rate-limit windows were returned."}
	}

	// resetCredits is read by the dashboard at QuotaTrackerView.svelte's
	// getCodexResetCreditCount off the raw response, so it rides in extra.
	return usageResult{
		plan:   usage.Plan,
		quotas: quotas,
		extra:  map[string]any{"resetCredits": map[string]any{"availableCount": usage.ResetCredits}},
	}
}

// addCodexWindow renders one window in upstream's shape: total is always 100
// and `remaining` is an explicit field, because the dashboard's codex branch
// reads quota.remaining directly (it never re-derives it from used/total) and
// getConnectionQuotaRemaining returns Infinity without it, breaking the sort.
// A nil window is skipped — an account with no 7d cap gets no Weekly row.
func addCodexWindow(quotas map[string]any, key string, w *codexquota.Window) {
	if w == nil {
		return
	}
	quota := map[string]any{
		"used":      w.UsedPercent,
		"total":     float64(100),
		"remaining": w.Remaining(),
		"resetAt":   nil,
		"unlimited": false,
	}
	if w.ResetAt != nil {
		quota["resetAt"] = w.ResetAt.UTC().Format(time.RFC3339)
	}
	quotas[key] = quota
}
