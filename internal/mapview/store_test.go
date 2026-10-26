package mapview

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"fleetops/internal/testinfra"
)

// seedVehicle inserts a vehicle with a latest position. Points are (lng, lat).
func seedVehicle(t *testing.T, db *sql.DB, fleetID int64, plate string, lng, lat float64) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(
		`INSERT INTO vehicles (fleet_id, plate) VALUES ($1, $2) RETURNING id`, fleetID, plate,
	).Scan(&id); err != nil {
		t.Fatalf("insert vehicle %s: %v", plate, err)
	}
	ts := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`
		INSERT INTO vehicles_latest (vehicle_id, fleet_id, geom, speed_kmh, heading_deg, device_ts, server_ts)
		VALUES ($1, $2, ST_SetSRID(ST_MakePoint($3, $4), 4326)::geography, 42, 271.5, $5, $5)`,
		id, fleetID, lng, lat, ts); err != nil {
		t.Fatalf("insert vehicles_latest %s: %v", plate, err)
	}
	return id
}

func newFleet(t *testing.T, db *sql.DB, name string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`INSERT INTO fleets (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("insert fleet: %v", err)
	}
	return id
}

// fixtureBox is the box every query test uses. Fixture points keep at least
// 100 m from every edge (GF-128): 0.002 deg of longitude at lat 52.5 is about 137 m.
var fixtureBox = BBox{MinLng: 13.30, MinLat: 52.45, MaxLng: 13.50, MaxLat: 52.60}

// TestBBoxQueryFixture covers in-box, out-of-box, other-fleet in-box, and the
// empty result (GF-128).
func TestBBoxQueryFixture(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	ctx := context.Background()
	caller := newFleet(t, db, "caller")
	other := newFleet(t, db, "other")

	inA := seedVehicle(t, db, caller, "IN-A", 13.40, 52.52)
	inB := seedVehicle(t, db, caller, "IN-B", 13.3020, 52.50)
	seedVehicle(t, db, caller, "OUT", 13.60, 52.52)
	seedVehicle(t, db, other, "OTHER-IN", 13.41, 52.52)

	rows, err := InBox(ctx, db, caller, fixtureBox, 10)
	if err != nil {
		t.Fatalf("InBox: %v", err)
	}
	got := make([]int64, 0, len(rows))
	for _, r := range rows {
		got = append(got, r.VehicleID)
	}
	if len(got) != 2 || got[0] != inA || got[1] != inB {
		t.Fatalf("in-box ids = %v, want [%d %d]", got, inA, inB)
	}
	if rows[0].Lat != 52.52 || rows[0].Lng != 13.40 || !rows[0].SpeedKmh.Valid || rows[0].SpeedKmh.Float64 != 42 {
		t.Fatalf("row 0 = %+v", rows[0])
	}

	empty, err := InBox(ctx, db, caller, BBox{MinLng: 0, MinLat: 0, MaxLng: 1, MaxLat: 1}, 10)
	if err != nil {
		t.Fatalf("InBox empty: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("empty box returned %d rows", len(empty))
	}
}

// TestBBoxWholeWorldAndWide covers boxes the geography predicate got wrong
// (GF-128 amendment): a whole-world box must not raise the antipodal-edge
// error, and a box wider than 90 degrees of longitude must keep points that
// lie inside it. Points sit at least 100 m from every edge.
func TestBBoxWholeWorldAndWide(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	ctx := context.Background()
	caller := newFleet(t, db, "world")
	other := newFleet(t, db, "world-other")

	pts := []struct {
		name     string
		lng, lat float64
	}{
		{"BERLIN", 13.40, 52.52},
		{"NYC", -74.00, 40.71},
		{"SYDNEY", 151.21, -33.87},
		{"SOUTH-EDGE", -179.9, -89.9},
		{"NORTH-EDGE", 179.9, 89.9},
	}
	names := map[int64]string{}
	for _, p := range pts {
		names[seedVehicle(t, db, caller, p.name, p.lng, p.lat)] = p.name
	}
	seedVehicle(t, db, other, "OTHER-BERLIN", 13.41, 52.52)

	cases := []struct {
		name string
		box  BBox
		want []string
	}{
		{"whole world", BBox{MinLng: -180, MinLat: -90, MaxLng: 180, MaxLat: 90},
			[]string{"BERLIN", "NYC", "SYDNEY", "SOUTH-EDGE", "NORTH-EDGE"}},
		{"wide south to north lng -100..160", BBox{MinLng: -100, MinLat: -40, MaxLng: 160, MaxLat: 60},
			[]string{"BERLIN", "NYC", "SYDNEY"}},
		{"wide northern band", BBox{MinLng: -170, MinLat: 0, MaxLng: 170, MaxLat: 85},
			[]string{"BERLIN", "NYC"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := InBox(ctx, db, caller, tc.box, 1001)
			if err != nil {
				t.Fatalf("InBox: %v", err)
			}
			got := make([]string, 0, len(rows))
			for _, r := range rows {
				got = append(got, names[r.VehicleID])
			}
			if !sameSet(got, tc.want) {
				t.Fatalf("in-box names = %v, want %v", got, tc.want)
			}
		})
	}
}

// sameSet reports whether a and b hold the same strings, ignoring order.
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
		if seen[s] < 0 {
			return false
		}
	}
	return true
}

// TestBBoxIndexScan proves the planar predicate uses the planar expression
// GIST index (GF-128, GF-134). It disables sequential scans so the planner
// must pick an index when one fits.
func TestBBoxIndexScan(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	ctx := context.Background()
	fleet := newFleet(t, db, "scan")
	for i := 0; i < 20; i++ {
		seedVehicle(t, db, fleet, fmt.Sprintf("SCAN-%02d", i), 13.40, 52.52)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatalf("set enable_seqscan: %v", err)
	}

	// EXPLAIN takes literal values, so the placeholders are replaced here.
	query := strings.NewReplacer(
		"$1", "1", "$2", "13.3", "$3", "52.45", "$4", "13.5", "$5", "52.6", "$6", "1001",
	).Replace(inBoxSQL)
	rows, err := tx.QueryContext(ctx, "EXPLAIN "+query)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		plan.WriteString(line + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("plan rows: %v", err)
	}
	if !strings.Contains(plan.String(), "vehicles_latest_geom_planar_gix") {
		t.Fatalf("plan does not use vehicles_latest_geom_planar_gix:\n%s", plan.String())
	}
}
