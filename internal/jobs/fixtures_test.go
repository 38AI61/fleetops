package jobs

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/lib/pq"
)

// fleetWithAdmin inserts a fleet and one admin user in it and returns both IDs.
func fleetWithAdmin(t testing.TB, db *sql.DB, name string) (fleetID, userID int64) {
	t.Helper()
	if err := db.QueryRow(`INSERT INTO fleets (name) VALUES ($1) RETURNING id`, name).Scan(&fleetID); err != nil {
		t.Fatalf("insert fleet: %v", err)
	}
	if err := db.QueryRow(
		`INSERT INTO users (fleet_id, email, password_hash, role) VALUES ($1, $2, 'x', 'admin') RETURNING id`,
		fleetID, name+"-admin@example.test").Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return fleetID, userID
}

// fleetVehicle inserts an active vehicle in the fleet.
func fleetVehicle(t testing.TB, db *sql.DB, fleetID int64, plate string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`INSERT INTO vehicles (fleet_id, plate) VALUES ($1, $2) RETURNING id`, fleetID, plate).Scan(&id); err != nil {
		t.Fatalf("insert vehicle: %v", err)
	}
	return id
}

// fleetDriver inserts a driver in the fleet.
func fleetDriver(t testing.TB, db *sql.DB, fleetID int64, name string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(
		`INSERT INTO drivers (fleet_id, name, license_no_hash) VALUES ($1, $2, $3) RETURNING id`,
		fleetID, name, []byte(name)).Scan(&id); err != nil {
		t.Fatalf("insert driver: %v", err)
	}
	return id
}

// insertJobRaw inserts a created job with actor set in the same transaction,
// so the AFTER INSERT trigger can write the creation event. Tests that need a
// job without the store use it.
func insertJobRaw(t testing.TB, db *sql.DB, fleetID, actor int64) int64 {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`SELECT set_config('app.actor_id', $1::text, true)`, actor); err != nil {
		t.Fatalf("set actor: %v", err)
	}
	var id int64
	if err := tx.QueryRow(`
		INSERT INTO jobs (fleet_id, pickup, dropoff)
		VALUES ($1, ST_SetSRID(ST_MakePoint(28.9784, 41.0082), 4326)::geography,
		            ST_SetSRID(ST_MakePoint(29.0083, 41.0422), 4326)::geography)
		RETURNING id`, fleetID).Scan(&id); err != nil {
		t.Fatalf("insert job: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return id
}

// sqlState returns the SQLSTATE of a pq error, or "" for any other error.
func sqlState(err error) string {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return string(pqErr.Code)
	}
	return ""
}

// rowCount returns SELECT count(*) for a query with args.
func rowCount(t testing.TB, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}
