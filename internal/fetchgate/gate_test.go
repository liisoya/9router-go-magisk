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
//
// The assertion is on the span of the whole burst, not on each gap
// individually. A recorded start is "the instant the gate opened my slot" plus
// "however long my goroutine then waited to be scheduled onto a busy CPU", and
// that second term is unbounded — differencing two consecutive starts compares
// two different goroutines' scheduling luck. One early goroutine next to one
// late goroutine reads as two slots at once even though the gate did nothing
// wrong, which is why this failed at 24-33ms against a 40ms floor under
// `-p 16`, always on the same middle indices where the two kinds of luck meet.
//
// Summing the gaps cancels that noise instead: scheduler lag is zero-sum
// around the loop. Eight callers on a 40ms floor span at least 280ms, while a
// gate that granted them all at once spans the same few microseconds however
// busy the CPU is. The span is therefore invariant under scheduling jitter and
// still collapses the moment pacing is removed.
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

	// callers-1 gaps of at least minGap: anything shorter means the gate
	// let at least one pair through without waiting, which is the burst this
	// gate exists to prevent.
	span := slots[len(slots)-1] - slots[0]
	wantSpan := minGap * time.Duration(callers-1)
	if span < wantSpan {
		t.Errorf("%d callers spanned %s end to end, want at least %s: they started %s too close together",
			callers, span, wantSpan, span/time.Duration(callers-1))
	}
}

// Jitter must only ever add delay: the floor stays the hard guarantee.
//
// Checked against the instants the gate promises, not against wall-clock
// wake-ups. The floor is a property of reservation: reserve hands out `start`
// and only then sleeps until it, so consecutive promised starts are minGap
// apart no matter how late any goroutine runs. A stopwatch around Acquire
// cannot see that — it starts counting *after* the previous grant returned, so
// the previous goroutine's wake-up lateness is charged to the next gap. Under
// -race with the rest of the suite loaded that read 39.43ms against a 40ms
// floor (and 29.77ms against a 30ms one) with the gate behaving correctly
// throughout, at indices that follow no pattern. Comparing promised starts
// removes that term instead of buying margin against it.
func TestGateAcquire_JitterOnlyWidensTheGap(t *testing.T) {
	const (
		minGap = 40 * time.Millisecond
		jitter = 60 * time.Millisecond
		slots  = 8
	)
	g := New(minGap, jitter)

	var prev time.Time
	widened := 0
	for i := range slots {
		start, now := g.reserve()
		if i == 0 {
			if wait := start.Sub(now); wait > 0 {
				t.Fatalf("first slot waited %s on an idle gate, want it immediate", wait)
			}
		}
		if gap := start.Sub(prev); i > 0 {
			if gap < minGap {
				t.Fatalf("slot %d was promised %s after the previous one, want at least the %s floor", i, gap, minGap)
			}
			if gap > minGap {
				widened++
			}
		}
		prev = start
	}

	// Jitter is drawn per slot from [0, maxJitter]; eight zero draws in a row
	// have probability (1/61)^8, so an all-floor run is not a real outcome.
	if widened == 0 {
		t.Errorf("jitter never widened any of the %d gaps, so it is not in play at all", slots-1)
	}
}

// The floor on its own, with jitter switched off: every gap must then be
// exactly minGap. Deterministic, and it is what catches a removed floor — the
// jittered test above cannot, since a draw below minGap is legal there.
func TestGateAcquire_FloorHoldsWithoutJitter(t *testing.T) {
	const (
		minGap = 40 * time.Millisecond
		slots  = 6
	)
	g := New(minGap, 0)

	var prev time.Time
	for i := range slots {
		start, _ := g.reserve()
		if i > 0 {
			if gap := start.Sub(prev); gap != minGap {
				t.Fatalf("slot %d was promised %s after the previous one, want exactly the %s floor", i, gap, minGap)
			}
		}
		prev = start
	}
}

// Acquire must actually wait out the slot it reserved. The stopwatch here
// starts before the reservation, so the only thing it can lose is timer
// earliness — Go timers fire late, never early — which makes this direction of
// the measurement safe to assert exactly.
func TestGateAcquire_WaitsOutTheReservedSlot(t *testing.T) {
	const minGap = 40 * time.Millisecond
	g := New(minGap, 0)

	if err := g.Acquire(t.Context()); err != nil {
		t.Fatalf("first Acquire: %v", err)
	}

	before := time.Now()
	if err := g.Acquire(t.Context()); err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	if elapsed := time.Since(before); elapsed < minGap {
		t.Fatalf("reserved slot returned after %s, want at least the %s floor", elapsed, minGap)
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
