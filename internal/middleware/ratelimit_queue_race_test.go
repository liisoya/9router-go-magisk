package middleware

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestQueueNoLostWakeupHammer is the adversarial counterpart to the stress
// test: it repeatedly parks a waiter and releases the slot at a random point in
// the acquire/release interleaving, then asserts every waiter eventually wins.
// A lost wakeup would show up as a waiter stuck until its full timeout.
func TestQueueNoLostWakeupHammer(t *testing.T) {
	const rounds = 400
	for r := 0; r < rounds; r++ {
		rl := NewRateLimiter(time.Minute)
		const key = "hammer"

		if _, ok := rl.Acquire(context.Background(), key, 0, 1, 0); !ok {
			t.Fatalf("round %d: initial acquire failed", r)
		}

		got := make(chan bool, 1)
		go func() {
			// A generous timeout: if the wake is lost the waiter still succeeds
			// at the deadline, but far slower than the release that should have
			// freed it. We measure the wait to catch that.
			start := time.Now()
			_, ok := rl.Acquire(context.Background(), key, 0, 1, 2*time.Second)
			elapsed := time.Since(start)
			got <- ok && elapsed < 500*time.Millisecond
		}()

		// Release almost immediately; the waiter may or may not be parked yet,
		// which is exactly the race we want to cover.
		rl.ReleaseConcurrency(key)

		select {
		case ok := <-got:
			if !ok {
				t.Fatalf("round %d: waiter lost its wakeup (acquired=false or waited too long)", r)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("round %d: waiter never returned", r)
		}
	}
}

// TestQueueWaitDoesNotBlockOtherCallers proves a parked waiter does not hold the
// limiter's lock: while key A's waiter sleeps, a brand new key B must be able
// to acquire without waiting for A.
func TestQueueWaitDoesNotBlockOtherCallers(t *testing.T) {
	rl := NewRateLimiter(time.Minute)

	// Saturate A so its next acquire parks.
	if _, ok := rl.Acquire(context.Background(), "A", 0, 1, 0); !ok {
		t.Fatal("A failed to acquire")
	}

	parked := make(chan struct{})
	go func() {
		close(parked)
		// This blocks until its 50ms timeout, holding no lock.
		rl.Acquire(context.Background(), "A", 0, 1, 50*time.Millisecond)
	}()
	<-parked
	// Give the goroutine a moment to actually enter the wait.
	time.Sleep(10 * time.Millisecond)

	// B must acquire instantly, unaffected by A's parked waiter.
	start := time.Now()
	if _, ok := rl.Acquire(context.Background(), "B", 0, 1, 0); !ok {
		t.Fatal("B blocked behind A's parked waiter — the lock is held during the wait")
	}
	if elapsed := time.Since(start); elapsed > 25*time.Millisecond {
		t.Errorf("B took %v; A's parked waiter held the lock", elapsed)
	}
}

// TestQueueSoakNoSlotLeak runs many acquire/release cycles across a pool of keys
// and asserts every slot is returned. A leaked slot (a release that did not
// decrement) would leave the counter above zero at the end.
func TestQueueSoakNoSlotLeak(t *testing.T) {
	rl := NewRateLimiter(time.Minute)
	const (
		keys    = 20
		cap     = 3
		workers = 40
		cycles  = 50
	)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for c := 0; c < cycles; c++ {
				key := "soak-" + itoaSmall((seed+c)%keys)
				if _, ok := rl.Acquire(context.Background(), key, 0, cap, 5*time.Second); !ok {
					t.Errorf("acquire failed for %s", key)
					return
				}
				rl.ReleaseConcurrency(key)
			}
		}(w)
	}
	wg.Wait()

	rl.mu.Lock()
	defer rl.mu.Unlock()
	for key, counter := range rl.active {
		if v := atomic.LoadInt32(counter); v != 0 {
			t.Errorf("key %s leaked %d slot(s) after soak", key, v)
		}
	}
}

func itoaSmall(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}
