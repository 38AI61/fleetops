package testinfra

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"sync"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"fleetops/db"
)

// shared holds the one container a test process uses (GF-29). It is not
// terminated explicitly; Testcontainers' reaper removes it when the process exits.
var shared struct {
	once sync.Once
	dsn  string
	err  error
}

// sharedDSN starts the container on first use and returns its admin DSN.
func sharedDSN(t testing.TB) string {
	t.Helper()
	shared.once.Do(func() {
		_, shared.dsn, shared.err = startPostgis(context.Background())
	})
	if shared.err != nil {
		t.Fatalf("start %s (is Docker running?): %v", PostgisImage, shared.err)
	}
	return shared.dsn
}

// NewMigratedDB returns a connection to a database that exists only for this
// test and has every migration applied through the golang-migrate library
// (GF-22). The database is dropped when the test ends.
func NewMigratedDB(t testing.TB) *sql.DB {
	t.Helper()
	conn, drop, err := createMigratedDatabase(sharedDSN(t))
	if err != nil {
		t.Fatalf("%v", err)
	}
	t.Cleanup(func() {
		if err := drop(); err != nil {
			t.Logf("%v", err)
		}
	})
	return conn
}

// Cleaner is the teardown and logging surface shared by testing.TB and *rapid.T.
type Cleaner interface {
	Cleanup(func())
	Logf(format string, args ...any)
}

// NewMigratedDBFrom starts or reuses the shared container through t and returns
// a constructor for fresh migrated databases (GF-46). The constructor takes the
// Cleaner of the current unit, so each database is dropped when that unit ends:
// a rapid check passes its *rapid.T, so 100 checks never hold 100 databases open
// at once. It panics on failure; rapid reports a panic as a failing check.
func NewMigratedDBFrom(t testing.TB) func(c Cleaner) *sql.DB {
	t.Helper()
	dsn := sharedDSN(t)
	return func(c Cleaner) *sql.DB {
		conn, drop, err := createMigratedDatabase(dsn)
		if err != nil {
			panic(err)
		}
		c.Cleanup(func() {
			if err := drop(); err != nil {
				c.Logf("%v", err)
			}
		})
		return conn
	}
}

// createMigratedDatabase creates a uniquely named database, applies every
// migration, and returns a connection plus a drop function.
func createMigratedDatabase(adminDSN string) (*sql.DB, func() error, error) {
	suffix, err := randomHex(8)
	if err != nil {
		return nil, nil, fmt.Errorf("database name: %w", err)
	}
	name := "t_" + suffix

	admin, err := sql.Open("postgres", adminDSN)
	if err != nil {
		return nil, nil, fmt.Errorf("open admin: %w", err)
	}
	defer func() { _ = admin.Close() }()
	if _, err := admin.Exec(`CREATE DATABASE "` + name + `"`); err != nil {
		return nil, nil, fmt.Errorf("create database %s: %w", name, err)
	}

	dsn, err := withDatabase(adminDSN, name)
	if err != nil {
		return nil, nil, fmt.Errorf("dsn for %s: %w", name, err)
	}
	if err := migrateUp(dsn); err != nil {
		return nil, nil, err
	}

	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("open %s: %w", name, err)
	}
	drop := func() error {
		_ = conn.Close()
		dropAdmin, err := sql.Open("postgres", adminDSN)
		if err != nil {
			return fmt.Errorf("open admin for drop: %w", err)
		}
		defer func() { _ = dropAdmin.Close() }()
		if _, err := dropAdmin.Exec(`DROP DATABASE IF EXISTS "` + name + `" WITH (FORCE)`); err != nil {
			return fmt.Errorf("drop database %s: %w", name, err)
		}
		return nil
	}
	return conn, drop, nil
}

func migrateUp(dsn string) error {
	src, err := iofs.New(db.Migrations, "migrations")
	if err != nil {
		return fmt.Errorf("iofs source: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, dsn)
	if err != nil {
		return fmt.Errorf("migrate instance: %w", err)
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Up(); err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

func withDatabase(dsn, name string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	u.Path = "/" + name
	return u.String(), nil
}
