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

// TestMigrationRoundTrip is the P1 smoke test: up -> down -> up on PostGIS.
func TestMigrationRoundTrip(t *testing.T) {
	dsn := testinfra.StartPostgis(t)

	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	src, err := iofs.New(db.Migrations, "migrations")
	if err != nil {
		t.Fatalf("iofs source: %v", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, dsn)
	if err != nil {
		t.Fatalf("migrate instance: %v", err)
	}
	t.Cleanup(func() { m.Close() })

	t.Run("up", func(t *testing.T) {
		if err := m.Up(); err != nil {
			t.Fatalf("up: %v", err)
		}
		assertVersion(t, m, 1)
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
		assertVersion(t, m, 1)
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
	var fleets, postgis bool
	if err := conn.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name = 'fleets')`).Scan(&fleets); err != nil {
		t.Fatalf("query fleets: %v", err)
	}
	if err := conn.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'postgis')`).Scan(&postgis); err != nil {
		t.Fatalf("query postgis: %v", err)
	}
	if fleets != present {
		t.Errorf("fleets table present=%v, want %v", fleets, present)
	}
	if postgis != present {
		t.Errorf("postgis extension present=%v, want %v", postgis, present)
	}
}
