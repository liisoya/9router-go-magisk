package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"9router/proxy/internal/codexquota"
)

func TestNoteCodexQuotaError_CachesUsageLimitReached(t *testing.T) {
	ClearCodexQuotaCache()
	t.Cleanup(ClearCodexQuotaCache)

	reset := time.Now().UTC().Add(90 * time.Minute).Truncate(time.Second)
	body := codexResetBody(t, reset)

	if IsCodexConnectionExhausted("conn-1") {
		t.Fatal("a fresh connection must not be exhausted")
	}

	got := NoteCodexQuotaError("conn-1", http.StatusTooManyRequests, body)
	if got == nil {
		t.Fatal("expected a reset time from a usage_limit_reached body")
	}
	if !got.Equal(reset) {
		t.Errorf("reset = %v, want %v", got, reset)
	}
	if !IsCodexConnectionExhausted("conn-1") {
		t.Error("expected the connection to be exhausted until reset")
	}
	// Codex quota is account-level, so the block must not depend on a model.
	if !IsCodexConnectionExhausted("conn-1") {
		t.Error("expected an account-level block")
	}
}

func TestNoteCodexQuotaError_IgnoresNonQuota429(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{
			name:   "generic 429 without the marker",
			status: http.StatusTooManyRequests,
			body:   `{"error":{"type":"rate_limit_error","message":"Too many requests"}}`,
		},
		{
			name:   "429 with a past reset",
			status: http.StatusTooManyRequests,
			body:   `{"error":{"type":"usage_limit_reached","resets_at":1000000000}}`,
		},
		{
			name:   "429 with no reset at all",
			status: http.StatusTooManyRequests,
			body:   `{"error":{"type":"usage_limit_reached","message":"slow down"}}`,
		},
		{
			name:   "403 is not a quota signal",
			status: http.StatusForbidden,
			body:   `{"error":{"type":"usage_limit_reached","resets_at":4102444800}}`,
		},
		{
			name:   "malformed body",
			status: http.StatusTooManyRequests,
			body:   `not json`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ClearCodexQuotaCache()
			t.Cleanup(ClearCodexQuotaCache)

			if got := NoteCodexQuotaError("conn-x", tt.status, []byte(tt.body)); got != nil {
				t.Errorf("expected no cached reset, got %v", got)
			}
			if IsCodexConnectionExhausted("conn-x") {
				t.Error("must not block the connection on a non-quota 429")
			}
		})
	}
}

func TestNoteCodexQuotaError_ResetsInSeconds(t *testing.T) {
	ClearCodexQuotaCache()
	t.Cleanup(ClearCodexQuotaCache)

	got := NoteCodexQuotaError("conn-rel", http.StatusTooManyRequests,
		[]byte(`{"error":{"type":"usage_limit_reached","resets_in_seconds":3600}}`))
	if got == nil {
		t.Fatal("expected a reset from resets_in_seconds")
	}
	delta := time.Until(*got)
	if delta < 55*time.Minute || delta > 65*time.Minute {
		t.Errorf("reset in %v, want roughly 1h", delta)
	}
}

func TestIsCodexConnectionExhausted_WeeklyWindowBlocksToo(t *testing.T) {
	ClearCodexQuotaCache()
	t.Cleanup(ClearCodexQuotaCache)

	codexQuotaMu.Lock()
	codexQuotaCache["conn-week"] = CodexConnectionQuota{
		Session:    CodexQuotaWindow{RemainingPercentage: 80, ResetAt: time.Now().UTC().Add(time.Hour)},
		HasSession: true,
		Weekly:     CodexQuotaWindow{RemainingPercentage: 0, ResetAt: time.Now().UTC().Add(3 * 24 * time.Hour)},
		HasWeekly:  true,
	}
	codexQuotaMu.Unlock()

	if !IsCodexConnectionExhausted("conn-week") {
		t.Error("an exhausted 7d window must block the account even when 5h is healthy")
	}
}

