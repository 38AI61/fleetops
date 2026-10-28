// Package presence tracks which devices are online (D-20). Redis holds the
// TTL'd key; an in-memory map holds the last emitted state, so each transition
// is published once. Presence is WS-only and is not persisted (GF-104).
package presence

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"fleetops/internal/fanout"
)

// Device states as carried on the wire (GF-101).
const (
	stateOnline  = "online"
	stateOffline = "offline"
)

// Result counts the transitions one call published (GF-121).
type Result struct {
	Online  int
	Offline int
}

// Observation is one stored position: the device, its fleet, and the server
// stamp that the flush committed (GF-104).
type Observation struct {
	DeviceID int64
	FleetID  int64
	ServerTS time.Time
}

// entry is the in-memory state of one device. Entries are never evicted; the
// map is bounded by the device count and resets on restart (GF-106).
type entry struct {
	state    string
	fleetID  int64
	lastSeen time.Time
}

// Tracker writes presence keys and publishes online transitions (GF-104). The
// mutex is shared with Sweeper, so flush and sweep emissions are ordered (GF-106).
type Tracker struct {
	store Store
	fan   fanout.Fanout
	ttl   time.Duration

	mu      sync.Mutex
	entries map[int64]*entry
}

// NewTracker returns a tracker with an empty in-memory state. ttl is the store
// TTL and the sweeper threshold (GF-107). store is RedisStore in redis mode and
// MemoryStore in nats mode (GF-347).
func NewTracker(store Store, fan fanout.Fanout, ttl time.Duration) *Tracker {
	return &Tracker{store: store, fan: fan, ttl: ttl, entries: map[int64]*entry{}}
}

// deviceKey is the Redis key for a device (GF-101).
func deviceKey(id int64) string {
	return fmt.Sprintf("device:%d", id)
}

// Observe refreshes presence for each distinct device with SET … EX ttl GET
// through the store (GF-104). A device goes online when its key was absent or
// its in-memory state is offline. Redis errors are joined and returned; the
// in-memory state still advances, because the DB already holds the position.
func (t *Tracker) Observe(ctx context.Context, obs []Observation, now time.Time) (Result, error) {
	if len(obs) == 0 {
		return Result{}, nil
	}

	// Coalesce duplicate devices: the TTL refresh is identical within a flush.
	latest := map[int64]Observation{}
	var order []int64
	for _, o := range obs {
		prev, seen := latest[o.DeviceID]
		if !seen {
			order = append(order, o.DeviceID)
			latest[o.DeviceID] = o
			continue
		}
		if o.ServerTS.After(prev.ServerTS) {
			latest[o.DeviceID] = o
		}
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	var res Result
	var errs []error
	for _, id := range order {
		o := latest[id]
		e := t.entries[id]

		_, found, err := t.store.SetGet(ctx, deviceKey(id), stateOnline, t.ttl)
		if err != nil {
			errs = append(errs, fmt.Errorf("set %s: %w", deviceKey(id), err))
		} else if !found || (e != nil && e.state == stateOffline) {
			if err := t.publish(ctx, o.FleetID, id, stateOnline, now); err != nil {
				errs = append(errs, err)
			} else {
				res.Online++
			}
		}

		if e == nil {
			e = &entry{}
			t.entries[id] = e
		}
		e.state = stateOnline
		e.fleetID = o.FleetID
		if o.ServerTS.After(e.lastSeen) {
			e.lastSeen = o.ServerTS
		}
	}
	return res, errors.Join(errs...)
}

// publish sends one presence.changed event. The caller holds t.mu.
func (t *Tracker) publish(ctx context.Context, fleetID, deviceID int64, state string, ts time.Time) error {
	ev := fanout.PresenceEvent{FleetID: fleetID, DeviceID: deviceID, State: state, TS: ts}
	if err := t.fan.PublishPresence(ctx, ev); err != nil {
		return fmt.Errorf("presence %s for device %d: %w", state, deviceID, err)
	}
	return nil
}
