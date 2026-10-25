package testinfra

import (
	"context"
	"database/sql"
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

// NewMigratedDB returns a connection to a database that exists only for this
// test and has every migration applied through the golang-migrate library
// (GF-22). The database is dropped when the test ends.
func NewMigratedDB(t testing.TB) *sql.DB {
	t.Helper()

	shared.once.Do(func() {
		_, shared.dsn, shared.err = startPostgis(context.Background())
	})
	if shared.err != nil {
		t.Fatalf("start %s (is Docker running?): %v", PostgisImage, shared.err)
	}

	name, err := randomHex(8)
	if err != nil {
		t.Fatalf("database name: %v", err)
	}
	name = "t_" + name

	admin, err := sql.Open("postgres", shared.dsn)
	if err != nil {
		t.Fatalf("open admin: %v", err)
	}
	if _, err := admin.Exec(`CREATE DATABASE "` + name + `"`); err != nil {
		_ = admin.Close()
		t.Fatalf("create database %s: %v", name, err)
	}

	dsn, err := withDatabase(shared.dsn, name)
	if err != nil {
		t.Fatalf("dsn for %s: %v", name, err)
	}
	migrateUp(t, dsn)

	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		if _, err := admin.Exec(`DROP DATABASE IF EXISTS "` + name + `" WITH (FORCE)`); err != nil {
			t.Logf("drop database %s: %v", name, err)
		}
		_ = admin.Close()
	})
	return conn
}

func migrateUp(t testing.TB, dsn string) {
	t.Helper()
	src, err := iofs.New(db.Migrations, "migrations")
	if err != nil {
		t.Fatalf("iofs source: %v", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, dsn)
	if err != nil {
		t.Fatalf("migrate instance: %v", err)
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Up(); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
}

func withDatabase(dsn, name string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	u.Path = "/" + name
	return u.String(), nil
}
