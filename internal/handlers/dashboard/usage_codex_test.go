package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"9router/proxy/internal/codexquota"
)

func TestFetchCodexUsage_RendersBothWindows(t *testing.T) {
	withCodexUsageServer(t, http.StatusOK, `{
		"plan_type": "plus",
		"rate_limit": {
			"primary_window":   {"used_percent": 65,  "reset_at": 1712345678},
			"secondary_window": {"used_percent": 100, "reset_at": 1712432078}
		},
		"rate_limit_reset_credits": {"available_count": 2}
	}`)

	res := fetchCodexUsage(t.Context(), "tok")
	if res.message != "" {
		t.Fatalf("unexpected message: %q", res.message)
	}
	if res.plan != "plus" {
		t.Errorf("plan = %q, want plus", res.plan)
	}
	if len(res.quotas) != 2 {
		t.Fatalf("expected 2 quota rows, got %d (%v)", len(res.quotas), res.quotas)
	}

	session := codexQuotaRow(t, res, "session")
	if got := codexNumber(t, session, "used"); got != 65 {
		t.Errorf("session used = %v, want 65", got)
	}
	// The dashboard reads quota.remaining directly and getConnectionQuotaRemaining
	// returns Infinity without it, breaking the Codex sort — so it must be present.
	if got := codexNumber(t, session, "remaining"); got != 35 {
		t.Errorf("session remaining = %v, want 35", got)
	}
	if got := codexNumber(t, session, "total"); got != 100 {
		t.Errorf("session total = %v, want 100", got)
	}
	if session["resetAt"] != "2024-04-05T19:34:38Z" {
		t.Errorf("session resetAt = %v, want 2024-04-05T19:34:38Z", session["resetAt"])
	}

	weekly := codexQuotaRow(t, res, "weekly")
	if got := codexNumber(t, weekly, "used"); got != 100 {
		t.Errorf("weekly used = %v, want 100", got)
	}
	if got := codexNumber(t, weekly, "remaining"); got != 0 {
		t.Errorf("weekly remaining = %v, want 0", got)
	}

	credits, ok := res.extra["resetCredits"].(map[string]any)
	if !ok {
		t.Fatalf("expected resetCredits in extra, got %v", res.extra)
	}
	if credits["availableCount"] != 2 {
		t.Errorf("availableCount = %v, want 2", credits["availableCount"])
	}
}

// An account with no 7d window gets a single row, not a fabricated Weekly
// placeholder — that is what produced the "7d=None" in the bug report, and
// upstream renders one row quietly rather than treating it as a failure.
func TestFetchCodexUsage_OmitsAbsentWindow(t *testing.T) {
	withCodexUsageServer(t, http.StatusOK, `{"rate_limit":{"primary_window":{"used_percent":0}}}`)

	res := fetchCodexUsage(t.Context(), "tok")
	if _, exists := res.quotas["weekly"]; exists {
		t.Errorf("expected no weekly row, got %v", res.quotas)
	}
	if _, exists := res.quotas["session"]; !exists {
		t.Errorf("expected a session row, got %v", res.quotas)
	}
}

func TestFetchCodexUsage_Non2xxIsBareMessage(t *testing.T) {
	withCodexUsageServer(t, http.StatusServiceUnavailable, `{}`)

	res := fetchCodexUsage(t.Context(), "tok")
	if !res.bare {
		t.Error("expected a bare result so the dashboard omits quotas entirely")
	}
	if !strings.Contains(res.message, "temporarily unavailable (503)") {
		t.Errorf("message = %q, want it to name status 503", res.message)
	}

	// toResponse must not attach an empty quotas map on this path, or the
	// dashboard draws an empty table instead of the message.
	if _, hasQuotas := res.toResponse()["quotas"]; hasQuotas {
		t.Errorf("expected no quotas key in the response, got %v", res.toResponse())
	}
}

func TestFetchCodexUsage_NoWindows(t *testing.T) {
	withCodexUsageServer(t, http.StatusOK, `{"plan_type":"pro","rate_limit":{}}`)

	res := fetchCodexUsage(t.Context(), "tok")
	if res.message == "" {
		t.Error("expected a message when no windows come back")
	}
	if len(res.quotas) != 0 {
		t.Errorf("expected no quota rows, got %v", res.quotas)
	}
}

func TestFetchCodexUsage_RequiresToken(t *testing.T) {
	res := fetchCodexUsage(context.Background(), "")
	if res.message == "" {
		t.Fatal("expected a message for a blank token")
	}
}

// The dispatch must route codex to the fetcher; before this the switch had no
// codex case and every Codex account fell through to the empty lock fallback.
func TestFetchProviderUsage_DispatchesCodex(t *testing.T) {
	withCodexUsageServer(t, http.StatusOK, `{"rate_limit":{"primary_window":{"used_percent":1}}}`)

	data := map[string]any{"accessToken": "tok", "apiKey": "tok"}
	res, ok := fetchProviderUsage(t.Context(), "codex", data)
	if !ok {
		t.Fatal("expected fetchProviderUsage to handle codex")
	}
	if _, hasSession := res.quotas["session"]; !hasSession {
		t.Errorf("expected a session row, got %v", res.quotas)
	}
}

func withCodexUsageServer(t *testing.T, status int, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("Authorization = %q, want %q", r.Header.Get("Authorization"), "Bearer tok")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	prev := codexquota.UsageURL
	codexquota.UsageURL = srv.URL
	t.Cleanup(func() {
		codexquota.UsageURL = prev
		srv.Close()
	})
}

func codexQuotaRow(t *testing.T, res usageResult, key string) map[string]any {
	t.Helper()
	row, ok := res.quotas[key].(map[string]any)
	if !ok {
		t.Fatalf("expected a %q row, got %v", key, res.quotas)
	}
	return row
}

func codexNumber(t *testing.T, row map[string]any, key string) float64 {
	t.Helper()
	v, ok := row[key].(float64)
	if !ok {
		t.Fatalf("expected numeric %q, got %v (%T)", key, row[key], row[key])
	}
	return v
}

// An account can hold reset credits while its rate-limit windows come back
// unparseable. The credit count rode in `extra`, and the no-windows branch
// returned before setting it — so the dashboard showed 0 and the chooser
// button stayed unusable for an account that had credits to spend.
func TestFetchCodexUsage_KeepsResetCreditsWhenNoWindowsParse(t *testing.T) {
	withCodexUsageServer(t, http.StatusOK, `{
		"plan_type": "plus",
		"rate_limit_reset_credits": {"available_count": 3}
	}`)

	res := fetchCodexUsage(t.Context(), "tok")
	if len(res.quotas) != 0 {
		t.Fatalf("expected no quota rows, got %v", res.quotas)
	}
	credits, ok := res.extra["resetCredits"].(map[string]any)
	if !ok {
		t.Fatalf("resetCredits missing from extra on the no-windows path: %v", res.extra)
	}
	if got, _ := credits["availableCount"].(int); got != 3 {
		t.Errorf("availableCount = %v, want 3", credits["availableCount"])
	}
}