func TestIsCodexConnectionExhausted_PastResetDoesNotBlock(t *testing.T) {
	ClearCodexQuotaCache()
	t.Cleanup(ClearCodexQuotaCache)

	codexQuotaMu.Lock()
	codexQuotaCache["conn-old"] = CodexConnectionQuota{
		Session:    CodexQuotaWindow{RemainingPercentage: 0, ResetAt: time.Now().UTC().Add(-time.Minute)},
		HasSession: true,
	}
	codexQuotaCache["conn-unknown-reset"] = CodexConnectionQuota{
		Session:    CodexQuotaWindow{RemainingPercentage: 0},
		HasSession: true,
	}
	codexQuotaMu.Unlock()

	if IsCodexConnectionExhausted("conn-old") {
		t.Error("an expired reset must not block")
	}
	// Without a reset we cannot know how long to skip the account, and an
	// unbounded block would take it out of service permanently.
	if IsCodexConnectionExhausted("conn-unknown-reset") {
		t.Error("a zero remaining with no reset must not block")
	}
}

// A fresh wham reading that is optimistic must not resurrect an account a 429
// already proved exhausted — the block is re-asserted until its reset passes.
func TestRefreshCodexQuota_ReassertsBlockOnOptimisticRefresh(t *testing.T) {
	ClearCodexQuotaCache()
	t.Cleanup(ClearCodexQuotaCache)

	srv := withCodexUsageServer(t, `{"rate_limit":{"primary_window":{"used_percent":10},"secondary_window":{"used_percent":20}}}`)
	reset := time.Now().UTC().Add(time.Hour)
	BlockCodexConnectionUntil("conn-block", reset)

	if _, err := RefreshCodexQuota(t.Context(), srv.Client(), "conn-block", "tok"); err != nil {
		t.Fatalf("RefreshCodexQuota() error = %v", err)
	}
	if !IsCodexConnectionExhausted("conn-block") {
		t.Error("an optimistic refresh must not clear a live block")
	}
}

func TestRefreshCodexQuota_CachesExhaustedWindow(t *testing.T) {
	ClearCodexQuotaCache()
	t.Cleanup(ClearCodexQuotaCache)

	reset := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	srv := withCodexUsageServer(t, codexExhaustedPayload(reset))

	q, err := RefreshCodexQuota(t.Context(), srv.Client(), "conn-dead", "tok")
	if err != nil {
		t.Fatalf("RefreshCodexQuota() error = %v", err)
	}
	if !q.HasSession || !q.HasWeekly {
		t.Errorf("expected both windows cached, got %+v", q)
	}
	if q.Session.RemainingPercentage != 0 {
		t.Errorf("session remaining = %v, want 0", q.Session.RemainingPercentage)
	}
	if !IsCodexConnectionExhausted("conn-dead") {
		t.Error("expected the exhausted account to be blocked")
	}
}

func TestRefreshCodexQuota_ThrottlesRepeatCalls(t *testing.T) {
	ClearCodexQuotaCache()
	t.Cleanup(ClearCodexQuotaCache)

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":5}}}`))
	}))
	t.Cleanup(srv.Close)
	withCodexUsageURL(t, srv.URL)

	for range 5 {
		if _, err := RefreshCodexQuota(t.Context(), srv.Client(), "conn-throttle", "tok"); err != nil {
			t.Fatalf("RefreshCodexQuota() error = %v", err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("wham calls = %d, want 1 (30s throttle)", got)
	}
}

func TestRefreshCodexQuota_FailOpenPreservesLastReading(t *testing.T) {
	ClearCodexQuotaCache()
	t.Cleanup(ClearCodexQuotaCache)

	reset := time.Now().UTC().Add(2 * time.Hour)
	srv := withCodexUsageServer(t, codexExhaustedPayload(reset))
	if _, err := RefreshCodexQuota(t.Context(), srv.Client(), "conn-failopen", "tok"); err != nil {
		t.Fatalf("RefreshCodexQuota() error = %v", err)
	}
	if !IsCodexConnectionExhausted("conn-failopen") {
		t.Fatal("expected the account to be blocked after a successful read")
	}

	// Point wham at a dead endpoint and force a refresh past the throttle.
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(failing.Close)
	withCodexUsageURL(t, failing.URL)
	expireCodexRefresh(t, "conn-failopen")

	if _, err := RefreshCodexQuota(t.Context(), failing.Client(), "conn-failopen", "tok"); err == nil {
		t.Error("expected an error from a failing wham endpoint")
	}
	if !IsCodexConnectionExhausted("conn-failopen") {
		t.Error("a failed refresh must preserve the last known reading, not clear the block")
	}
}

func TestRefreshCodexQuota_CoalescesConcurrentCalls(t *testing.T) {
	ClearCodexQuotaCache()
	t.Cleanup(ClearCodexQuotaCache)

	var calls atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		<-release
		_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":5}}}`))
	}))
	t.Cleanup(srv.Close)
	withCodexUsageURL(t, srv.URL)

	const callers = 6
	done := make(chan struct{}, callers)
	for range callers {
		go func() {
			defer func() { done <- struct{}{} }()
			if _, err := RefreshCodexQuota(context.Background(), srv.Client(), "conn-coalesce", "tok"); err != nil {
				t.Errorf("RefreshCodexQuota() error = %v", err)
			}
		}()
	}

	// Let the first caller reach the handler, then release it.
	time.Sleep(100 * time.Millisecond)
	close(release)
	for range callers {
		<-done
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("wham calls = %d, want 1 (in-flight coalescing)", got)
	}
}

