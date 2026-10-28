package alerts

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	pgmigrate "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	fleetdb "fleetops/db"
	"fleetops/internal/testinfra"
)

// ruleRow is one alert_rules row as the tests read it back.
type ruleRow struct {
	Kind    string
	Params  map[string]any
	Enabled bool
}

// readRules returns the fleet's rules ordered by kind.
func readRules(t *testing.T, db *sql.DB, fleet int64) []ruleRow {
	t.Helper()
	rows, err := db.Query(`SELECT kind, params, enabled FROM alert_rules WHERE fleet_id = $1 ORDER BY kind`, fleet)
	if err != nil {
		t.Fatalf("read rules: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ruleRow
	for rows.Next() {
		var r ruleRow
		var raw []byte
		if err := rows.Scan(&r.Kind, &raw, &r.Enabled); err != nil {
			t.Fatalf("scan rule: %v", err)
		}
		if err := json.Unmarshal(raw, &r.Params); err != nil {
			t.Fatalf("params json: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rules rows: %v", err)
	}
	return out
}

// wantRules is the D-08 seed: silent 10 min on, ETA slip off, schedule window on.
func wantRules() []ruleRow {
	return []ruleRow{
		{Kind: KindDeviceSilent, Params: map[string]any{"threshold_s": float64(600)}, Enabled: true},
		{Kind: KindETASlip, Params: map[string]any{}, Enabled: false},
		{Kind: KindGeofenceExit, Params: map[string]any{}, Enabled: true},
	}
}

// TestSeedRulesThreeRowsIdempotent: SeedRules writes the three fixed kinds and a
// second call inside a new transaction changes nothing (D-08, GF-290).
func TestSeedRulesThreeRowsIdempotent(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	ctx := context.Background()
	fleet := newFleet(t, db, "seed")

	for pass := 0; pass < 2; pass++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if err := SeedRules(ctx, tx, fleet); err != nil {
			_ = tx.Rollback()
			t.Fatalf("SeedRules pass %d: %v", pass, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit pass %d: %v", pass, err)
		}
	}
	if got := readRules(t, db, fleet); !reflect.DeepEqual(got, wantRules()) {
		t.Fatalf("rules = %+v, want %+v", got, wantRules())
	}
}

// TestAlertRulesBackfill: 011's backfill gives every existing fleet the same three
// rows SeedRules writes. The test steps the migration down, adds a fleet, and steps up.
func TestAlertRulesBackfill(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
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
	if err := m.Migrate(10); err != nil {
		t.Fatalf("down to 10: %v", err)
	}
	fleet := newFleet(t, db, "legacy")
	if err := m.Up(); err != nil {
		t.Fatalf("up to 11: %v", err)
	}
	if got := readRules(t, db, fleet); !reflect.DeepEqual(got, wantRules()) {
		t.Fatalf("backfilled rules = %+v, want %+v", got, wantRules())
	}
}
