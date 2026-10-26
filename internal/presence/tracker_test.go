package presence

import (
	"context"
	"testing"
	"time"

	"fleetops/internal/fanout"
)

func TestPresenceSetGetTransitions(t *testing.T) {
	rdb := newRedis(t)
	const fleet, device = 92001, 93001
	ch := subscribe(t, rdb, fleet)
	tr := NewTracker(rdb, fanout.NewRedisFanout(rdb), 120*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ts := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	obs := []Observation{{DeviceID: device, FleetID: fleet, ServerTS: ts}}

	// Absent key: online is published.
	res, err := tr.Observe(ctx, obs, ts)
	if err != nil || res.Online != 1 {
		t.Fatalf("absent: res=%+v err=%v, want one online", res, err)
	}
	if ev := nextEvent(t, ch); ev.State != "online" || ev.DeviceID != device || ev.FleetID != fleet {
		t.Fatalf("absent: event %+v", ev)
	}

	// Present and online in memory: no event.
	res, err = tr.Observe(ctx, obs, ts)
	if err != nil || res.Online != 0 {
		t.Fatalf("present online: res=%+v err=%v, want no transition", res, err)
	}
	expectNoEvent(t, ch)

	// Present in Redis but offline in memory: online is published again.
	tr.mu.Lock()
	tr.entries[device].state = stateOffline
	tr.mu.Unlock()
	res, err = tr.Observe(ctx, obs, ts)
	if err != nil || res.Online != 1 {
		t.Fatalf("offline in memory: res=%+v err=%v, want one online", res, err)
	}
	if ev := nextEvent(t, ch); ev.State != "online" {
		t.Fatalf("offline in memory: event %+v", ev)
	}
}
