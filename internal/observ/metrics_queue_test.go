package observ

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRateLimitQueueWaitIsObserved proves the concurrency-queue histogram the
// rate limiter records into is actually registered and observable, so an
// operator can see queue pressure rather than only its rejections.
func TestRateLimitQueueWaitIsObserved(t *testing.T) {
	m := New()

	m.ObserveRateLimitQueueWait(0.02)
	m.ObserveRateLimitQueueWait(0.5)
	m.ObserveRateLimitQueueWait(3.0)

	if got := familyValue(t, m, "router_rate_limit_queue_seconds"); got != 3 {
		t.Errorf("queue histogram sample count = %v, want 3", got)
	}
	if labels := familyLabels(t, m, "router_rate_limit_queue_seconds"); labels["scope"] != "api_key" {
		t.Errorf("queue histogram scope = %q, want api_key", labels["scope"])
	}
}

// TestRateLimitQueueWaitIsolatedPerRegistry keeps the existing isolation
// contract: a second Metrics instance must not observe the first's waits.
func TestRateLimitQueueWaitIsolatedPerRegistry(t *testing.T) {
	a, b := New(), New()
	a.ObserveRateLimitQueueWait(1.0)

	if got := familyValue(t, b, "router_rate_limit_queue_seconds"); got != 0 {
		t.Errorf("second registry saw %v queue samples, want 0", got)
	}
	if got := familyValue(t, a, "router_rate_limit_queue_seconds"); got != 1 {
		t.Errorf("first registry saw %v queue samples, want 1", got)
	}
}

// TestQueueHistogramHasNoKeyLabel proves the histogram's labels cannot carry a
// credential: the only label is a bounded scope, so the wait metric can never
// become a cardinality or secret leak even if a key id were passed by mistake.
func TestQueueHistogramHasNoKeyLabel(t *testing.T) {
	m := New()
	m.ObserveRateLimitQueueWait(0.25)

	rec := httptest.NewRecorder()
	HandlerFor(m).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	text := string(body)
	if !strings.Contains(text, "router_rate_limit_queue_seconds") {
		t.Fatal("the queue histogram is not present in the exposition output")
	}
	// The histogram must appear with only the scope label, never a key.
	if strings.Contains(text, `router_rate_limit_queue_seconds_bucket{key=`) {
		t.Error("queue histogram carries a key label")
	}
}
