// Package fetchgate paces outbound HTTP requests that a burst of callers would
// otherwise fire in the same millisecond.
//
// The dashboard quota tracker refreshes every visible connection at once
// (`Promise.allSettled(connections.map(fetchQuota))`). A user running ten
// accounts from one office IP therefore sent ten quota reads inside a few
// milliseconds, and the provider answered 429 — which the chat path then reads
// as real quota exhaustion, locking accounts that still had live tokens.
//
// A gate is the general fix: callers acquire a slot before their request goes
// out, and the gate hands out at most one slot per minimum gap. Several
// accounts behind one NAT stop looking like an automated fleet, at the cost of
// the last read finishing a couple of seconds later.
package fetchgate

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"
)

// Gate serializes callers and enforces a minimum spacing between the slots it
// hands out. The zero value is unusable; call New.
type Gate struct {
	mu sync.Mutex
	// next is the earliest instant the next slot may start. Callers that find
	// it in the past take the slot immediately.
	next      time.Time
	minGap    time.Duration
	maxJitter time.Duration
}

// New returns a gate that spaces consecutive slots by at least minGap, plus a
// random [0, maxJitter] per slot. Non-positive values disable that half of the
// pacing: minGap 0 with a jitter still keeps bursts from being perfectly
// simultaneous.
//
// Jitter is additive rather than a window around minGap so minGap stays a hard
// floor that tests and operators can rely on.
func New(minGap, maxJitter time.Duration) *Gate {
	return &Gate{minGap: max(minGap, 0), maxJitter: max(maxJitter, 0)}
}

// Acquire reserves the next slot, blocking until it opens. It returns early
// with the context error when ctx is done first, so a disconnected dashboard
// does not keep a slot reserved ahead of the requests that are still waiting.
//
// Slots are handed out in the order Acquire is called, and the reservation is
// taken under the lock before the wait: a caller that gives up still consumed
// its slot, which is what keeps a stampede from immediately re-collapsing into
// the slot the next caller takes.
func (g *Gate) Acquire(ctx context.Context) error {
	start, now := g.reserve()
	if wait := start.Sub(now); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return fmt.Errorf("fetchgate.Acquire: %w", ctx.Err())
		}
	}
	return nil
}

// reserve hands out the next slot and returns the instant it opens along with
// the instant the reservation was made.
func (g *Gate) reserve() (start, now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()

	now = time.Now()
	start = now
	if g.next.After(start) {
		start = g.next
	}
	g.next = start.Add(g.minGap + g.jitter())
	return start, now
}

// jitter returns the extra delay for one slot, drawn from the package-level
// source so concurrent callers never need their own *rand.Rand.
func (g *Gate) jitter() time.Duration {
	if g.maxJitter <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(g.maxJitter) + 1))
}
