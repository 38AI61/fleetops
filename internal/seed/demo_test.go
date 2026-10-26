package seed_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fleetops/internal/devicesfile"
	"fleetops/internal/registry"
	"fleetops/internal/seed"
	"fleetops/internal/testinfra"
)

// demoSalt is 32 bytes of test data, the minimum (GF-30). It is never a real secret.
var demoSalt = []byte(strings.Repeat("d", 32))

func demoOpts(path string) seed.Options {
	o := validOpts()
	o.DemoDevices = true
	o.DeviceKeySalt = demoSalt
	o.DevicesPath = path
	return o
}

func count(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", q, err)
	}
	return n
}

// TestDemoDevicesCreatesFiftyPairedDevices: 50 DEMO vehicles, 50 trackers,
// 1:1 open pairings, and a file with 50 entries that match the database (GF-136, GF-137).
func TestDemoDevicesCreatesFiftyPairedDevices(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	path := filepath.Join(t.TempDir(), "devices.json")
	res, err := seed.Run(context.Background(), db, demoOpts(path), constPW(goodPW))
	if err != nil || !res.Created {
		t.Fatalf("Run = %+v, %v; want created", res, err)
	}

	if n := count(t, db, `SELECT count(*) FROM vehicles WHERE fleet_id = $1 AND plate ~ '^DEMO-[0-9]{3}$'`, res.FleetID); n != 50 {
		t.Fatalf("demo vehicles = %d, want 50", n)
	}
	if n := count(t, db, `SELECT count(*) FROM devices WHERE fleet_id = $1 AND kind = 'tracker'`, res.FleetID); n != 50 {
		t.Fatalf("tracker devices = %d, want 50", n)
	}
	if n := count(t, db, `SELECT count(*) FROM vehicle_pairings WHERE ended_at IS NULL`); n != 50 {
		t.Fatalf("open pairings = %d, want 50", n)
	}
	if n := count(t, db, `SELECT count(DISTINCT device_id) FROM vehicle_pairings WHERE ended_at IS NULL`); n != 50 {
		t.Fatalf("distinct paired devices = %d, want 50", n)
	}

	f, err := devicesfile.Read(path)
	if err != nil {
		t.Fatalf("Read devices file: %v", err)
	}
	if len(f.Devices) != 50 || f.FleetID != res.FleetID {
		t.Fatalf("file has %d devices fleet %d, want 50 and %d", len(f.Devices), f.FleetID, res.FleetID)
	}
	for _, d := range f.Devices {
		var paired sql.NullInt64
		if err := db.QueryRow(`SELECT paired_device_id FROM vehicles WHERE id = $1`, d.VehicleID).Scan(&paired); err != nil {
			t.Fatalf("vehicle %d: %v", d.VehicleID, err)
		}
		if !paired.Valid || paired.Int64 != d.DeviceID {
			t.Fatalf("vehicle %d paired to %v, file says device %d", d.VehicleID, paired, d.DeviceID)
		}
	}
}

// TestDemoDevicesRerunIdempotent: a second run is a no-op with exit 0, no new
// rows, and the same file bytes (GF-135).
func TestDemoDevicesRerunIdempotent(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	path := filepath.Join(t.TempDir(), "devices.json")
	ctx := context.Background()
	if _, err := seed.Run(ctx, db, demoOpts(path), constPW(goodPW)); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	before, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	vehicles := count(t, db, `SELECT count(*) FROM vehicles`)
	devices := count(t, db, `SELECT count(*) FROM devices`)
	audit := count(t, db, `SELECT count(*) FROM admin_audit`)

	res, err := seed.Run(ctx, db, demoOpts(path), constPW(goodPW))
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if res.Created {
		t.Fatal("second Run reported Created")
	}
	after, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("reread file: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("rerun rewrote devices.json")
	}
	if count(t, db, `SELECT count(*) FROM vehicles`) != vehicles ||
		count(t, db, `SELECT count(*) FROM devices`) != devices ||
		count(t, db, `SELECT count(*) FROM admin_audit`) != audit {
		t.Fatal("rerun wrote rows")
	}
}

// TestDemoKeysAuthenticate: every key in the file passes the registry's device
// authentication, so the simulator's keys are the ones ingest accepts (GF-136).
func TestDemoKeysAuthenticate(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	path := filepath.Join(t.TempDir(), "devices.json")
	ctx := context.Background()
	if _, err := seed.Run(ctx, db, demoOpts(path), constPW(goodPW)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	f, err := devicesfile.Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	for _, d := range f.Devices {
		dev, err := registry.AuthenticateDeviceKey(ctx, db, demoSalt, d.Key)
		if err != nil {
			t.Fatalf("device %d key rejected: %v", d.DeviceID, err)
		}
		if dev.ID != d.DeviceID {
			t.Fatalf("key authenticated device %d, file says %d", dev.ID, d.DeviceID)
		}
	}
}

// TestDemoRequiresSalt: a missing or short salt with the flag is a usage error
// before any database write (GF-135).
func TestDemoRequiresSalt(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	path := filepath.Join(t.TempDir(), "devices.json")
	for name, salt := range map[string][]byte{
		"missing":  nil,
		"31 bytes": []byte(strings.Repeat("d", 31)),
	} {
		o := demoOpts(path)
		o.DeviceKeySalt = salt
		_, err := seed.Run(context.Background(), db, o, constPW(goodPW))
		if !errors.Is(err, seed.ErrUsage) {
			t.Fatalf("%s: err = %v, want ErrUsage", name, err)
		}
	}
	if n := count(t, db, `SELECT count(*) FROM fleets`); n != 0 {
		t.Fatalf("fleets = %d after usage errors, want 0", n)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("devices file exists after usage error: %v", err)
	}
}

// TestDemoAuditRows: the issue, pair, and vehicle create actions are audited
// under the seeded admin (GF-136).
func TestDemoAuditRows(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	path := filepath.Join(t.TempDir(), "devices.json")
	res, err := seed.Run(context.Background(), db, demoOpts(path), constPW(goodPW))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for action, want := range map[string]int{"device.issue": 50, "vehicle.pair": 50, "vehicle.create": 50} {
		got := count(t, db,
			`SELECT count(*) FROM admin_audit WHERE action = $1 AND actor_id = $2 AND fleet_id = $3`,
			action, res.UserID, res.FleetID)
		if got != want {
			t.Fatalf("audit %s = %d, want %d", action, got, want)
		}
	}
}
