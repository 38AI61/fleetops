package eta

import (
	"math/rand/v2"
	"testing"
	"time"
)

// TestLimiterFakeClockTicks drives several jobs through 30 s ticks and checks the 5-min window.
func TestLimiterFakeClockTicks(t *testing.T) {
	l := NewLimiter(AttemptWindow)
	start := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	jobs := []int64{1, 2, 3}

	attempts := map[int64][]time.Time{}
	for tick := 0; tick <= 22; tick++ { // 0 .. 660 s
		now := start.Add(time.Duration(tick) * Interval)
		for _, id := range jobs {
			if l.Allow(id, now) {
				attempts[id] = append(attempts[id], now)
			}
		}
	}

	for _, id := range jobs {
		got := attempts[id]
		// Attempts at 0 s, 300 s and 600 s, one per window.
		if len(got) != 3 {
			t.Fatalf("job %d: %d attempts in 660 s, want 3 (%v)", id, len(got), got)
		}
		for i, at := range got {
			want := start.Add(time.Duration(i) * AttemptWindow)
			if !at.Equal(want) {
				t.Errorf("job %d attempt %d at %s, want %s", id, i, at, want)
			}
		}
	}
}

// TestLimiterBoundaryIsInclusive checks that exactly one window after an attempt is allowed.
func TestLimiterBoundaryIsInclusive(t *testing.T) {
	l := NewLimiter(AttemptWindow)
	at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	if !l.Allow(7, at) {
		t.Fatal("first attempt denied")
	}
	if l.Allow(7, at.Add(AttemptWindow-time.Nanosecond)) {
		t.Error("attempt one nanosecond before the window was allowed")
	}
	if !l.Allow(7, at.Add(AttemptWindow)) {
		t.Error("attempt exactly one window later was denied")
	}
}

// TestLimiterPrunesInactiveJobs checks that a job that left the active set loses its record.
func TestLimiterPrunesInactiveJobs(t *testing.T) {
	l := NewLimiter(AttemptWindow)
	at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	l.Allow(1, at)
	l.Allow(2, at)

	l.Prune(map[int64]struct{}{2: {}})
	if _, ok := l.last[1]; ok {
		t.Error("job 1 is no longer active but still tracked")
	}
	if _, ok := l.last[2]; !ok {
		t.Error("job 2 is active but was pruned")
	}
}

// TestLimiterPropertyAtMostOneCallPerWindow is the bounded property test (D-37, GF-307).
// Fixed seed, 1000 iterations. Jobs may leave the active set and never come back
// (jobs cannot re-enter an active state). Every pass prunes to the active set.
// Each job's attempts must be at least one window apart, and the count must stay within the span.
func TestLimiterPropertyAtMostOneCallPerWindow(t *testing.T) {
	const iterations = 1000
	rng := rand.New(rand.NewPCG(0x45_54_41_4c, 20)) //nolint:gosec // seeded test stream, not a credential
	origin := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)

	for iter := 0; iter < iterations; iter++ {
		l := NewLimiter(AttemptWindow)
		nJobs := 1 + rng.IntN(8)
		ticks := 1 + rng.IntN(120) // up to 60 min of 30 s ticks

		active := make(map[int64]bool, nJobs)
		for id := int64(1); id <= int64(nJobs); id++ {
			active[id] = true
		}
		firstTick := map[int64]int{}
		attempts := map[int64][]time.Time{}

		for tick := 0; tick < ticks; tick++ {
			now := origin.Add(time.Duration(tick) * Interval)
			set := make(map[int64]struct{}, len(active))
			for id, on := range active {
				if on {
					set[id] = struct{}{}
					if _, seen := firstTick[id]; !seen {
						firstTick[id] = tick
					}
				}
			}
			l.Prune(set)
			for id := range set {
				if l.Allow(id, now) {
					attempts[id] = append(attempts[id], now)
				}
			}
			// A job may leave the active set for good (its state moved on).
			for id, on := range active {
				if on && rng.IntN(20) == 0 {
					active[id] = false
				}
			}
		}

		for id, got := range attempts {
			for i := 1; i < len(got); i++ {
				if gap := got[i].Sub(got[i-1]); gap < AttemptWindow {
					t.Fatalf("iter %d job %d: attempts %s apart, want at least %s", iter, id, gap, AttemptWindow)
				}
			}
			// From the first active tick to the last tick, the window bounds the attempts.
			span := time.Duration(ticks-1-firstTick[id]) * Interval
			if limit := int(span/AttemptWindow) + 1; len(got) > limit {
				t.Fatalf("iter %d job %d: %d attempts in a span of %s, at most %d allowed", iter, id, len(got), span, limit)
			}
		}
	}
}
