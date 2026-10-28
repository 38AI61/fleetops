// Package seed bootstraps the first fleet and admin (D-05, GF-23). It runs only
// when fleets is empty and never writes an audit row: there is no actor yet.
package seed

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	pgmigrate "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	fleetdb "fleetops/db"
	"fleetops/internal/alerts"
	"fleetops/internal/auth"
	"fleetops/internal/config"
	"fleetops/internal/devicesfile"
	"fleetops/internal/users"
)

// ErrUsage marks input problems the operator can fix: bad flags, a weak
// password, or no password source. main maps it to exit code 2.
var ErrUsage = errors.New("usage")

// Options are the seed inputs. Timezone is an IANA name such as "UTC".
// DemoDevices adds the demo vehicles and tracker devices on first seed only
// (GF-135). It needs DeviceKeySalt of at least config.MinDeviceKeySaltBytes.
// DevicesPath defaults to devicesfile.DefaultPath. DemoUsersPath defaults to
// DemoUsersDefaultPath and receives the demo driver logins (GF-339).
type Options struct {
	AdminEmail    string
	FleetName     string
	Timezone      string
	DemoDevices   bool
	DeviceKeySalt []byte
	DevicesPath   string
	DemoUsersPath string
}

// PasswordSource returns the admin password. It is called only when a seed is
// about to happen, so an already-seeded database never prompts.
type PasswordSource func() (string, error)

// Result reports what Run did. Created is false when the database was already seeded.
type Result struct {
	Created bool
	FleetID int64
	UserID  int64
}

// Validate checks the flags before any database work and returns them normalized.
func Validate(opts Options) (Options, error) {
	email := strings.TrimSpace(opts.AdminEmail)
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return Options{}, fmt.Errorf("%w: --admin-email is not a plain email address", ErrUsage)
	}
	name := strings.TrimSpace(opts.FleetName)
	if name == "" {
		return Options{}, fmt.Errorf("%w: --fleet-name must not be empty", ErrUsage)
	}
	tz := strings.TrimSpace(opts.Timezone)
	if _, err := time.LoadLocation(tz); err != nil {
		return Options{}, fmt.Errorf("%w: --timezone %q is not a known IANA zone", ErrUsage, tz)
	}
	// The salt check runs before any database work (GF-135).
	if opts.DemoDevices && len(opts.DeviceKeySalt) < config.MinDeviceKeySaltBytes {
		return Options{}, fmt.Errorf("%w: --demo-devices needs DEVICE_KEY_SALT of at least %d bytes", ErrUsage, config.MinDeviceKeySaltBytes)
	}
	path := opts.DevicesPath
	if path == "" {
		path = devicesfile.DefaultPath
	}
	usersPath := opts.DemoUsersPath
	if usersPath == "" {
		usersPath = DemoUsersDefaultPath
	}
	return Options{
		AdminEmail:    users.NormalizeEmail(email),
		FleetName:     name,
		Timezone:      tz,
		DemoDevices:   opts.DemoDevices,
		DeviceKeySalt: opts.DeviceKeySalt,
		DevicesPath:   path,
		DemoUsersPath: usersPath,
	}, nil
}

// Run seeds one fleet and one admin when fleets is empty. Concurrent runs are
// serialized by LOCK TABLE fleets, and the loser sees a non-empty table and
// returns Created=false.
func Run(ctx context.Context, db *sql.DB, opts Options, pw PasswordSource) (Result, error) {
	opts, err := Validate(opts)
	if err != nil {
		return Result{}, err
	}
	if err := requireMigrated(db); err != nil {
		return Result{}, err
	}

	seeded, err := fleetCount(ctx, db)
	if err != nil {
		return Result{}, err
	}
	if seeded {
		return Result{}, nil
	}

	password, err := pw()
	if err != nil {
		return Result{}, err
	}
	// Policy runs before the hash and before any write (GF-23).
	if err := auth.CheckPolicy(password); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrUsage, err)
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return Result{}, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, fmt.Errorf("begin seed: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `LOCK TABLE fleets IN EXCLUSIVE MODE`); err != nil {
		return Result{}, fmt.Errorf("lock fleets: %w", err)
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM fleets`).Scan(&n); err != nil {
		return Result{}, fmt.Errorf("recount fleets: %w", err)
	}
	if n > 0 {
		return Result{}, nil
	}

	var fleetID int64
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO fleets (name, timezone) VALUES ($1, $2) RETURNING id`,
		opts.FleetName, opts.Timezone,
	).Scan(&fleetID); err != nil {
		return Result{}, fmt.Errorf("insert fleet: %w", err)
	}
	// The three alert rules commit with the fleet (D-08, GF-290).
	if err := alerts.SeedRules(ctx, tx, fleetID); err != nil {
		return Result{}, fmt.Errorf("seed alert rules: %w", err)
	}

	store := users.NewStore(db)
	admin, err := store.Create(ctx, tx, fleetID, opts.AdminEmail, hash, "admin")
	if err != nil {
		return Result{}, fmt.Errorf("insert admin: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Result{}, fmt.Errorf("commit seed: %w", err)
	}
	res := Result{Created: true, FleetID: fleetID, UserID: admin.ID}
	if opts.DemoDevices {
		// Demo devices run only in this branch, after the fleet commits (GF-135).
		if err := RunDemo(ctx, db, res, opts.DeviceKeySalt, opts.DevicesPath, opts.DemoUsersPath, time.Now); err != nil {
			return res, err
		}
	}
	return res, nil
}

func fleetCount(ctx context.Context, db *sql.DB) (bool, error) {
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM fleets`).Scan(&n); err != nil {
		return false, fmt.Errorf("count fleets: %w", err)
	}
	return n > 0, nil
}

// requireMigrated is the seed self-check (GF-22). It reads schema_migrations
// through the golang-migrate library and refuses to seed a database that is
// dirty or behind the embedded migrations.
func requireMigrated(db *sql.DB) error {
	src, err := iofs.New(fleetdb.Migrations, "migrations")
	if err != nil {
		return fmt.Errorf("migration source: %w", err)
	}
	drv, err := pgmigrate.WithInstance(db, &pgmigrate.Config{})
	if err != nil {
		return fmt.Errorf("migration driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "postgres", drv)
	if err != nil {
		return fmt.Errorf("migrate instance: %w", err)
	}

	got, dirty, err := m.Version()
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if dirty {
		return fmt.Errorf("schema is dirty at version %d: run make migrate", got)
	}
	want, err := latestVersion(src)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("schema is at version %d, expected %d: run make migrate", got, want)
	}
	return nil
}

func latestVersion(src source.Driver) (uint, error) {
	v, err := src.First()
	if err != nil {
		return 0, fmt.Errorf("first migration: %w", err)
	}
	for {
		next, err := src.Next(v)
		if errors.Is(err, os.ErrNotExist) {
			return v, nil
		}
		if err != nil {
			return 0, fmt.Errorf("next migration: %w", err)
		}
		v = next
	}
}
