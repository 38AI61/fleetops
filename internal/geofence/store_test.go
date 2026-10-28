package geofence

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"fleetops/internal/testinfra"
)

// fixtureFleet inserts a fleet and returns its id.
func fixtureFleet(t testing.TB, db *sql.DB, name string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`INSERT INTO fleets (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("insert fleet: %v", err)
	}
	return id
}

// fixtureVehicle inserts an active vehicle in the fleet and returns its id.
func fixtureVehicle(t testing.TB, db *sql.DB, fleetID int64, plate string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`INSERT INTO vehicles (fleet_id, plate) VALUES ($1, $2) RETURNING id`, fleetID, plate).Scan(&id); err != nil {
		t.Fatalf("insert vehicle: %v", err)
	}
	return id
}

// fixtureFence inserts a small counter-clockwise square polygon with its
// bbox, straight into the table. The app write path is not used here.
func fixtureFence(t testing.TB, db *sql.DB, fleetID int64, name string) int64 {
	t.Helper()
	var id int64
	err := db.QueryRow(`
		INSERT INTO geofences (fleet_id, name, kind, geom, bbox)
		SELECT $1, $2, 'polygon', g::geography, ST_Envelope(g)
		FROM (SELECT ST_GeomFromText('POLYGON((13.0 52.0, 13.1 52.0, 13.1 52.1, 13.0 52.1, 13.0 52.0))', 4326) AS g) s
		RETURNING id`, fleetID, name).Scan(&id)
	if err != nil {
		t.Fatalf("insert geofence: %v", err)
	}
	return id
}

// TestGeofenceEventsAppendOnly inserts one fixture event row and checks that
// UPDATE, DELETE and TRUNCATE each fail with the append-only trigger (R-7).
// P17 has no event writer, so the insert is test-only.
func TestGeofenceEventsAppendOnly(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	fleet := fixtureFleet(t, db, "append-only")
	vehicle := fixtureVehicle(t, db, fleet, "AO-0001")
	fence := fixtureFence(t, db, fleet, "append-only fence")
	if _, err := db.Exec(`
		INSERT INTO geofence_events (fleet_id, geofence_id, vehicle_id, kind, occurred_at)
		VALUES ($1, $2, $3, 'enter', now())`, fleet, fence, vehicle); err != nil {
		t.Fatalf("insert fixture event: %v", err)
	}

	for _, stmt := range []string{
		`UPDATE geofence_events SET kind = 'exit'`,
		`DELETE FROM geofence_events`,
		`TRUNCATE geofence_events`,
	} {
		_, err := db.Exec(stmt)
		if err == nil {
			t.Fatalf("%s: want append-only error, got nil", stmt)
		}
		if !strings.Contains(err.Error(), "append-only") {
			t.Fatalf("%s: error %q does not mention append-only", stmt, err)
		}
	}

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM geofence_events`).Scan(&n); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if n != 1 {
		t.Fatalf("event rows = %d, want 1 (every blocked statement must leave the row)", n)
	}
}

// TestGeofenceBboxIndexUsed copies the P8 EXPLAIN pattern (mapview store_test):
// it proves geofences_bbox_gix serves the bbox overlap predicate P18 will use.
func TestGeofenceBboxIndexUsed(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	ctx := context.Background()
	fleet := fixtureFleet(t, db, "bbox-index")
	for i := 0; i < 20; i++ {
		fixtureFence(t, db, fleet, fmt.Sprintf("bbox fence %02d", i))
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
		"$1", "12.9", "$2", "51.9", "$3", "13.2", "$4", "52.2",
	).Replace(`SELECT id FROM geofences WHERE bbox && ST_MakeEnvelope($1, $2, $3, $4, 4326)`)
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
	if !strings.Contains(plan.String(), "geofences_bbox_gix") {
		t.Fatalf("plan does not use geofences_bbox_gix:\n%s", plan.String())
	}
}
