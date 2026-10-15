package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// withClaudeUsageServer points the fetcher at a stub and resets the per-token
// state so tests do not see each other's cache.
func withClaudeUsageServer(t *testing.T, handler http.HandlerFunc) *int32 {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		handler(w, r)
	}))
	prev := claudeUsageURL
	claudeUsageURL = srv.URL
	claudeUsageMu.Lock()
	claudeUsageState = map[string]*claudeUsageEntry{}
	claudeUsageMu.Unlock()
	t.Cleanup(func() {
		claudeUsageURL = prev
		srv.Close()
	})
	return &calls
}

const claudeUsageBody = `{
	"five_hour":  {"utilization": 87, "resets_at": "2026-10-11T20:00:00Z"},
	"seven_day":  {"utilization": 12.5, "resets_at": "2026-10-17T10:00:00Z"},
	"seven_day_opus": {"utilization": 40, "resets_at": "2026-10-17T10:00:00Z"},
	"seven_day_oauth_apps": null,
	"limits": [
		{"kind": "weekly_scoped", "percent": 33, "resets_at": "2026-10-17T10:00:00Z",
		 "scope": {"model": {"display_name": "Fable"}}},
		{"kind": "session", "percent": 90}
	]
}`

func TestFetchClaudeUsage_RendersWindows(t *testing.T) {
	withClaudeUsageServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok-render" {
			t.Errorf("Authorization = %q", got)
		}
		if r.Header.Get("anthropic-beta") != claudeUsageBeta {
			t.Errorf("anthropic-beta = %q", r.Header.Get("anthropic-beta"))
		}
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "claude-cli/") {
			t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
		}
		_, _ = w.Write([]byte(claudeUsageBody))
	})

	res := fetchClaudeUsage(t.Context(), "tok-render")
	if res.message != "" {
		t.Fatalf("unexpected message %q", res.message)
	}
	if res.plan != "Claude Code" {
		t.Errorf("plan = %q", res.plan)
	}
	want := map[string]float64{
		"session (5h)":      87,
		"weekly (7d)":       12.5,
		"weekly opus (7d)":  40,
		"weekly fable (7d)": 33,
	}
	if len(res.quotas) != len(want) {
		t.Fatalf("got %d rows (%v), want %d", len(res.quotas), res.quotas, len(want))
	}
	for name, used := range want {
		row := codexQuotaRow(t, res, name)
		if got := codexNumber(t, row, "used"); got != used {
			t.Errorf("%s used = %v, want %v", name, got, used)
		}
		if got := codexNumber(t, row, "remaining"); got != 100-used {
			t.Errorf("%s remaining = %v, want %v", name, got, 100-used)
		}
		if got := codexNumber(t, row, "total"); got != 100 {
			t.Errorf("%s total = %v, want 100", name, got)
		}
	}
	if got := codexQuotaRow(t, res, "session (5h)")["resetAt"]; got != "2026-10-11T20:00:00Z" {
		t.Errorf("session resetAt = %v", got)
	}
}

func TestFetchClaudeUsage_ClampsUtilization(t *testing.T) {
	withClaudeUsageServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"five_hour":{"utilization":140},"seven_day":{"utilization":-3}}`))
	})
	res := fetchClaudeUsage(t.Context(), "tok-clamp")
	if got := codexNumber(t, codexQuotaRow(t, res, "session (5h)"), "remaining"); got != 0 {
		t.Errorf("remaining = %v, want 0", got)
	}
	if got := codexNumber(t, codexQuotaRow(t, res, "weekly (7d)"), "used"); got != 0 {
		t.Errorf("used = %v, want 0", got)
	}
	if codexQuotaRow(t, res, "session (5h)")["resetAt"] != nil {
		t.Errorf("missing resets_at must render as nil")
	}
}

func TestFetchClaudeUsage_NoWindows(t *testing.T) {
	withClaudeUsageServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"five_hour":null,"seven_day":null}`))
	})
	res := fetchClaudeUsage(t.Context(), "tok-empty")
	if len(res.quotas) != 0 || res.message == "" {
		t.Fatalf("expected a message and no rows, got %+v", res)
	}
}

func TestFetchClaudeUsage_RequiresToken(t *testing.T) {
	res := fetchClaudeUsage(t.Context(), "  ")
	if res.message == "" || len(res.quotas) != 0 {
		t.Fatalf("expected a message, got %+v", res)
	}
}

// A 401 must read as an expired credential so the handler refreshes it and
// reads once more (isAuthExpiredUsageMessage).
func TestFetchClaudeUsage_UnauthorizedTriggersRefreshPath(t *testing.T) {
	withClaudeUsageServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	res := fetchClaudeUsage(t.Context(), "tok-401")
	if !isAuthExpiredUsageMessage(res) {
		t.Fatalf("message %q is not recognised as auth-expired", res.message)
	}
}

func TestFetchClaudeUsage_CachesSuccessfulReads(t *testing.T) {
	calls := withClaudeUsageServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(claudeUsageBody))
	})
	for i := 0; i < 3; i++ {
		fetchClaudeUsage(t.Context(), "tok-cache")
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Errorf("upstream calls = %d, want 1", got)
	}
}

func TestFetchClaudeUsage_429CoolsDownAndServesStale(t *testing.T) {
	var limited atomic.Bool
	calls := withClaudeUsageServer(t, func(w http.ResponseWriter, r *http.Request) {
		if limited.Load() {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(claudeUsageBody))
	})
	fetchClaudeUsage(t.Context(), "tok-429")

	// Age the cached reading out, then rate-limit upstream.
	claudeUsageUpdate(claudeUsageKey("tok-429"), func(e *claudeUsageEntry) { e.expiresAt = time.Now().Add(-time.Second) })
	limited.Store(true)

	res := fetchClaudeUsage(t.Context(), "tok-429")
	if _, ok := res.quotas["session (5h)"]; !ok {
		t.Fatalf("a 429 must keep the last good reading, got %+v", res)
	}
	before := atomic.LoadInt32(calls)
	fetchClaudeUsage(t.Context(), "tok-429")
	if got := atomic.LoadInt32(calls); got != before {
		t.Errorf("token was asked again during cooldown (%d -> %d calls)", before, got)
	}
}

func TestFetchClaudeUsage_429WithoutHistoryIsBareMessage(t *testing.T) {
	withClaudeUsageServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	res := fetchClaudeUsage(t.Context(), "tok-429-cold")
	if res.message == "" || len(res.quotas) != 0 || !res.bare {
		t.Fatalf("expected a bare message, got %+v", res)
	}
}

func TestFetchProviderUsage_DispatchesClaude(t *testing.T) {
	withClaudeUsageServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(claudeUsageBody))
	})
	res, ok := fetchProviderUsage(t.Context(), "claude", map[string]any{"accessToken": "tok-dispatch"})
	if !ok {
		t.Fatal("expected fetchProviderUsage to handle claude")
	}
	if _, has := res.quotas["session (5h)"]; !has {
		t.Errorf("expected a session row, got %v", res.quotas)
	}
	out := res.toResponse()
	if q, _ := out["quotas"].(map[string]any); len(q) == 0 {
		t.Errorf("response must carry the quota rows, got %v", out)
	}
}
