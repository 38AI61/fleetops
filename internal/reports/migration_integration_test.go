package reports

import (
	"database/sql"
	"reflect"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	pgmigrate "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	fleetdb "fleetops/db"
	"fleetops/internal/testinfra"
)

// stepper returns a migrate instance over the same database as db. The test
// steps 012 down and up through it (GF-5: down is kept for the round-trip test).
func stepper(t *testing.T, db *sql.DB) *migrate.Migrate {
	t.Helper()
	src, err := iofs.New(fleetdb.Migrations, "migrations")
	if err != nil {
		t.Fatalf("iofs source: %v", err)
	}
	drv, err := pgmigrate.WithInstance(db, &pgmigrate.Config{})
	if err != nil {
		t.Fatalf("migrate driver: %v", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "postgres", drv)
	if err != nil {
		t.Fatalf("migrate instance: %v", err)
	}
	// The driver holds one connection until Close; release it so the database can drop.
	t.Cleanup(func() { _, _ = m.Close() })
	return m
}

// tableExists reports whether daily_driver_stats is present.
func tableExists(t *testing.T, db *sql.DB) bool {
	t.Helper()
	var name sql.NullString
	if err := db.QueryRow(`SELECT to_regclass('daily_driver_stats')::text`).Scan(&name); err != nil {
		t.Fatalf("to_regclass: %v", err)
	}
	return name.Valid
}

// schemaShape reads the column list, primary key, check constraint and index of
// daily_driver_stats. It is compared after each step of the round trip.
type schemaShape struct {
	Columns    []string
	PrimaryKey []string
	Checks     []string
	Indexes    []string
}

func readShape(t *testing.T, db *sql.DB) schemaShape {
	t.Helper()
	var s schemaShape
	rows, err := db.Query(`
		SELECT column_name || ' ' || data_type || ' ' || is_nullable
		FROM information_schema.columns
		WHERE table_name = 'daily_driver_stats' ORDER BY ordinal_position`)
	if err != nil {
		t.Fatalf("columns: %v", err)
	}
	s.Columns = scanStrings(t, rows)

	rows, err = db.Query(`
		SELECT a.attname::text
		FROM pg_index i
		JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
		WHERE i.indrelid = 'daily_driver_stats'::regclass AND i.indisprimary
		ORDER BY array_position(i.indkey::int2[], a.attnum)`)
	if err != nil {
		t.Fatalf("primary key: %v", err)
	}
	s.PrimaryKey = scanStrings(t, rows)

	rows, err = db.Query(`
		SELECT conname::text FROM pg_constraint
		WHERE conrelid = 'daily_driver_stats'::regclass AND contype = 'c' ORDER BY conname`)
	if err != nil {
		t.Fatalf("checks: %v", err)
	}
	s.Checks = scanStrings(t, rows)

	rows, err = db.Query(`
		SELECT indexname::text FROM pg_indexes
		WHERE tablename = 'daily_driver_stats' ORDER BY indexname`)
	if err != nil {
		t.Fatalf("indexes: %v", err)
	}
	s.Indexes = scanStrings(t, rows)
	return s
}

func scanStrings(t *testing.T, rows *sql.Rows) []string {
	t.Helper()
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

// TestMigration012RoundTrip: up, down, up (GF-5, GF-319). After up the shape is
// the GF-319 shape; after down the table is gone; after the second up it matches.
func TestMigration012RoundTrip(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	m := stepper(t, db)

	up := readShape(t, db)
	wantColumns := []string{
		"fleet_id bigint NO",
		"driver_id bigint NO",
		"day date NO",
		"jobs_completed integer NO",
		"on_time_count integer NO",
		"on_time_denom integer NO",
		"distance_m double precision NO",
		"idle_s integer NO",
	}
	if !reflect.DeepEqual(up.Columns, wantColumns) {
		t.Fatalf("columns after up = %v, want %v", up.Columns, wantColumns)
	}
	if want := []string{"fleet_id", "driver_id", "day"}; !reflect.DeepEqual(up.PrimaryKey, want) {
		t.Fatalf("primary key = %v, want %v", up.PrimaryKey, want)
	}
	if want := []string{"daily_driver_stats_bounds"}; !reflect.DeepEqual(up.Checks, want) {
		t.Fatalf("checks = %v, want %v", up.Checks, want)
	}
	if !contains(up.Indexes, "daily_driver_stats_fleet_day_idx") {
		t.Fatalf("indexes = %v, want daily_driver_stats_fleet_day_idx", up.Indexes)
	}

	if err := m.Migrate(11); err != nil {
		t.Fatalf("down to 11: %v", err)
	}
	if tableExists(t, db) {
		t.Fatal("table survived down")
	}

	if err := m.Up(); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if again := readShape(t, db); !reflect.DeepEqual(again, up) {
		t.Fatalf("shape after second up = %+v, want %+v", again, up)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
