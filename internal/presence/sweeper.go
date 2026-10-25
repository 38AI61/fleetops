package presence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"
)

// SweepInterval is how often the sweeper checks vehicles_latest (GF-106).
const SweepInterval = 15 * time.Second

// sweepBudget bounds each presence publish made by a sweep (GF-103).
const sweepBudget = time.Second

// qStale selects paired devices whose latest position is older than the cutoff
// (GF-106). vehicles_latest has no device column, so the device comes from the
// vehicle's current pairing.
const qStale = `
SELECT v.paired_device_id, vehicles_latest.fleet_id, vehicles_latest.server_ts
FROM vehicles_latest
JOIN vehicles v ON v.id = vehicles_latest.vehicle_id
WHERE vehicles_latest.server_ts < $1 AND v.paired_device_id IS NOT NULL`

// Sweeper emits offline transitions for devices whose position went stale (D-20).
// The DB is the source of truth for offline; the sweeper only publishes.
type Sweeper struct {
	db *sql.DB
	t  *Tracker

	// Report, when set, receives the outcome of each Run tick (GF-121).
	Report func(Result, error)

	// afterSnapshot runs after the stale rows are read and before the lock is
	// taken. Tests use it to interleave a flush with the snapshot (GF-106).
	afterSnapshot func()
}

// NewSweeper returns a sweeper that shares its tracker's in-memory state.
func NewSweeper(db *sql.DB, t *Tracker, report func(Result, error)) *Sweeper {
	return &Sweeper{db: db, t: t, Report: report}
}

// staleRow is one paired device whose latest position is older than the cutoff.
type staleRow struct {
	deviceID int64
	fleetID  int64
	serverTS time.Time
}

// Sweep runs one pass at instant now (GF-106). It emits offline once per
// episode: only when the device is absent from memory or not yet offline, and
// its last-seen stamp is older than the cutoff. A flush that committed after
// the query snapshot updates lastSeen first, so the race guard keeps the device.
func (s *Sweeper) Sweep(ctx context.Context, now time.Time) (Result, error) {
	cutoff := now.Add(-s.t.ttl)
	rows, err := s.db.QueryContext(ctx, qStale, cutoff)
	if err != nil {
		return Result{}, fmt.Errorf("sweep query: %w", err)
	}
	var stale []staleRow
	for rows.Next() {
		var r staleRow
		if err := rows.Scan(&r.deviceID, &r.fleetID, &r.serverTS); err != nil {
			_ = rows.Close()
			return Result{}, fmt.Errorf("sweep scan: %w", err)
		}
		stale = append(stale, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return Result{}, fmt.Errorf("sweep rows: %w", err)
	}
	_ = rows.Close()
	if s.afterSnapshot != nil {
		s.afterSnapshot()
	}

	t := s.t
	t.mu.Lock()
	defer t.mu.Unlock()

	var res Result
	var errs []error
	for _, r := range stale {
		e := t.entries[r.deviceID]
		if e == nil {
			e = &entry{lastSeen: r.serverTS}
			t.entries[r.deviceID] = e
		} else if e.state == stateOffline || !e.lastSeen.Before(cutoff) {
			continue
		}
		// State is set before the publish, so the event fires once per episode.
		e.state = stateOffline
		e.fleetID = r.fleetID

		pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sweepBudget)
		err := t.publish(pctx, r.fleetID, r.deviceID, stateOffline, now)
		cancel()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		res.Offline++
	}
	return res, errors.Join(errs...)
}

// Run sweeps every SweepInterval until ctx is cancelled (GF-106).
func (s *Sweeper) Run(ctx context.Context) {
	ticker := time.NewTicker(SweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			res, err := s.Sweep(ctx, now)
			if err != nil && ctx.Err() == nil {
				log.Printf("presence: sweep: %v", err)
			}
			if s.Report != nil {
				s.Report(res, err)
			}
		}
	}
}
