package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"9router/proxy/internal/models"
)

// TestEvictionPrunesEveryMap proves the bound covers the maps this change added,
// not just the RPM windows. Without it the new wake-channel map grows one entry
// per key ever seen, which for a per-caller key fleet is an unbounded leak.
func TestEvictionPrunesEveryMap(t *testing.T) {
	rl := NewRateLimiter(time.Minute)

	// Drive far past the cap through the RPM path so eviction runs, and touch
	// the TPM, concurrency and queue maps too so every map has entries to prune.
	const n = 12000 // > maxKeys (10000)
	for i := 0; i < n; i++ {
		key := keyName(i)
		rl.Check(key, 10, 1000, 0, 1)
		rl.tokens[key] = &tokenBucket{tokens: 1, lastRefill: time.Now()}
		// Enqueue a waiter under the real path for the first slice of keys only:
		// claim the single slot, then start a second acquire that cannot be
		// admitted and parks, timing out almost immediately. This populates the
		// queue map so eviction has something to prune, without the 1ms wait per
		// key across all 12000.
		if i < 200 {
			if _, ok := rl.Acquire(context.Background(), key, 1, 1, 0); ok {
				rl.Acquire(context.Background(), key, 1, 1, time.Millisecond)
				rl.ReleaseConcurrency(key)
			}
		}
	}

	if got := len(rl.windows); got > rl.maxKeys {
		t.Errorf("windows map = %d entries, want <= %d", got, rl.maxKeys)
	}
	// tokens/queues are pruned alongside the window; assert they are bounded by
	// the same cap.
	if got := len(rl.tokens); got > rl.maxKeys {
		t.Errorf("tokens map = %d entries, want <= %d", got, rl.maxKeys)
	}
	if got := len(rl.queues); got > rl.maxKeys {
		t.Errorf("queues map = %d entries, want <= %d", got, rl.maxKeys)
	}
}

// TestEvictionNeverDropsBusyKey proves eviction leaves an in-flight key alone:
// dropping its concurrency counter would let a slot leak (the running request
// releases into a fresh counter) and dropping its wake channel would strand a
// parked waiter.
func TestEvictionNeverDropsBusyKey(t *testing.T) {
	rl := NewRateLimiter(time.Minute)

	// Hold a slot on a specific key that we will then try to evict.
	const busyKey = "busy-key"
	if _, ok := rl.Acquire(context.Background(), busyKey, 0, 1, 0); !ok {
		t.Fatal("busy key failed to acquire its slot")
	}

	// Force eviction pressure from other keys.
	for i := 0; i < rl.maxKeys+100; i++ {
		rl.Check(keyName(i), 10, 0, 0, 0)
	}

	// The busy key must still be tracked, its slot still held.
	rl.mu.Lock()
	busyCounter := rl.active[busyKey]
	rl.mu.Unlock()
	if busyCounter == nil {
		t.Fatal("eviction dropped the active counter of a busy key")
	}
	if got := atomic.LoadInt32(busyCounter); got != 1 {
		t.Errorf("busy key in-flight count = %d, want 1", got)
	}

	// Releasing must return it to zero without losing the slot.
	rl.ReleaseConcurrency(busyKey)
	if got := atomic.LoadInt32(busyCounter); got != 0 {
		t.Errorf("after release in-flight count = %d, want 0", got)
	}
}

// TestQueuesAreIsolatedBetweenKeys proves a key at its concurrency cap does not
// slow down or block a different key: the counters and wake channels are keyed,
// and one key's queue must not leak into another's.
func TestQueuesAreIsolatedBetweenKeys(t *testing.T) {
	rl := NewRateLimiter(time.Minute)

	// Saturate key A.
	if _, ok := rl.Acquire(context.Background(), "A", 0, 1, 0); !ok {
		t.Fatal("A failed to acquire")
	}

	// Key B, with the same cap, must acquire immediately.
	start := time.Now()
	_, ok := rl.Acquire(context.Background(), "B", 0, 1, 0)
	if !ok {
		t.Fatal("B was blocked by A's saturation — queues are not isolated")
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("B took %v to acquire; A's saturation leaked into B", elapsed)
	}
}

// TestMiddlewareIsolationBetweenKeys runs two concurrent requests for two keys
// through the middleware: one key's handler blocks, and the other must still
// complete immediately.
func TestMiddlewareIsolationBetweenKeys(t *testing.T) {
	rl := NewRateLimiter(time.Minute)

	blockA := make(chan struct{})
	enteredA := make(chan struct{}, 1)
	var order []string
	var mu sync.Mutex

	handler := RequireRateLimit(rl, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-Test-Key")
		if key == "A" {
			select {
			case enteredA <- struct{}{}:
			default:
			}
			<-blockA
		}
		mu.Lock()
		order = append(order, key)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))

	mkReq := func(key string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		req.Header.Set("X-Test-Key", key)
		return req.WithContext(withAPIKey(req, &models.APIKey{
			ID:                   key,
			RateLimitConcurrency: new(1),
			QueueTimeoutMs:       new(2000),
		}))
	}

	doneA := make(chan struct{})
	go func() {
		defer close(doneA)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, mkReq("A"))
	}()
	<-enteredA // A now holds its only slot

	// B must sail through while A is blocked.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, mkReq("B"))
	if rec.Code != http.StatusOK {
		t.Fatalf("B status = %d, want 200 while A is blocked", rec.Code)
	}

	mu.Lock()
	first := ""
	if len(order) > 0 {
		first = order[0]
	}
	mu.Unlock()
	if first != "B" {
		t.Errorf("first completed key = %q, want B (A must not block B)", first)
	}

	close(blockA)
	<-doneA
}

// keyName returns a distinct key id for index i.
func keyName(i int) string {
	return "key-" + strconv.Itoa(i)
}