func TestRefreshCodexQuota_RequiresInputs(t *testing.T) {
	ClearCodexQuotaCache()
	t.Cleanup(ClearCodexQuotaCache)

	if _, err := RefreshCodexQuota(t.Context(), nil, "", "tok"); err == nil {
		t.Error("expected an error without a connection id")
	}
	if _, err := RefreshCodexQuota(t.Context(), nil, "conn", ""); err == nil {
		t.Error("expected an error without a token")
	}
}

func TestClearCodexQuotaBlock_UnblocksOnSuccess(t *testing.T) {
	ClearCodexQuotaCache()
	t.Cleanup(ClearCodexQuotaCache)

	BlockCodexConnectionUntil("conn-ok", time.Now().UTC().Add(time.Hour))
	if !IsCodexConnectionExhausted("conn-ok") {
		t.Fatal("expected a block")
	}
	ClearCodexQuotaBlock("conn-ok")
	if IsCodexConnectionExhausted("conn-ok") {
		t.Error("a successful request must clear the cached block")
	}
}

// The picker filter is what turns the cache into quota-aware fallback; without
// it a cached block would never skip the account.
func TestCodexConnectionIsSkippedByQuotaCache(t *testing.T) {
	ClearCodexQuotaCache()
	t.Cleanup(ClearCodexQuotaCache)

	if quotaCacheBlocked("codex", "conn-1", "gpt-6-sol") {
		t.Fatal("precondition: a fresh codex connection must not be blocked")
	}
	BlockCodexConnectionUntil("conn-1", time.Now().UTC().Add(time.Hour))
	if !quotaCacheBlocked("codex", "conn-1", "gpt-6-sol") {
		t.Error("expected the codex pre-filter to skip an exhausted account")
	}
	// Provider isolation: an antigravity filter must not fire for codex.
	if quotaCacheBlocked("antigravity", "conn-1", "gpt-6-sol") {
		t.Error("the antigravity pre-filter must not apply to codex")
	}
}

func codexResetBody(t *testing.T, reset time.Time) []byte {
	t.Helper()
	return []byte(`{"error":{"type":"usage_limit_reached","resets_at":` +
		strconv.FormatInt(reset.Unix(), 10) + `}}`)
}

func codexExhaustedPayload(reset time.Time) string {
	return `{"rate_limit":{"primary_window":{"used_percent":100,"reset_at":` +
		strconv.FormatInt(reset.Unix(), 10) + `},"secondary_window":{"used_percent":100,"reset_at":` +
		strconv.FormatInt(reset.Unix(), 10) + `}}}`
}

func withCodexUsageServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	withCodexUsageURL(t, srv.URL)
	return srv
}

func withCodexUsageURL(t *testing.T, url string) {
	t.Helper()
	prev := codexquota.UsageURL
	codexquota.UsageURL = url
	t.Cleanup(func() { codexquota.UsageURL = prev })
}

// expireCodexRefresh ages the throttle stamp so the next call actually refetches.
func expireCodexRefresh(t *testing.T, connectionID string) {
	t.Helper()
	codexQuotaMu.Lock()
	defer codexQuotaMu.Unlock()
	codexLastRefreshAt[connectionID] = time.Now().UTC().Add(-2 * MinCodexQuotaRefreshInterval)
}
