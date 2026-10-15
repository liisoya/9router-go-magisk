package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"9router/proxy/internal/models"
)

// TestRequireRateLimitExposesReservationToHandler proves the TPM charge the
// limiter took travels on the handler's request context, which is how the chat
// handler learns what to reconcile. Breaking this would leave the bucket
// permanently over- or under-charged.
func TestRequireRateLimitExposesReservationToHandler(t *testing.T) {
	rl := NewRateLimiter(time.Minute)

	var reserved int
	var hadReservation bool
	handler := RequireRateLimit(rl, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reserved, hadReservation = ReservedTokensFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"` + strings.Repeat("word ", 200) + `"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req = req.WithContext(withAPIKey(req, &models.APIKey{
		ID:           "resv",
		RateLimitTPM: new(100000),
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !hadReservation {
		t.Fatal("handler saw no TPM reservation on the request context")
	}
	if reserved <= 0 {
		t.Errorf("reservation = %d, want a positive prompt estimate", reserved)
	}
}

// TestSettleReconcilesUnderEstimate proves the settle hook corrects an
// under-estimate (the turn cost more than the prompt sizing charged) but never
// refunds an over-estimate — refunding would let a client bank budget by
// over-stating its prompt. This is the contract the chat handler relies on.
func TestSettleReconcilesUnderEstimate(t *testing.T) {
	rl := NewRateLimiter(time.Minute)

	// Charge 100 prompt tokens against a 1000 TPM bucket.
	if ok, _, _ := rl.Check("settle", 0, 1000, 0, 100); !ok {
		t.Fatal("initial charge refused")
	}

	rl.mu.Lock()
	afterCharge := rl.tokens["settle"].tokens
	rl.mu.Unlock()

	// The turn actually cost 300: settle must charge the extra 200.
	rl.SettleTokenUsage("settle", 1000, 100, 300)
	rl.mu.Lock()
	afterUnder := rl.tokens["settle"].tokens
	rl.mu.Unlock()
	if afterUnder >= afterCharge {
		t.Errorf("under-estimate settle did not charge extra: %v -> %v", afterCharge, afterUnder)
	}

	// A later over-estimate (actual < reserved) must NOT refund.
	rl.SettleTokenUsage("settle", 1000, 500, 100)
	rl.mu.Lock()
	afterOver := rl.tokens["settle"].tokens
	rl.mu.Unlock()
	if afterOver != afterUnder {
		t.Errorf("over-estimate settle changed the bucket: %v -> %v (must not refund)", afterUnder, afterOver)
	}
}

// TestSettleThroughContext proves the context-carried settler is invoked with
// the real count and reaches the same bucket, which is the whole point of the
// reservation → settle hand-off.
func TestSettleThroughContext(t *testing.T) {
	rl := NewRateLimiter(time.Minute)
	key := &models.APIKey{ID: "ctx-settle", RateLimitTPM: new(1000)}

	// Seed the bucket with a charge so SettleTokenUsage has one to adjust.
	if ok, _, _ := rl.Check(key.ID, 0, 1000, 0, 100); !ok {
		t.Fatal("initial charge refused")
	}

	ctx := WithTokenSettler(t.Context(), func(actual int) {
		rl.SettleTokenUsage(key.ID, 1000, 100, actual)
	})

	rl.mu.Lock()
	before := rl.tokens[key.ID].tokens
	rl.mu.Unlock()

	SettleTokenUsageFromContext(ctx, 400) // under-estimate by 300

	rl.mu.Lock()
	after := rl.tokens[key.ID].tokens
	rl.mu.Unlock()
	if after >= before {
		t.Errorf("context settle did not charge the under-estimate: %v -> %v", before, after)
	}
}

// TestSettleNoopWithoutReservation proves a request that was never charged has
// nothing to settle: the helper must be a no-op, not a panic or a charge into a
// bucket that does not exist.
func TestSettleNoopWithoutReservation(t *testing.T) {
	rl := NewRateLimiter(time.Minute)
	// No Check, so no bucket exists for this key.
	rl.SettleTokenUsage("never-charged", 1000, 10, 500)
	if _, exists := rl.tokens["never-charged"]; exists {
		t.Error("settle created a bucket for a key that was never charged")
	}

	// A context with no settler installed must also be safe.
	SettleTokenUsageFromContext(t.Context(), 500)
}
