package ingest

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"fleetops/internal/registry"
	"fleetops/internal/testinfra"
)

// flushBase is the fixed "now" of the flush tests; every server_ts is near it,
// inside the 2026-10 partition.
var flushBase = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

// flushEnv is one migrated database with a fleet, an admin actor, and a writer.
type flushEnv struct {
	t        testing.TB
	conn     *sql.DB
	svc      *registry.Service
	actor    registry.Actor
	fleet    int64
	writer   *Writer
	counters *Counters
}

func newFlushEnv(t testing.TB) *flushEnv {
	t.Helper()
	conn := testinfra.NewMigratedDB(t)
	testinfra.EnsurePositionsPartitions(t, conn, flushBase, flushBase)

	var fleet, user int64
	if err := conn.QueryRow(`INSERT INTO fleets (name) VALUES ('flush fleet') RETURNING id`).Scan(&fleet); err != nil {
		t.Fatalf("insert fleet: %v", err)
	}
	if err := conn.QueryRow(`INSERT INTO users (fleet_id, email, password_hash, role) VALUES ($1, 'admin@flush.invalid', 'x', 'admin') RETURNING id`, fleet).Scan(&user); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	actor := registry.Actor{UserID: user, FleetID: fleet}
	svc := &registry.Service{DB: conn, Salt: bytes.Repeat([]byte("s"), 32), Clock: func() time.Time { return flushBase }}
	c := &Counters{}
	return &flushEnv{t: t, conn: conn, svc: svc, actor: actor, fleet: fleet, writer: NewWriter(conn, c), counters: c}
}

// vehicle creates a vehicle and returns its id.
func (e *flushEnv) vehicle(i int) int64 {
	e.t.Helper()
	v, err := e.svc.CreateVehicle(context.Background(), e.actor, registry.VehicleInput{Plate: fmt.Sprintf("FT-%03d", i)})
	if err != nil {
		e.t.Fatalf("create vehicle: %v", err)
	}
	return v.ID
}

// device issues a tracker key and returns its id.
func (e *flushEnv) device() int64 {
	e.t.Helper()
	d, err := e.svc.IssueDevice(context.Background(), e.actor, registry.KindTracker)
	if err != nil {
		e.t.Fatalf("issue device: %v", err)
	}
	return d.ID
}

// pair makes the device the vehicle's paired device.
func (e *flushEnv) pair(vehicleID, deviceID int64) {
	e.t.Helper()
	if _, _, err := e.svc.SetPairing(context.Background(), e.actor, vehicleID, registry.PairingInput{DeviceID: &deviceID}); err != nil {
		e.t.Fatalf("set pairing: %v", err)
	}
}

// pairedVehicle issues a device and pairs it to a new vehicle.
func (e *flushEnv) pairedVehicle(i int) (vehicleID, deviceID int64) {
	vehicleID = e.vehicle(i)
	deviceID = e.device()
	e.pair(vehicleID, deviceID)
	return vehicleID, deviceID
}

// msg builds a message from a device at server time serverTS.
func (e *flushEnv) msg(deviceID int64, serverTS time.Time, lat, lng, heading float64) Message {
	return Message{
		MsgID:    uuid.Must(uuid.NewV7()),
		FleetID:  e.fleet,
		DeviceID: deviceID,
		Position: Position{Lat: lat, Lng: lng, Speed: 42, Heading: heading, Accuracy: 5, DeviceTS: serverTS},
		ServerTS: serverTS,
	}
}

func (e *flushEnv) flush(batch []Message, now time.Time) FlushResult {
	e.t.Helper()
	res, err := e.writer.Flush(context.Background(), batch, now)
	if err != nil {
		e.t.Fatalf("flush: %v", err)
	}
	return res
}

