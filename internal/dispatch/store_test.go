package dispatch

import (
	"context"
	"database/sql"
	"math"
	"testing"
	"time"

	"fleetops/internal/testinfra"
)

// fixtureTS is the clock for vehicles_latest and pairings. Fixed so the fixture is deterministic.
var fixtureTS = time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)

// WGS84 constants for the independent distance reference (GF-212).
const (
	wgs84A  = 6378137.0
	wgs84E2 = 0.00669437999014
)

// meridianArc is the ellipsoidal arc length from the equator to latDeg, in
// meters: M = a·[A0·φ − A2·sin2φ + A4·sin4φ − A6·sin6φ] with the standard
// WGS84 coefficients. A point on a meridian is at this geodesic distance from
// (lat 0, lng 0). The series has no a(1−e²) prefactor; that factor belongs only
// to the B-coefficient form, and mixing the two is off by 0.67%.
func meridianArc(latDeg float64) float64 {
	e2 := wgs84E2
	e4 := e2 * e2
	e6 := e4 * e2
	a0 := 1 - e2/4 - 3*e4/64 - 5*e6/256
	a2 := 3.0 / 8 * (e2 + e4/4 + 15*e6/128)
	a4 := 15.0 / 256 * (e4 + 3*e6/4)
	a6 := 35 * e6 / 3072
	phi := latDeg * math.Pi / 180
	return wgs84A * (a0*phi - a2*math.Sin(2*phi) + a4*math.Sin(4*phi) - a6*math.Sin(6*phi))
}

// equatorArc is the geodesic length along the equator for a longitude offset: a * dλ.
func equatorArc(lngDeg float64) float64 {
	return wgs84A * lngDeg * math.Pi / 180
}

// TestMeridianArcReference checks the reference helper against the published
// WGS84 quarter meridian (10001965.729 m), so the fixture distances are trusted.
func TestMeridianArcReference(t *testing.T) {
	if got := meridianArc(90); math.Abs(got-10001965.729) > 0.01 {
		t.Fatalf("quarter meridian = %.3f, want 10001965.729", got)
	}
}

func newFleet(t *testing.T, db *sql.DB, name string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`INSERT INTO fleets (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("insert fleet: %v", err)
	}
	return id
}

func seedDriver(t *testing.T, db *sql.DB, fleet int64, name string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(
		`INSERT INTO drivers (fleet_id, name, license_no_hash) VALUES ($1, $2, $3) RETURNING id`,
		fleet, name, []byte(name),
	).Scan(&id); err != nil {
		t.Fatalf("insert driver %s: %v", name, err)
	}
	return id
}

func seedDevice(t *testing.T, db *sql.DB, fleet int64, key string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(
		`INSERT INTO devices (fleet_id, kind, api_key_hash) VALUES ($1, 'tracker', $2) RETURNING id`,
		fleet, []byte(key),
	).Scan(&id); err != nil {
		t.Fatalf("insert device %s: %v", key, err)
	}
	return id
}

func seedVehicle(t *testing.T, db *sql.DB, fleet int64, plate, status string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(
		`INSERT INTO vehicles (fleet_id, plate, status) VALUES ($1, $2, $3) RETURNING id`,
		fleet, plate, status,
	).Scan(&id); err != nil {
		t.Fatalf("insert vehicle %s: %v", plate, err)
	}
	return id
}

// seedLatest writes the vehicle's latest position at fixtureTS. Points are (lat, lng).
func seedLatest(t *testing.T, db *sql.DB, fleet, vehicle int64, lat, lng float64) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO vehicles_latest (vehicle_id, fleet_id, geom, speed_kmh, heading_deg, device_ts, server_ts)
		VALUES ($1, $2, ST_SetSRID(ST_MakePoint($3, $4), 4326)::geography, 30, 90, $5, $5)`,
		vehicle, fleet, lng, lat, fixtureTS); err != nil {
		t.Fatalf("insert vehicles_latest %d: %v", vehicle, err)
	}
}

