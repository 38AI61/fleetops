package seed_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fleetops/internal/auth"
	"fleetops/internal/dispatch"
	"fleetops/internal/seed"
	"fleetops/internal/testinfra"
)

// seededDemo runs a first seed with demo devices and drivers into a temp dir
// and returns the database, the result, and the two file paths (GF-339).
func seededDemo(t *testing.T) (db *sql.DB, res seed.Result, devicesPath, usersPath string) {
	t.Helper()
	db = testinfra.NewMigratedDB(t)
	devicesPath = filepath.Join(t.TempDir(), "devices.json")
	o := demoOpts(devicesPath)
	res, err := seed.Run(context.Background(), db, o, constPW(goodPW))
	if err != nil || !res.Created {
		t.Fatalf("Run = %+v, %v; want created", res, err)
	}
	return db, res, devicesPath, o.DemoUsersPath
}

// TestDemoCreatesFiftyDriversLinkedToUsers: 50 DRV drivers, and each has a
// driver-role user in the fleet whose driver_id points at its own driver row (GF-339).
func TestDemoCreatesFiftyDriversLinkedToUsers(t *testing.T) {
	db, res, _, _ := seededDemo(t)

	if n := count(t, db, `SELECT count(*) FROM drivers WHERE fleet_id = $1 AND name ~ '^Demo Driver [0-9]{3}$'`, res.FleetID); n != seed.DemoDriverCount {
		t.Fatalf("demo drivers = %d, want %d", n, seed.DemoDriverCount)
	}
	if n := count(t, db, `SELECT count(*) FROM users WHERE fleet_id = $1 AND role = 'driver' AND email ~ '^demo-driver-[0-9]{3}@example[.]com$'`, res.FleetID); n != seed.DemoDriverCount {
		t.Fatalf("demo driver users = %d, want %d", n, seed.DemoDriverCount)
	}
	linked := count(t, db, `
		SELECT count(*) FROM users u
		JOIN drivers d ON d.id = u.driver_id AND d.fleet_id = u.fleet_id
		WHERE u.fleet_id = $1 AND u.role = 'driver' AND u.email ~ '^demo-driver-[0-9]{3}@example[.]com$'`, res.FleetID)
	if linked != seed.DemoDriverCount {
		t.Fatalf("driver users linked to a fleet driver = %d, want %d", linked, seed.DemoDriverCount)
	}
}

