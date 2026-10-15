package middleware

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestQueueFIFOUnderContention proves arrival order is preserved even when the
// slot is freed concurrently while waiters are still enqueuing.
//
// Arrival order is made deterministic by observing the queue itself: waiter i is
// started only after waiter i-1 is confirmed parked (the queue slice grew). The
// queue is then inspected to bind each slot position to a waiter id, so the
// assertion is exactly "grants follow queue position" — the definition of FIFO —
// rather than an assumption about goroutine start order.
func TestQueueFIFOUnderContention(t *testing.T) {
	const (
		key     = "fifo-contended"
		waiters = 12
	)
	for round := 0; round < 30; round++ {
		rl := NewRateLimiter(time.Minute)
		if _, ok := rl.Acquire(context.Background(), key, 0, 1, 0); !ok {
			t.Fatal("failed to take the only slot")
		}

		var mu sync.Mutex
		granted := make([]int, 0, waiters)
		var wg sync.WaitGroup

		// Enqueue waiters one at a time, confirming each is parked before the
		// next starts. This fixes the queue order to 0,1,2,...
		for i := 0; i < waiters; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				if _, ok := rl.Acquire(context.Background(), key, 0, 1, 5*time.Second); !ok {
					t.Errorf("round %d: waiter %d timed out", round, id)
					return
				}
				mu.Lock()
				granted = append(granted, id)
				mu.Unlock()
				rl.ReleaseConcurrency(key)
			}(i)

			// Wait until this waiter is actually in the queue before starting the
			// next, so the queue order is the loop order. The slot is held, so
			// the acquire cannot succeed early and empty the queue.
			waitForQueueLen(t, rl, key, i+1)
		}

		// Free the slot only now that all waiters are parked and ordered.
		rl.ReleaseConcurrency(key)
		wg.Wait()

		if len(granted) != waiters {
			t.Fatalf("round %d: granted %d, want %d", round, len(granted), waiters)
		}
		for i, id := range granted {
			if id != i {
				t.Fatalf("round %d: grant #%d = waiter %d, want %d (granted %v)", round, i, id, i, granted)
			}
		}
	}
}

// waitForQueueLen blocks until the key's queue holds at least n waiters, or the
// deadline passes. It makes the FIFO test's enqueue order deterministic without
// relying on sleeps.
func waitForQueueLen(t *testing.T, rl *RateLimiter, key string, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rl.mu.Lock()
		q := rl.queues[key]
		l := 0
		if q != nil {
			l = len(q.waiters)
		}
		rl.mu.Unlock()
		if l >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("queue for %s never reached %d waiters", key, n)
}

// TestQueueMixedAxisAdmitsCorrectly proves a key capped on BOTH rpm and
// concurrency only admits when both axes allow it, and that a waiter queued
// under both is granted exactly when the last constraint clears.
func TestQueueMixedAxisAdmitsCorrectly(t *testing.T) {
	rl := NewRateLimiter(150 * time.Millisecond) // short RPM window
	const key = "mixed"

	// Take the one RPM stamp and the one concurrency slot.
	if _, ok := rl.Acquire(context.Background(), key, 1, 1, 0); !ok {
		t.Fatal("first request refused")
	}

	// A second request is blocked on BOTH axes. It must be admitted only after
	// both clear: the concurrency slot is released now, but the RPM window is
	// still full, so it must wait for the window, then succeed.
	done := make(chan bool, 1)
	go func() {
		_, ok := rl.Acquire(context.Background(), key, 1, 1, 2*time.Second)
		done <- ok
	}()

	// Free concurrency immediately; RPM remains the binding constraint.
	rl.ReleaseConcurrency(key)

	start := time.Now()
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("second request was refused instead of waiting for the RPM window")
		}
		if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
			t.Errorf("second request admitted in %v; RPM window should still have blocked it", elapsed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("second request never resolved")
	}
}

