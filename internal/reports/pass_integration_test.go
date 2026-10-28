package reports

import (
	"context"
	"testing"
	"time"
)

// fixed returns a Clock that always reads t.
func fixed(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// TestPassLockSkipsAndCounts: a pass that cannot take ROLL does nothing and
// increments lock_held (GF-320). Once the lock is free the pass runs.
func TestPassLockSkipsAndCounts(t *testing.T) {
	f := newFx(t)
	fleet, _ := seedGolden(f)
	loop := &Loop{DB: f.db, Clock: fixed(at(12, 0, 0)), Metrics: f.m}
	ctx := context.Background()

	holder, err := f.db.Begin()
	if err != nil {
		t.Fatalf("begin holder: %v", err)
	}
	if _, err := holder.Exec(`SELECT pg_advisory_xact_lock($1)`, rollLockKey); err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	if err := loop.Pass(ctx, true); err != nil {
		t.Fatalf("locked pass: %v", err)
	}
	if n := skippedCount(t, f.m, reasonLockHeld); n != 1 {
		t.Fatalf("lock_held skips = %v, want 1", n)
	}
	if rows := f.stats(fleet, goldenDay); len(rows) != 0 {
		t.Fatalf("locked pass wrote %d rows", len(rows))
	}
	if err := holder.Commit(); err != nil {
		t.Fatalf("release: %v", err)
	}

	if err := loop.Pass(ctx, true); err != nil {
		t.Fatalf("pass after release: %v", err)
	}
	if n := skippedCount(t, f.m, reasonLockHeld); n != 1 {
		t.Fatalf("lock_held skips = %v after release, want still 1", n)
	}
	if rows := f.stats(fleet, goldenDay); len(rows) != 2 {
		t.Fatalf("pass after release wrote %d rows, want 2", len(rows))
	}
}

// TestPassPostMidnightWindow: at 00:45 fleet-local only today is recomputed; at
// 00:10 yesterday is recomputed as well (GF-320). The data sits on the 8th, so
// the 00:45 pass must leave the 8th alone.
func TestPassPostMidnightWindow(t *testing.T) {
	f := newFx(t)
	fleet := f.fleet("ny-late", "America/New_York")
	drv := f.driver(fleet, "Eve")
	v := f.vehicle(fleet)
	f.pair(fleet, v, drv, civil(2026, time.March, 1), nil)
	delivered := at(16, 0, 0) // 12:00 EDT on the 8th
	f.job(fleet, "delivered", v, drv, nil, &delivered)
	ctx := context.Background()
	eighth := civil(2026, time.March, 8)

	loop := &Loop{DB: f.db, Clock: fixed(time.Date(2026, time.March, 9, 4, 45, 0, 0, time.UTC)), Metrics: f.m}
	if err := loop.Pass(ctx, false); err != nil {
		t.Fatalf("00:45 pass: %v", err)
	}
	if rows := f.stats(fleet, eighth); len(rows) != 0 {
		t.Fatalf("00:45 pass recomputed yesterday: %+v", rows)
	}

	loop.Clock = fixed(time.Date(2026, time.March, 9, 4, 10, 0, 0, time.UTC))
	if err := loop.Pass(ctx, false); err != nil {
		t.Fatalf("00:10 pass: %v", err)
	}
	rows := f.stats(fleet, eighth)
	if len(rows) != 1 || rows[0].JobsCompleted != 1 {
		t.Fatalf("00:10 pass rows = %+v, want one row with one job", rows)
	}
}

// TestPassSkipsBadFleetTZ: an unloadable timezone skips that fleet and counts it
// as fleet_tz. Other fleets still roll up in the same pass (GF-320).
func TestPassSkipsBadFleetTZ(t *testing.T) {
	f := newFx(t)
	fleet, _ := seedGolden(f)
	f.fleet("bad-tz", "Not/A_Zone")
	loop := &Loop{DB: f.db, Clock: fixed(at(12, 0, 0)), Metrics: f.m}
	if err := loop.Pass(context.Background(), true); err != nil {
		t.Fatalf("pass: %v", err)
	}
	if n := skippedCount(t, f.m, reasonFleetTZ); n != 1 {
		t.Fatalf("fleet_tz skips = %v, want 1", n)
	}
	if rows := f.stats(fleet, goldenDay); len(rows) != 2 {
		t.Fatalf("good fleet rows = %d, want 2", len(rows))
	}
}