// seedPairing opens a pairing unless ended is set. driver nil means NULL driver_id.
func seedPairing(t *testing.T, db *sql.DB, fleet, vehicle, device int64, driver *int64, ended bool) {
	t.Helper()
	var endedAt *time.Time
	if ended {
		e := fixtureTS.Add(-30 * time.Minute)
		endedAt = &e
	}
	if _, err := db.Exec(`
		INSERT INTO vehicle_pairings (fleet_id, vehicle_id, driver_id, device_id, started_at, ended_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		fleet, vehicle, driver, device, fixtureTS.Add(-time.Hour), endedAt); err != nil {
		t.Fatalf("insert pairing for vehicle %d: %v", vehicle, err)
	}
}

// seedPaired is an eligible row (GF-210): active, reported, open pairing with a
// driver of the same fleet. It returns the vehicle and driver ids.
func seedPaired(t *testing.T, db *sql.DB, fleet int64, plate string, lat, lng float64) (int64, int64) {
	t.Helper()
	v := seedVehicle(t, db, fleet, plate, "active")
	seedLatest(t, db, fleet, v, lat, lng)
	d := seedDriver(t, db, fleet, "Driver "+plate)
	seedPairing(t, db, fleet, v, seedDevice(t, db, fleet, "dev-"+plate), &d, false)
	return v, d
}

// TestNearestOrderingFixture is GF-210 to GF-212 against a hand-computed
// fixture. The pickup is (lat 0, lng 0). Expected distances come from the
// independent reference helpers and must match within 1 m.
func TestNearestOrderingFixture(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	ctx := context.Background()
	fleet := newFleet(t, db, "caller")
	other := newFleet(t, db, "other")

	// Eligible rows. FX-LNG-010 is at latitude 0, so it would rank first by
	// |lat| alone; it must rank fourth by distance.
	a, _ := seedPaired(t, db, fleet, "FX-LAT-001", 0.01, 0)
	b, _ := seedPaired(t, db, fleet, "FX-LAT-002", 0.02, 0)
	c, _ := seedPaired(t, db, fleet, "FX-LNG-010", 0, 0.10)
	d, _ := seedPaired(t, db, fleet, "FX-LAT-005", 0.05, 0)
	e, _ := seedPaired(t, db, fleet, "FX-LAT-020", 0.20, 0)

	// Decoys that must never appear. Each sits at (or near) the pickup, so
	// it would rank first if it were eligible.
	seedPaired(t, db, other, "OTHER-0", 0, 0)

	maint := seedVehicle(t, db, fleet, "DEC-MAINT", "maintenance")
	seedLatest(t, db, fleet, maint, 0.0001, 0)
	mDrv := seedDriver(t, db, fleet, "Driver DEC-MAINT")
	seedPairing(t, db, fleet, maint, seedDevice(t, db, fleet, "dev-DEC-MAINT"), &mDrv, false)

	retired := seedVehicle(t, db, fleet, "DEC-RETIRED", "retired")
	seedLatest(t, db, fleet, retired, 0.0001, 0)
	rDrv := seedDriver(t, db, fleet, "Driver DEC-RETIRED")
	seedPairing(t, db, fleet, retired, seedDevice(t, db, fleet, "dev-DEC-RETIRED"), &rDrv, false)

	unpaired := seedVehicle(t, db, fleet, "DEC-UNPAIRED", "active")
	seedLatest(t, db, fleet, unpaired, 0.0002, 0)

	nullDrv := seedVehicle(t, db, fleet, "DEC-NULLDRV", "active")
	seedLatest(t, db, fleet, nullDrv, 0.0003, 0)
	seedPairing(t, db, fleet, nullDrv, seedDevice(t, db, fleet, "dev-DEC-NULLDRV"), nil, false)

	ended := seedVehicle(t, db, fleet, "DEC-ENDED", "active")
	seedLatest(t, db, fleet, ended, 0.0005, 0)
	eDrv := seedDriver(t, db, fleet, "Driver DEC-ENDED")
	seedPairing(t, db, fleet, ended, seedDevice(t, db, fleet, "dev-DEC-ENDED"), &eDrv, true)

	foreignDrv := seedDriver(t, db, other, "Outsider")
	foreign := seedVehicle(t, db, fleet, "DEC-FOREIGN-DRV", "active")
	seedLatest(t, db, fleet, foreign, 0.0006, 0)
	seedPairing(t, db, fleet, foreign, seedDevice(t, db, fleet, "dev-DEC-FOREIGN"), &foreignDrv, false)

	never := seedVehicle(t, db, fleet, "DEC-NEVER", "active")
	nvDrv := seedDriver(t, db, fleet, "Driver DEC-NEVER")
	seedPairing(t, db, fleet, never, seedDevice(t, db, fleet, "dev-DEC-NEVER"), &nvDrv, false)

	store := &Store{DB: db}
	want := []struct {
		id    int64
		plate string
		dist  float64
	}{
		{a, "FX-LAT-001", meridianArc(0.01)},
		{b, "FX-LAT-002", meridianArc(0.02)},
		{d, "FX-LAT-005", meridianArc(0.05)},
		{c, "FX-LNG-010", equatorArc(0.10)},
		{e, "FX-LAT-020", meridianArc(0.20)},
	}

	got, err := store.Nearest(ctx, fleet, 0, 0, 10)
	if err != nil {
		t.Fatalf("Nearest: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		t.Logf("row %d: %s got %.1f m want %.3f m", i, g.Plate, g.DistanceM, w.dist)
		if g.VehicleID != w.id || g.Plate != w.plate {
			t.Fatalf("row %d = vehicle %d %s, want %d %s", i, g.VehicleID, g.Plate, w.id, w.plate)
		}
		if math.Abs(g.DistanceM-w.dist) > 1 {
			t.Fatalf("row %d distance = %.1f, want %.3f within 1 m", i, g.DistanceM, w.dist)
		}
		if g.DriverName != "Driver "+w.plate || g.DriverID == 0 {
			t.Fatalf("row %d driver = %d %q", i, g.DriverID, g.DriverName)
		}
		if math.Abs(g.Lat) > 1 || math.Abs(g.Lng) > 1 || !g.ServerTS.Equal(fixtureTS) {
			t.Fatalf("row %d position/ts = %v,%v %v", i, g.Lat, g.Lng, g.ServerTS)
		}
	}

	// LIMIT: the first three of the same order.
	limited, err := store.Nearest(ctx, fleet, 0, 0, 3)
	if err != nil {
		t.Fatalf("Nearest limit 3: %v", err)
	}
	if len(limited) != 3 || limited[0].VehicleID != a || limited[1].VehicleID != b || limited[2].VehicleID != d {
		t.Fatalf("limit 3 = %+v, want [%d %d %d]", limited, a, b, d)
	}
}

// TestNearestFleetScope places the pickup inside the other fleet's cluster.
// No row from the other fleet may appear, and the reverse query is symmetric.
func TestNearestFleetScope(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	ctx := context.Background()
	fleet := newFleet(t, db, "caller")
	other := newFleet(t, db, "other")

	far, _ := seedPaired(t, db, fleet, "CAL-FAR", 0.05, 0)
	o1, _ := seedPaired(t, db, other, "OTH-1", 0.0001, 0.0001)
	o2, _ := seedPaired(t, db, other, "OTH-2", 0.0002, 0.0001)

	store := &Store{DB: db}
	got, err := store.Nearest(ctx, fleet, 0.0001, 0.0001, 10)
	if err != nil {
		t.Fatalf("Nearest caller: %v", err)
	}
	if len(got) != 1 || got[0].VehicleID != far {
		t.Fatalf("caller rows = %+v, want only %d", got, far)
	}

	foreign, err := store.Nearest(ctx, other, 0.0001, 0.0001, 10)
	if err != nil {
		t.Fatalf("Nearest other: %v", err)
	}
	if len(foreign) != 2 || foreign[0].VehicleID != o1 || foreign[1].VehicleID != o2 {
		t.Fatalf("other rows = %+v, want [%d %d]", foreign, o1, o2)
	}
}
