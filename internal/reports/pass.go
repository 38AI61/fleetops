package reports

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"
)

// Interval is the rollup tick (GF-320). Today is recomputed on every tick.
const Interval = 15 * time.Minute

// rollLockKey is the rollup advisory-lock key, "ROLL". It is separate from the
// partition lock so maintenance is not blocked (GF-320).
const rollLockKey int64 = 0x52_4f_4c_4c

// Skip reasons, the label of fleetops_reports_rollup_passes_skipped_total (GF-325).
const (
	reasonLockHeld = "lock_held"
	reasonFleetTZ  = "fleet_tz"
)

// Loop recomputes the daily rollup (D-32). It lives in services/ingest beside the
// ETA loop. Each pass runs in one transaction under rollLockKey (GF-294).
type Loop struct {
	DB      *sql.DB
	Clock   func() time.Time
	Metrics *Metrics
}

// Run runs a boot pass, then one pass per interval, until ctx is done. A pass
// error is logged; the next tick tries again.
func (l *Loop) Run(ctx context.Context, interval time.Duration) {
	l.tick(ctx, true)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.tick(ctx, false)
		}
	}
}

func (l *Loop) tick(ctx context.Context, boot bool) {
	if err := l.Pass(ctx, boot); err != nil && ctx.Err() == nil {
		log.Printf("reports: pass: %v", err)
	}
}

func (l *Loop) now() time.Time {
	if l.Clock != nil {
		return l.Clock()
	}
	return time.Now()
}

// fleetZone is one fleet row read at the start of a pass (GF-297).
type fleetZone struct {
	id int64
	tz string
}

// Pass recomputes every fleet's today, and yesterday when the boot flag is set or
// the fleet-local time is in [00:00, 00:30) (GF-320). A pass that cannot take the
// lock does nothing and is counted.
func (l *Loop) Pass(ctx context.Context, boot bool) error {
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin rollup pass: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var locked bool
	if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock($1)`, rollLockKey).Scan(&locked); err != nil {
		return fmt.Errorf("rollup advisory lock: %w", err)
	}
	if !locked {
		l.Metrics.skipped(reasonLockHeld)
		return nil
	}
	start := time.Now()
	now := l.now()

	fleets, err := loadFleets(ctx, tx)
	if err != nil {
		return err
	}
	for _, f := range fleets {
		loc, err := time.LoadLocation(f.tz)
		if err != nil {
			// A bad timezone skips this fleet only, and is logged on each pass (GF-320).
			log.Printf("reports: fleet %d timezone %q: %v", f.id, f.tz, err)
			l.Metrics.skipped(reasonFleetTZ)
			continue
		}
		local := now.In(loc)
		today := LocalDay(local, loc)
		days := []time.Time{today}
		if boot || inYesterdayWindow(local) {
			days = []time.Time{today.AddDate(0, 0, -1), today}
		}
		for _, day := range days {
			if _, err := RollupDay(ctx, tx, f.id, loc, day); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit rollup pass: %w", err)
	}
	l.Metrics.observe(time.Since(start))
	return nil
}

// inYesterdayWindow reports whether fleet-local time is in [00:00, 00:30) (GF-320).
// Two 15 min ticks land in it, so the recompute is at-least-once.
func inYesterdayWindow(local time.Time) bool {
	return local.Hour() == 0 && local.Minute() < 30
}

// loadFleets reads every fleet and its timezone in one query (GF-297). No cache.
func loadFleets(ctx context.Context, tx *sql.Tx) ([]fleetZone, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, timezone FROM fleets ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("load fleets: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []fleetZone
	for rows.Next() {
		var f fleetZone
		if err := rows.Scan(&f.id, &f.tz); err != nil {
			return nil, fmt.Errorf("scan fleet: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