// TestDemoDriversPairedOneToOne: DRV-n is paired to DEMO-n, and the P13 nearest
// query returns each van with its own driver (GF-339, CLAUDE.md P13).
func TestDemoDriversPairedOneToOne(t *testing.T) {
	db, res, _, _ := seededDemo(t)
	ctx := context.Background()

	rows, err := db.QueryContext(ctx, `
		SELECT v.plate, d.name FROM vehicle_pairings p
		JOIN vehicles v ON v.id = p.vehicle_id
		JOIN drivers d ON d.id = p.driver_id
		WHERE p.fleet_id = $1 AND p.ended_at IS NULL`, res.FleetID)
	if err != nil {
		t.Fatalf("pairings: %v", err)
	}
	pairs := 0
	for rows.Next() {
		var plate, name string
		if err := rows.Scan(&plate, &name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		pairs++
		if strings.TrimPrefix(plate, "DEMO-") != strings.TrimPrefix(name, "Demo Driver ") {
			t.Fatalf("van %s paired to %s, want its own number", plate, name)
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close pairings: %v", err)
	}
	if pairs != seed.DemoDriverCount {
		t.Fatalf("open driver pairings = %d, want %d", pairs, seed.DemoDriverCount)
	}

	// Give every van a latest position so nearest has candidates (P13).
	if _, err := db.ExecContext(ctx, `
		INSERT INTO vehicles_latest (vehicle_id, fleet_id, geom, device_ts)
		SELECT id, fleet_id, ST_SetSRID(ST_MakePoint(13.40, 52.52), 4326)::geography, now()
		FROM vehicles WHERE fleet_id = $1`, res.FleetID); err != nil {
		t.Fatalf("latest positions: %v", err)
	}
	cands, err := (&dispatch.Store{DB: db}).Nearest(ctx, res.FleetID, 52.52, 13.40, 50)
	if err != nil {
		t.Fatalf("nearest: %v", err)
	}
	if len(cands) != seed.DemoDriverCount {
		t.Fatalf("nearest candidates = %d, want %d", len(cands), seed.DemoDriverCount)
	}
	for _, c := range cands {
		if strings.TrimPrefix(c.Plate, "DEMO-") != strings.TrimPrefix(c.DriverName, "Demo Driver ") {
			t.Fatalf("candidate %s has driver %s, want its own number", c.Plate, c.DriverName)
		}
	}
}

// demoUsersFile reads and decodes demo-users.json.
func demoUsersFile(t *testing.T, path string) seed.DemoUsersFile {
	t.Helper()
	body, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read demo users: %v", err)
	}
	var f seed.DemoUsersFile
	if err := json.Unmarshal(body, &f); err != nil {
		t.Fatalf("decode demo users: %v", err)
	}
	return f
}

// TestDemoUsersFileMode0600AndAtomic: the logins file is 0600, complete, matches
// the database, and leaves no temp file behind (GF-339, GF-138).
func TestDemoUsersFileMode0600AndAtomic(t *testing.T) {
	db, res, devicesPath, usersPath := seededDemo(t)

	info, err := os.Stat(usersPath)
	if err != nil {
		t.Fatalf("stat demo users: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("demo users mode = %o, want 600", mode)
	}

	f := demoUsersFile(t, usersPath)
	if f.Version != seed.DemoUsersVersion || f.FleetID != res.FleetID {
		t.Fatalf("header = version %d fleet %d, want %d %d", f.Version, f.FleetID, seed.DemoUsersVersion, res.FleetID)
	}
	if len(f.Drivers) != seed.DemoDriverCount {
		t.Fatalf("drivers = %d, want %d", len(f.Drivers), seed.DemoDriverCount)
	}
	for _, d := range f.Drivers {
		if d.DriverID == 0 || d.UserID == 0 || d.VehicleID == 0 || d.Email == "" || d.Password == "" {
			t.Fatalf("incomplete entry: %+v", d)
		}
		var userDriver int64
		var plate string
		if err := db.QueryRow(`
			SELECT u.driver_id, v.plate FROM users u
			JOIN vehicles v ON v.id = $2 AND v.fleet_id = u.fleet_id
			WHERE u.id = $1`, d.UserID, d.VehicleID).Scan(&userDriver, &plate); err != nil {
			t.Fatalf("user %d or van %d: %v", d.UserID, d.VehicleID, err)
		}
		if userDriver != d.DriverID {
			t.Fatalf("user %d linked to driver %d, file says %d", d.UserID, userDriver, d.DriverID)
		}
		if strings.TrimPrefix(plate, "DEMO-") != strings.TrimPrefix(d.Email, "demo-driver-")[:3] {
			t.Fatalf("van %s with login %s", plate, d.Email)
		}
	}

	entries, err := os.ReadDir(filepath.Dir(devicesPath))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

// TestDemoPasswordsPassPolicy: every generated password is 24 characters, passes
// the policy, and is unique (GF-339, GF-23).
func TestDemoPasswordsPassPolicy(t *testing.T) {
	_, _, _, usersPath := seededDemo(t)
	seen := map[string]bool{}
	for _, d := range demoUsersFile(t, usersPath).Drivers {
		if n := len([]rune(d.Password)); n != 24 {
			t.Fatalf("password length = %d, want 24", n)
		}
		if err := auth.CheckPolicy(d.Password); err != nil {
			t.Fatalf("password fails policy: %v", err)
		}
		if seen[d.Password] {
			t.Fatal("two drivers share a password")
		}
		seen[d.Password] = true
	}
}

// TestDemoRerunIsNoOpExitZero: a rerun reports not created, never prompts, and
// leaves devices.json and demo-users.json byte-identical (GF-340).
func TestDemoRerunIsNoOpExitZero(t *testing.T) {
	db, _, devicesPath, usersPath := seededDemo(t)
	beforeDevices, err := os.ReadFile(filepath.Clean(devicesPath))
	if err != nil {
		t.Fatalf("read devices: %v", err)
	}
	beforeUsers, err := os.ReadFile(filepath.Clean(usersPath))
	if err != nil {
		t.Fatalf("read users: %v", err)
	}
	prompted := false
	prompt := func() (string, error) {
		prompted = true
		return goodPW, nil
	}

	res, err := seed.Run(context.Background(), db, demoOpts(devicesPath), prompt)
	if err != nil || res.Created {
		t.Fatalf("rerun = %+v, %v; want no-op", res, err)
	}
	if prompted {
		t.Fatal("rerun prompted for a password")
	}
	afterDevices, _ := os.ReadFile(filepath.Clean(devicesPath))
	afterUsers, _ := os.ReadFile(filepath.Clean(usersPath))
	if !bytes.Equal(beforeDevices, afterDevices) || !bytes.Equal(beforeUsers, afterUsers) {
		t.Fatal("rerun rewrote a demo file")
	}
	if n := count(t, db, `SELECT count(*) FROM drivers`); n != seed.DemoDriverCount {
		t.Fatalf("drivers after rerun = %d, want %d", n, seed.DemoDriverCount)
	}
}
