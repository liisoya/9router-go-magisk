package fetchgate

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"
)

// An idle gate must not delay the single caller: the dashboard's per-provider
// refresh button pays nothing, only the burst behind it is paced.
func TestGateAcquire_IdleGateReturnsImmediately(t *testing.T) {
	g := New(250*time.Millisecond, 120*time.Millisecond)

	start := time.Now()
	if err := g.Acquire(t.Context()); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("idle gate delayed the first caller by %s", elapsed)
	}
}

// The whole point of the gate: N simultaneous callers must not start in the
// same millisecond. This is the ten-accounts-on-one-IP case from issue #30.
func TestGateAcquire_SpacesConcurrentCallers(t *testing.T) {
	const (
		callers = 8
		minGap  = 40 * time.Millisecond
	)
	g := New(minGap, 0)

	var (
		mu    sync.Mutex
		slots []time.Duration
		wg    sync.WaitGroup
	)
	start := time.Now()
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := g.Acquire(t.Context()); err != nil {
				t.Errorf("Acquire: %v", err)
				return
			}
			mu.Lock()
			slots = append(slots, time.Since(start))
			mu.Unlock()
		}()
	}
	wg.Wait()

	if len(slots) != callers {
		t.Fatalf("got %d slots, want %d", len(slots), callers)
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i] < slots[j] })
	for i := 1; i < len(slots); i++ {
		// Sorted by start time, so consecutive entries are consecutive grants.
		if gap := slots[i] - slots[i-1]; gap < minGap*9/10 {
			t.Errorf("caller %d started %s after the previous one, want >= %s", i, gap, minGap)
		}
	}
}

// Jitter must only ever add delay: the floor stays the hard guarantee.
func TestGateAcquire_JitterOnlyWidensTheGap(t *testing.T) {
	g := New(30*time.Millisecond, 30*time.Millisecond)

	if err := g.Acquire(t.Context()); err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	for i := range 5 {
		start := time.Now()
		if err := g.Acquire(t.Context()); err != nil {
			t.Fatalf("Acquire %d: %v", i, err)
		}
		if elapsed := time.Since(start); elapsed < 30*time.Millisecond {
			t.Fatalf("slot %d waited %s, want at least the 30ms floor", i, elapsed)
		}
	}
}

// A dashboard that navigates away mid-wait must not hold a slot against the
// requests behind it, and it must see its own cancellation as an error.
func TestGateAcquire_CanceledContextReleasesItsWait(t *testing.T) {
	g := New(200*time.Millisecond, 0)

	if err := g.Acquire(t.Context()); err != nil {
		t.Fatalf("first Acquire: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := g.Acquire(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire error = %v, want context.Canceled", err)
	}
}

// A zero gap is a legitimate configuration (pure jitter, no floor), and must
// not be mistaken for a misconfigured gate.
func TestGateAcquire_ZeroGapStillGates(t *testing.T) {
	g := New(0, 0)

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := g.Acquire(t.Context()); err != nil {
				t.Errorf("Acquire: %v", err)
			}
		}()
	}
	wg.Wait()
}
