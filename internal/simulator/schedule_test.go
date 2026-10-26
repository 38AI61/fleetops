package simulator

import (
	"context"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// recordingSender is a test double of our own Sender interface (GF-148). It is
// not a mock of infrastructure. Each send draws its outcome from rapid.
type recordingSender struct {
	rt *rapid.T
}

func (r recordingSender) Send(context.Context, string, PositionBody) (Outcome, error) {
	return Outcome(rapid.IntRange(0, 2).Draw(r.rt, "outcome")), nil
}

// TestPropEmitIntervalRespected drives the pacer through random rates, fleet
// sizes, and step sequences with stalls (GF-150). It checks one emit per Due
// per vehicle, the spacing rules, and that the outcome tally plus skipped
// equals the due slot count.
func TestPropEmitIntervalRespected(t *testing.T) {
	start := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	rapid.Check(t, func(rt *rapid.T) {
		rate := time.Duration(rapid.IntRange(500, 60000).Draw(rt, "rateMs")) * time.Millisecond
		n := rapid.IntRange(1, 8).Draw(rt, "n")
		vehicles := make([]Vehicle, n)
		for i := range vehicles {
			r, err := NewRoute(int64(i+1), testWaypoints())
			if err != nil {
				rt.Fatalf("NewRoute: %v", err)
			}
			vehicles[i] = Vehicle{Index: i, DeviceID: int64(i + 1), Key: "key", Route: r}
		}
		p := NewPacer(start, rate, vehicles, recordingSender{rt: rt}, nil)

		now := start
		type last struct {
			index int64
			at    time.Time
		}
		prev := make(map[int]last)
		steps := rapid.IntRange(1, 40).Draw(rt, "steps")
		for step := 0; step < steps; step++ {
			now = now.Add(time.Duration(rapid.Int64Range(0, int64(4*rate)).Draw(rt, "advance")))
			seen := make(map[int]bool)
			for _, sl := range p.Step(context.Background(), now) {
				if seen[sl.Vehicle] {
					rt.Fatalf("vehicle %d emitted twice in one Due call", sl.Vehicle)
				}
				seen[sl.Vehicle] = true
				if sl.DueAt.After(now) {
					rt.Fatalf("slot due %s emitted at %s", sl.DueAt, now)
				}
				if l, ok := prev[sl.Vehicle]; ok {
					if sl.Index <= l.index {
						rt.Fatalf("vehicle %d slot %d after %d", sl.Vehicle, sl.Index, l.index)
					}
					gap := sl.DueAt.Sub(l.at)
					if gap < rate {
						rt.Fatalf("vehicle %d gap %s below rate %s", sl.Vehicle, gap, rate)
					}
					if sl.Index == l.index+1 && gap != rate {
						rt.Fatalf("vehicle %d gap %s without a skip, want %s", sl.Vehicle, gap, rate)
					}
				}
				prev[sl.Vehicle] = last{index: sl.Index, at: sl.DueAt}
			}
		}

		// Every due slot is either sent (one outcome) or skipped.
		var due int64
		for i := 0; i < n; i++ {
			offset := rate * time.Duration(i) / time.Duration(n)
			elapsed := now.Sub(start) - offset
			if elapsed >= 0 {
				due += int64(elapsed/rate) + 1
			}
		}
		s := p.Summary()
		if got := s.Total.Accepted + s.Total.Rejected + s.Total.Failed + s.Total.Skipped; got != due {
			rt.Fatalf("accepted+rejected+failed+skipped = %d, due slots = %d", got, due)
		}
	})
}

// TestScheduleDropsMissedSlots: a late tick emits once, counts the skipped
// slots, and never bursts (GF-145).
func TestScheduleDropsMissedSlots(t *testing.T) {
	start := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	s := NewScheduler(start, time.Second, 1)

	got := s.Due(start.Add(500 * time.Millisecond))
	if len(got) != 1 || got[0].Index != 0 || s.Skipped(0) != 0 {
		t.Fatalf("first tick = %+v, skipped %d", got, s.Skipped(0))
	}

	// Stall for 10 s. Slots 1 through 9 are dropped; slot 10 is the one emitted.
	got = s.Due(start.Add(10500 * time.Millisecond))
	if len(got) != 1 || got[0].Index != 10 {
		t.Fatalf("late tick = %+v, want one emit at slot 10", got)
	}
	if s.Skipped(0) != 9 {
		t.Fatalf("skipped = %d, want 9", s.Skipped(0))
	}

	if got := s.Due(start.Add(10600 * time.Millisecond)); len(got) != 0 {
		t.Fatalf("no slot is due yet, got %+v", got)
	}
	if got := s.Due(start.Add(11 * time.Second)); len(got) != 1 || got[0].Index != 11 {
		t.Fatalf("next tick = %+v, want slot 11", got)
	}
}
