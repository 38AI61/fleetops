package presence

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"fleetops/internal/fanout"
	"fleetops/internal/testinfra"
)

// seedPairedVehicle inserts a fleet, a device, a vehicle paired to it, and a
// vehicles_latest row stamped at serverTS. It returns the device id. Device ids
// restart in each test database, so the device's Redis key is cleared here.
func seedPairedVehicle(t *testing.T, db *sql.DB, rdb *redis.Client, fleet int64, serverTS time.Time) int64 {
	t.Helper()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `INSERT INTO fleets (id, name) VALUES ($1, 'presence test') ON CONFLICT DO NOTHING`, fleet); err != nil {
		t.Fatalf("fleet: %v", err)
	}
	var device, vehicle int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO devices (fleet_id, kind, api_key_hash) VALUES ($1, 'tracker', '\x00') RETURNING id`, fleet,
	).Scan(&device); err != nil {
		t.Fatalf("device: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO vehicles (fleet_id, plate, paired_device_id) VALUES ($1, $2, $3) RETURNING id`,
		fleet, "P-"+time.Now().Format("150405.000000000"), device,
	).Scan(&vehicle); err != nil {
		t.Fatalf("vehicle: %v", err)
	}
	setLatest(t, db, vehicle, fleet, serverTS)
	if err := rdb.Del(ctx, deviceKey(device)).Err(); err != nil {
		t.Fatalf("clear device key: %v", err)
	}
	return device
}

// setLatest writes the vehicle's vehicles_latest row at serverTS.
func setLatest(t *testing.T, db *sql.DB, vehicle, fleet int64, serverTS time.Time) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
INSERT INTO vehicles_latest (vehicle_id, fleet_id, geom, device_ts, server_ts)
VALUES ($1, $2, ST_SetSRID(ST_MakePoint(13.4, 52.5), 4326)::geography, $3, $3)
ON CONFLICT (vehicle_id) DO UPDATE SET server_ts = EXCLUDED.server_ts`,
		vehicle, fleet, serverTS)
	if err != nil {
		t.Fatalf("vehicles_latest: %v", err)
	}
}

// vehicleOf returns the vehicle paired to device.
func vehicleOf(t *testing.T, db *sql.DB, device int64) int64 {
	t.Helper()
	var v int64
	if err := db.QueryRowContext(context.Background(),
		`SELECT id FROM vehicles WHERE paired_device_id = $1`, device).Scan(&v); err != nil {
		t.Fatalf("vehicle of device %d: %v", device, err)
	}
	return v
}

func TestSweepEmitsOfflineOncePerEpisode(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	rdb := newRedis(t)
	const fleet = 94001
	ch := subscribe(t, rdb, fleet)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	t0 := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	device := seedPairedVehicle(t, db, rdb, fleet, t0)
	vehicle := vehicleOf(t, db, device)
	tr := NewTracker(rdb, fanout.NewRedisFanout(rdb), 120*time.Second)
	sw := NewSweeper(db, tr, nil)
	obs := func(ts time.Time) []Observation {
		return []Observation{{DeviceID: device, FleetID: fleet, ServerTS: ts}}
	}
	stale := t0.Add(121 * time.Second)

	mustObserve := func(ts time.Time) {
		t.Helper()
		if _, err := tr.Observe(ctx, obs(ts), ts); err != nil {
			t.Fatalf("observe: %v", err)
		}
	}
	mustSweep := func(now time.Time) Result {
		t.Helper()
		res, err := sw.Sweep(ctx, now)
		if err != nil {
			t.Fatalf("sweep: %v", err)
		}
		return res
	}

	mustObserve(t0)
	if ev := nextEvent(t, ch); ev.State != "online" {
		t.Fatalf("first observe: %+v", ev)
	}

	if res := mustSweep(stale); res.Offline != 1 {
		t.Fatalf("first sweep: %+v, want one offline", res)
	}
	if ev := nextEvent(t, ch); ev.State != "offline" || ev.DeviceID != device {
		t.Fatalf("first sweep event: %+v", ev)
	}
	if res := mustSweep(stale); res.Offline != 0 {
		t.Fatalf("second sweep: %+v, want no offline", res)
	}
	expectNoEvent(t, ch)

	// A new position brings the device back online.
	t1 := t0.Add(200 * time.Second)
	setLatest(t, db, vehicle, fleet, t1)
	mustObserve(t1)
	if ev := nextEvent(t, ch); ev.State != "online" {
		t.Fatalf("re-observe: %+v", ev)
	}

	// The next stale episode emits offline again.
	if res := mustSweep(t1.Add(121 * time.Second)); res.Offline != 1 {
		t.Fatalf("second episode sweep: %+v, want one offline", res)
	}
	if ev := nextEvent(t, ch); ev.State != "offline" {
		t.Fatalf("second episode event: %+v", ev)
	}
}

func TestSweepRaceGuardKeepsFreshDevice(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	rdb := newRedis(t)
	const fleet = 94002
	ch := subscribe(t, rdb, fleet)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	t0 := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	device := seedPairedVehicle(t, db, rdb, fleet, t0)
	tr := NewTracker(rdb, fanout.NewRedisFanout(rdb), 120*time.Second)
	if _, err := tr.Observe(ctx, []Observation{{DeviceID: device, FleetID: fleet, ServerTS: t0}}, t0); err != nil {
		t.Fatalf("observe: %v", err)
	}
	nextEvent(t, ch)

	// The sweep reads the stale row. Then a flush commits a newer position for
	// the same device before the sweep takes the lock.
	now := t0.Add(130 * time.Second)
	sw := NewSweeper(db, tr, nil)
	sw.afterSnapshot = func() {
		fresh := now.Add(-5 * time.Second)
		if _, err := tr.Observe(ctx, []Observation{{DeviceID: device, FleetID: fleet, ServerTS: fresh}}, fresh); err != nil {
			t.Errorf("racing observe: %v", err)
		}
	}
	res, err := sw.Sweep(ctx, now)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Offline != 0 {
		t.Fatalf("sweep emitted %d offline for a fresh device, want 0", res.Offline)
	}
	// The racing observe left the device online in memory, so no event is due.
	expectNoEvent(t, ch)
}

func TestSweepBootEmitsOncePerStaleDevice(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	rdb := newRedis(t)
	const fleet = 94003
	ch := subscribe(t, rdb, fleet)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	t0 := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	a := seedPairedVehicle(t, db, rdb, fleet, t0)
	b := seedPairedVehicle(t, db, rdb, fleet, t0)

	// A fresh tracker has no memory: both stale devices are announced once.
	tr := NewTracker(rdb, fanout.NewRedisFanout(rdb), 120*time.Second)
	sw := NewSweeper(db, tr, nil)
	now := t0.Add(121 * time.Second)
	res, err := sw.Sweep(ctx, now)
	if err != nil || res.Offline != 2 {
		t.Fatalf("boot sweep: res=%+v err=%v, want two offline", res, err)
	}
	seen := map[int64]int{}
	for range 2 {
		seen[nextEvent(t, ch).DeviceID]++
	}
	if seen[a] != 1 || seen[b] != 1 {
		t.Fatalf("offline per device %v, want one each", seen)
	}
	if res, err := sw.Sweep(ctx, now); err != nil || res.Offline != 0 {
		t.Fatalf("second boot sweep: res=%+v err=%v, want none", res, err)
	}
	expectNoEvent(t, ch)
}
