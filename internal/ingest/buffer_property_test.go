package ingest

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"pgregory.net/rapid"
)

// TestFlushSemanticsProperty drives the pure buffer on a virtual clock (group 7a).
func TestFlushSemanticsProperty(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		events := rapid.IntRange(1, 2000).Draw(rt, "events")
		c := &Counters{}
		b := newTestBuffer(c)

		now := bufT0
		var history []Message
		accepted := map[uuid.UUID]int{}
		emitted := map[uuid.UUID]int{}
		offers, expectedDrops := 0, 0

		emit := func(batch []Message, what string) {
			if len(batch) < 1 || len(batch) > testMax {
				rt.Fatalf("%s batch length %d outside [1, %d]", what, len(batch), testMax)
			}
			for _, m := range batch {
				emitted[m.MsgID]++
			}
		}

		for i := 0; i < events; i++ {
			now = now.Add(time.Duration(rapid.IntRange(0, 1500).Draw(rt, "advance_ms")) * time.Millisecond)

			deadline, armed := b.Deadline()
			if batch := b.Tick(now); batch != nil {
				if !armed || now.Before(deadline) {
					rt.Fatalf("Tick emitted before the deadline: now %v, deadline %v, armed %v", now, deadline, armed)
				}
				emit(batch, "Tick")
			}

			if rapid.Bool().Draw(rt, "offer") {
				var m Message
				if len(history) > 0 && rapid.Bool().Draw(rt, "redeliver") {
					m = history[rapid.IntRange(0, len(history)-1).Draw(rt, "redelivered_index")]
				} else {
					m = msgWithAccuracy(rapid.Float64Range(0, 100).Draw(rt, "accuracy"))
					history = append(history, m)
				}
				offers++
				if m.Position.Accuracy > testAccuracy {
					expectedDrops++
				}

				buffered, batch := b.Offer(m, now)
				if buffered {
					accepted[m.MsgID]++
				}
				if batch != nil {
					if len(batch) != testMax {
						rt.Fatalf("Offer emitted %d messages, want exactly %d", len(batch), testMax)
					}
					emit(batch, "Offer")
				}
			}

			if dl, ok := b.Deadline(); ok && !dl.After(now) {
				rt.Fatalf("buffered message waits past its deadline: now %v, deadline %v", now, dl)
			}
		}

		if rest := b.Drain(); rest != nil {
			emit(rest, "Drain")
		}

		if !sameCounts(accepted, emitted) {
			rt.Fatalf("emitted messages differ from accepted offers")
		}
		if got := c.DroppedAccuracy.Load(); got != int64(expectedDrops) {
			rt.Fatalf("DroppedAccuracy = %d, want %d of %d offers", got, expectedDrops, offers)
		}
	})
}

func sameCounts(a, b map[uuid.UUID]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
