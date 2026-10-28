package reports

import (
	"reflect"
	"testing"
	"time"
)

// TestRollupIdempotent: a second RollupDay on unchanged input writes nothing
// (RowsAffected 0 on delete and upsert) and leaves the table identical (GF-323, check i).
// A stale row for a driver with no activity is removed by the first run. A
// changed input writes exactly the changed driver's row.
func TestRollupIdempotent(t *testing.T) {
	f := newFx(t)
	fleet, drv := seedGolden(f)
	idle := f.driver(fleet, "Stale")
	if _, err := f.db.Exec(`
		INSERT INTO daily_driver_stats (fleet_id, driver_id, day, jobs_completed, on_time_count, on_time_denom, distance_m, idle_s)
		VALUES ($1, $2, $3, 1, 0, 0, 5, 0)`, fleet, idle, goldenDay.Format(time.DateOnly)); err != nil {
		t.Fatalf("insert stale row: %v", err)
	}

	first := f.rollup(fleet, goldenLoc, goldenDay)
	if first.Deleted != 1 || first.Upserted != 2 {
		t.Fatalf("first run = %+v, want {Deleted:1 Upserted:2}", first)
	}
	snap := f.stats(fleet, goldenDay)

	second := f.rollup(fleet, goldenLoc, goldenDay)
	if second != (Written{}) {
		t.Fatalf("second run = %+v, want zero rows affected", second)
	}
	if got := f.stats(fleet, goldenDay); !reflect.DeepEqual(got, snap) {
		t.Fatalf("table changed on second run:\n got %+v\nwant %+v", got, snap)
	}

	// One new delivery for D3 changes only D3's row.
	when := time.Date(2026, time.March, 8, 18, 0, 0, 0, time.UTC)
	f.job(fleet, "delivered", f.vehicle(fleet), drv["d3"], nil, &when)
	third := f.rollup(fleet, goldenLoc, goldenDay)
	if third != (Written{Upserted: 1}) {
		t.Fatalf("third run = %+v, want {Upserted:1}", third)
	}
}