func (e *flushEnv) count(query string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.conn.QueryRow(query, args...).Scan(&n); err != nil {
		e.t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func TestFlushStoresPairedPositions(t *testing.T) {
	e := newFlushEnv(t)
	var vehicles, devices []int64
	for i := 0; i < 3; i++ {
		v, d := e.pairedVehicle(i)
		vehicles, devices = append(vehicles, v), append(devices, d)
	}

	var batch []Message
	for i := 0; i < 10; i++ {
		batch = append(batch, e.msg(devices[i%3], flushBase.Add(time.Duration(i)*time.Second), 41.0+float64(i)/1000, 29.0+float64(i)/1000, 10))
	}
	res := e.flush(batch, flushBase.Add(10*time.Second))

	if len(res.Stored) != 10 {
		t.Fatalf("Stored = %d, want 10", len(res.Stored))
	}
	if got := e.count(`SELECT count(*) FROM positions`); got != 10 {
		t.Fatalf("positions rows = %d, want 10", got)
	}
	for vi, v := range vehicles {
		var latest time.Time
		if err := e.conn.QueryRow(`SELECT server_ts FROM vehicles_latest WHERE vehicle_id = $1`, v).Scan(&latest); err != nil {
			t.Fatalf("vehicles_latest for vehicle %d: %v", vi, err)
		}
		var wantMax time.Time
		for i := 0; i < 10; i++ {
			if i%3 == vi && flushBase.Add(time.Duration(i)*time.Second).After(wantMax) {
				wantMax = flushBase.Add(time.Duration(i) * time.Second)
			}
		}
		if !latest.Equal(wantMax) {
			t.Errorf("vehicle %d latest server_ts = %v, want %v", vi, latest, wantMax)
		}
	}

	m := batch[4]
	var lng, lat float64
	if err := e.conn.QueryRow(`SELECT ST_X(geom::geometry), ST_Y(geom::geometry) FROM positions WHERE msg_id = $1`, m.MsgID).Scan(&lng, &lat); err != nil {
		t.Fatalf("geom of stored message: %v", err)
	}
	if math.Abs(lng-m.Position.Lng) > 1e-9 || math.Abs(lat-m.Position.Lat) > 1e-9 {
		t.Fatalf("geom = (%v, %v), want (%v, %v)", lng, lat, m.Position.Lng, m.Position.Lat)
	}
}

func TestFlushDropsUnpairedRevokedAndFleetMismatch(t *testing.T) {
	e := newFlushEnv(t)
	_, paired := e.pairedVehicle(0)
	unpaired := e.device()
	_, revokedDev := e.pairedVehicle(1)
	if _, err := e.svc.RevokeDevice(context.Background(), e.actor, revokedDev); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	good := e.msg(paired, flushBase, 41, 29, 10)
	noRow := e.msg(unpaired, flushBase, 41, 29, 10)
	revoked := e.msg(revokedDev, flushBase, 41, 29, 10)
	mismatch := e.msg(paired, flushBase, 41, 29, 10)
	mismatch.FleetID = e.fleet + 7

	res := e.flush([]Message{good, noRow, revoked, mismatch}, flushBase)
	if res.Unpaired != 3 || len(res.Stored) != 1 {
		t.Fatalf("Unpaired = %d, Stored = %d; want 3 and 1", res.Unpaired, len(res.Stored))
	}
	if got := e.counters.Unpaired.Load(); got != 3 {
		t.Fatalf("Unpaired counter = %d, want 3", got)
	}
	if got := e.count(`SELECT count(*) FROM positions`); got != 1 {
		t.Fatalf("positions rows = %d, want 1 (only the paired message)", got)
	}
}

func TestFlushDropsStaleAndCountsIt(t *testing.T) {
	e := newFlushEnv(t)
	_, d := e.pairedVehicle(0)
	stale := e.msg(d, flushBase.Add(-61*time.Second), 41, 29, 10)
	edge := e.msg(d, flushBase.Add(-60*time.Second), 41, 29, 10)

	res := e.flush([]Message{stale, edge}, flushBase)
	if res.Stale != 1 || len(res.Stored) != 1 || res.Stored[0].MsgID != edge.MsgID {
		t.Fatalf("Stale = %d, Stored = %v; want 1 stale and only the 60 s message", res.Stale, res.Stored)
	}
	if got := e.counters.Stale.Load(); got != 1 {
		t.Fatalf("Stale counter = %d, want 1", got)
	}
	if got := e.count(`SELECT count(*) FROM positions WHERE msg_id = $1`, stale.MsgID); got != 0 {
		t.Fatalf("stale message stored %d times", got)
	}
	if got := e.count(`SELECT count(*) FROM positions WHERE msg_id = $1`, edge.MsgID); got != 1 {
		t.Fatalf("60 s message stored %d times, want 1", got)
	}
}

func TestFlushRollsBackOnError(t *testing.T) {
	e := newFlushEnv(t)
	v, d := e.pairedVehicle(0)
	first := e.msg(d, flushBase, 41, 29, 10)
	e.flush([]Message{first}, flushBase)
	beforeStored := e.counters.Stored.Load()

	// The second flush has a valid message and one with no partition
	// (December 2026), so the INSERT fails and the whole transaction rolls back.
	newer := e.msg(d, flushBase.Add(time.Second), 42, 30, 20)
	noPartition := e.msg(d, time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), 43, 31, 30)
	batch := []Message{newer, noPartition}
	snapshot := append([]Message(nil), batch...)

	if _, err := e.writer.Flush(context.Background(), batch, flushBase.Add(2*time.Second)); err == nil {
		t.Fatal("flush into a missing partition: want error, got nil")
	}
	for i := range batch {
		if batch[i].MsgID != snapshot[i].MsgID {
			t.Fatalf("batch[%d] changed on error", i)
		}
	}
	var latest time.Time
	if err := e.conn.QueryRow(`SELECT server_ts FROM vehicles_latest WHERE vehicle_id = $1`, v).Scan(&latest); err != nil {
		t.Fatalf("vehicles_latest: %v", err)
	}
	if !latest.Equal(flushBase) {
		t.Fatalf("vehicles_latest moved to %v after a rolled-back flush", latest)
	}
	if got := e.count(`SELECT count(*) FROM positions`); got != 1 {
		t.Fatalf("positions rows = %d after rollback, want 1", got)
	}
	if got := e.counters.Stored.Load(); got != beforeStored {
		t.Fatalf("Stored counter changed on error: %d -> %d", beforeStored, got)
	}
}

func TestFlushNoRowLocksHeldAcrossFlushes(t *testing.T) {
	e := newFlushEnv(t)
	_, d := e.pairedVehicle(0)
	for i := 0; i < 3; i++ {
		at := flushBase.Add(time.Duration(i) * time.Second)
		e.flush([]Message{e.msg(d, at, 41, 29, 10), e.msg(d, at, 41, 29, 11)}, at)

		if got := e.count(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND state = 'idle in transaction'`); got != 0 {
			t.Fatalf("flush %d: %d idle-in-transaction backends", i, got)
		}
		if got := e.count(`SELECT count(*) FROM pg_locks l JOIN pg_class c ON c.oid = l.relation
			WHERE c.relname ~ '^(positions|vehicles_latest)' AND l.pid <> pg_backend_pid()`); got != 0 {
			t.Fatalf("flush %d: %d locks on positions or vehicles_latest from other backends", i, got)
		}
	}
}

func TestPositionsDuplicateIgnored(t *testing.T) {
	e := newFlushEnv(t)
	_, d := e.pairedVehicle(0)
	var batch []Message
	for i := 0; i < 3; i++ {
		batch = append(batch, e.msg(d, flushBase.Add(time.Duration(i)*time.Second), 41, 29, 10))
	}
	e.flush(batch, flushBase.Add(3*time.Second))
	e.flush(batch, flushBase.Add(3*time.Second))
	if got := e.count(`SELECT count(*) FROM positions`); got != 3 {
		t.Fatalf("after two identical flushes: positions = %d, want 3", got)
	}

	dup := e.msg(d, flushBase.Add(5*time.Second), 41, 29, 10)
	e.flush([]Message{dup, dup}, flushBase.Add(5*time.Second))
	if got := e.count(`SELECT count(*) FROM positions WHERE msg_id = $1`, dup.MsgID); got != 1 {
		t.Fatalf("duplicate inside one batch stored %d rows, want 1", got)
	}
}

func TestVehiclesLatestOlderNotApplied(t *testing.T) {
	e := newFlushEnv(t)
	v, d := e.pairedVehicle(0)
	newer := e.msg(d, flushBase.Add(10*time.Second), 41.5, 29.5, 10)
	e.flush([]Message{newer}, flushBase.Add(11*time.Second))

	older := e.msg(d, flushBase.Add(5*time.Second), 40.5, 28.5, 10)
	e.flush([]Message{older}, flushBase.Add(11*time.Second))

	var latest time.Time
	var lat float64
	if err := e.conn.QueryRow(`SELECT server_ts, ST_Y(geom::geometry) FROM vehicles_latest WHERE vehicle_id = $1`, v).Scan(&latest, &lat); err != nil {
		t.Fatalf("vehicles_latest: %v", err)
	}
	if !latest.Equal(newer.ServerTS) || lat != 41.5 {
		t.Fatalf("older flush changed vehicles_latest: server_ts %v lat %v", latest, lat)
	}
}

func TestFlushHeadingSmoothedInVehiclesLatestRawInPositions(t *testing.T) {
	e := newFlushEnv(t)
	v, d := e.pairedVehicle(0)
	first := e.msg(d, flushBase, 41, 29, 90)
	second := e.msg(d, flushBase.Add(time.Second), 41, 29, 110)
	e.flush([]Message{first, second}, flushBase.Add(2*time.Second))

	var raw float64
	if err := e.conn.QueryRow(`SELECT heading_deg FROM positions WHERE msg_id = $1`, second.MsgID).Scan(&raw); err != nil {
		t.Fatalf("positions heading: %v", err)
	}
	if math.Abs(raw-110) > 1e-4 {
		t.Fatalf("positions.heading_deg = %v, want raw 110", raw)
	}

	var smoothed float64
	if err := e.conn.QueryRow(`SELECT heading_deg FROM vehicles_latest WHERE vehicle_id = $1`, v).Scan(&smoothed); err != nil {
		t.Fatalf("vehicles_latest heading: %v", err)
	}
	if math.Abs(smoothed-100) > 1e-4 {
		t.Fatalf("vehicles_latest.heading_deg = %v, want EMA 100", smoothed)
	}
}
