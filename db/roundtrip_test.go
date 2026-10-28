package db_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"fleetops/db"
	"fleetops/internal/testinfra"
)

// TestMigrationRoundTrip is the P1 smoke test extended in P2, P17 and P19: up ->
// down to zero -> up on PostGIS, with migrations 001-011 applied.
func TestMigrationRoundTrip(t *testing.T) {
	dsn := testinfra.StartPostgis(t)

	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	src, err := iofs.New(db.Migrations, "migrations")
	if err != nil {
		t.Fatalf("iofs source: %v", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, dsn)
	if err != nil {
		t.Fatalf("migrate instance: %v", err)
	}
	t.Cleanup(func() { _, _ = m.Close() })

	t.Run("up", func(t *testing.T) {
		if err := m.Up(); err != nil {
			t.Fatalf("up: %v", err)
		}
		assertVersion(t, m, 11)
		assertSchema(t, conn, true)
	})

	t.Run("down", func(t *testing.T) {
		if err := m.Down(); err != nil {
			t.Fatalf("down: %v", err)
		}
		if _, _, err := m.Version(); !errors.Is(err, migrate.ErrNilVersion) {
			t.Fatalf("version after down: want ErrNilVersion, got %v", err)
		}
		assertSchema(t, conn, false)
	})

	t.Run("up again", func(t *testing.T) {
		if err := m.Up(); err != nil {
			t.Fatalf("up again: %v", err)
		}
		assertVersion(t, m, 11)
		assertSchema(t, conn, true)
	})

	t.Run("up is idempotent", func(t *testing.T) {
		if err := m.Up(); !errors.Is(err, migrate.ErrNoChange) {
			t.Fatalf("second up: want ErrNoChange, got %v", err)
		}
	})
}

func assertVersion(t *testing.T, m *migrate.Migrate, want uint) {
	t.Helper()
	v, dirty, err := m.Version()
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if dirty {
		t.Fatalf("schema is dirty at version %d", v)
	}
	if v != want {
		t.Fatalf("version: want %d, got %d", want, v)
	}
}

func assertSchema(t *testing.T, conn *sql.DB, present bool) {
	t.Helper()
	for _, obj := range []struct {
		kind, name, query string
	}{
		{"table", "fleets", `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'fleets')`},
		{"table", "users", `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'users')`},
		{"table", "refresh_tokens", `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'refresh_tokens')`},
		{"table", "admin_audit", `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'admin_audit')`},
		{"table", "vehicles", `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'vehicles')`},
		{"table", "drivers", `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'drivers')`},
		{"table", "devices", `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'devices')`},
		{"table", "vehicle_pairings", `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'vehicle_pairings')`},
		{"table", "positions", `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'positions')`},
		{"table", "vehicles_latest", `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'vehicles_latest')`},
		{"index", "vehicles_latest_geom_planar_gix", `SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = 'public' AND indexname = 'vehicles_latest_geom_planar_gix')`},
		{"function", "admin_audit_append_only", `SELECT EXISTS (SELECT 1 FROM pg_proc WHERE proname = 'admin_audit_append_only')`},
		{"table", "jobs", `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'jobs')`},
		{"table", "job_transitions", `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'job_transitions')`},
		{"table", "job_events", `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'job_events')`},
		{"index", "jobs_fleet_window_idx", `SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = 'public' AND indexname = 'jobs_fleet_window_idx')`},
		{"function", "jobs_transition_guard", `SELECT EXISTS (SELECT 1 FROM pg_proc WHERE proname = 'jobs_transition_guard')`},
		{"function", "jobs_record_create", `SELECT EXISTS (SELECT 1 FROM pg_proc WHERE proname = 'jobs_record_create')`},
		{"function", "job_events_append_only", `SELECT EXISTS (SELECT 1 FROM pg_proc WHERE proname = 'job_events_append_only')`},
		{"extension", "postgis", `SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'postgis')`},
		{"extension", "citext", `SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'citext')`},
		{"column", "fleets.timezone", `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'fleets' AND column_name = 'timezone')`},
	} {
		var got bool
		if err := conn.QueryRow(obj.query).Scan(&got); err != nil {
			t.Fatalf("query %s %s: %v", obj.kind, obj.name, err)
		}
		if got != present {
			t.Errorf("%s %s present=%v, want %v", obj.kind, obj.name, got, present)
		}
	}
}