// TestQueueDrainsWithMultipleSlots proves the pump hands out every freed slot,
// not just one: with a cap of 3 and 10 waiters, all 10 must eventually run.
func TestQueueDrainsWithMultipleSlots(t *testing.T) {
	rl := NewRateLimiter(time.Minute)
	const (
		key     = "drain"
		slots   = 3
		waiters = 10
	)

	// Fill all slots.
	for i := 0; i < slots; i++ {
		if _, ok := rl.Acquire(context.Background(), key, 0, slots, 0); !ok {
			t.Fatalf("slot %d not taken", i)
		}
	}

	var wg sync.WaitGroup
	admitted := make([]bool, waiters)
	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if _, ok := rl.Acquire(context.Background(), key, 0, slots, 5*time.Second); ok {
				admitted[idx] = true
				time.Sleep(5 * time.Millisecond)
				rl.ReleaseConcurrency(key)
			}
		}(i)
	}

	// Free the initial slots; the pump must keep granting as waiters release.
	for i := 0; i < slots; i++ {
		rl.ReleaseConcurrency(key)
	}
	wg.Wait()

	for i, ok := range admitted {
		if !ok {
			t.Errorf("waiter %d never admitted; the queue did not drain", i)
		}
	}
}

// TestQueueWindowTimersAreCleanedUp proves a window wake timer is removed once
// it fires, so the timer map cannot grow without bound across many windows.
func TestQueueWindowTimersAreCleanedUp(t *testing.T) {
	rl := NewRateLimiter(60 * time.Millisecond)
	const key = "timers"

	// Fill the window and queue a waiter that will be admitted on rollover.
	if _, ok := rl.Acquire(context.Background(), key, 1, 0, 0); !ok {
		t.Fatal("first request refused")
	}
	if p, ok := rl.Acquire(context.Background(), key, 1, 0, time.Second); !ok {
		t.Fatalf("queued request refused (p=%v)", p)
	}

	// Give the timer a moment to fire and delete itself.
	time.Sleep(80 * time.Millisecond)

	rl.mu.Lock()
	timers := len(rl.windowTimers)
	queues := len(rl.queues)
	rl.mu.Unlock()
	if timers != 0 {
		t.Errorf("windowTimers holds %d entries after the window rolled, want 0", timers)
	}
	if queues != 0 {
		t.Errorf("queues holds %d entries after draining, want 0", queues)
	}
}

// TestEvictionNeverStrandsParkedWaiter proves eviction skips a key that has a
// waiter parked (no concurrency slot held), because dropping its queue or timer
// would strand that caller until its timeout.
func TestEvictionNeverStrandsParkedWaiter(t *testing.T) {
	rl := NewRateLimiter(time.Minute)

	// Queue a waiter on a key that occupies NO concurrency slot: RPM-only, with
	// the window full. This is the case keyBusy does NOT cover.
	const parked = "parked"
	if _, ok := rl.Acquire(context.Background(), parked, 1, 0, 0); !ok { // fills the 1-request window
		t.Fatal("seed request refused")
	}
	done := make(chan bool, 1)
	go func() {
		// Parks for longer than the eviction below takes, so a dropped queue
		// entry would show up as this waiter being stranded (it would still
		// resolve at its own timeout, but the queue map check below is what
		// proves it was not evicted).
		_, ok := rl.Acquire(context.Background(), parked, 1, 0, 700*time.Millisecond)
		done <- ok
	}()
	// Let the waiter park.
	time.Sleep(20 * time.Millisecond)

	rl.mu.Lock()
	if _, ok := rl.queues[parked]; !ok {
		rl.mu.Unlock()
		t.Fatal("waiter did not park on the key")
	}
	rl.mu.Unlock()

	// Force eviction pressure from many idle keys — must not touch `parked`.
	for i := 0; i < rl.maxKeys+500; i++ {
		rl.Check(keyName(i), 10, 0, 0, 0)
	}

	rl.mu.Lock()
	_, stillQueued := rl.queues[parked]
	rl.mu.Unlock()
	if !stillQueued {
		t.Error("eviction dropped the queue of a key with a parked waiter, stranding it")
	}

	// The parked waiter will resolve at its own timeout; drain it so the
	// goroutine does not outlive the test.
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Error("parked waiter never resolved")
	}
}
