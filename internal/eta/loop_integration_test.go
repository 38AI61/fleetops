package eta

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"fleetops/internal/testinfra"
)

// Fixed geography for the loop tests. The vehicle is south-west of pickup, pickup of dropoff.
var (
	vehiclePt = Point{Lat: 52.520000, Lng: 13.400000}
	pickupPt  = Point{Lat: 52.530000, Lng: 13.410000}
	dropoffPt = Point{Lat: 52.540000, Lng: 13.420000}
	loopBase  = time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
)

// osrmStub is an httptest OSRM that answers every call with one duration and records the paths.
type osrmStub struct {
	srv     *httptest.Server
	calls   atomic.Int64
	seconds atomic.Int64
	mu      sync.Mutex
	paths   []string
}

func newOSRMStub(t *testing.T, seconds int64) *osrmStub {
	t.Helper()
	s := &osrmStub{}
	s.seconds.Store(seconds)
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls.Add(1)
		s.mu.Lock()
		s.paths = append(s.paths, r.URL.Path)
		s.mu.Unlock()
		_, _ = fmt.Fprintf(w, `{"code":"Ok","routes":[{"duration":%d}]}`, s.seconds.Load())
	}))
	t.Cleanup(s.srv.Close)
	return s
}

// fixedRouter returns a fixed minute count, to test the clamp at write without HTTP.
type fixedRouter struct{ minutes int }

func (f fixedRouter) Route(context.Context, Point, Point) (int, error) { return f.minutes, nil }

// loopFx is one migrated database with the helpers the loop tests share.
type loopFx struct {
	t     *testing.T
	db    *sql.DB
	fleet int64
	actor int64
	now   time.Time
}

func newLoopFx(t *testing.T) *loopFx {
	t.Helper()
	f := &loopFx{t: t, db: testinfra.NewMigratedDB(t), now: loopBase}
	if err := f.db.QueryRow(`INSERT INTO fleets (name, timezone) VALUES ('eta', 'UTC') RETURNING id`).Scan(&f.fleet); err != nil {
		t.Fatalf("insert fleet: %v", err)
	}
	if err := f.db.QueryRow(
		`INSERT INTO users (fleet_id, email, password_hash, role) VALUES ($1, 'eta-admin@eta.example', 'x', 'admin') RETURNING id`,
		f.fleet).Scan(&f.actor); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return f
}

// loop returns a loop over the given router, with the fixture clock and a private registry.
func (f *loopFx) loop(r Router) *Loop {
	return &Loop{
		DB:      f.db,
		Router:  r,
		Limiter: NewLimiter(AttemptWindow),
		Clock:   func() time.Time { return f.now },
		Metrics: NewMetrics(prometheus.NewRegistry()),
	}
}

// vehicle inserts an active vehicle and, when at is non-nil, its latest position.
func (f *loopFx) vehicle(at *Point) int64 {
	f.t.Helper()
	var id int64
	if err := f.db.QueryRow(`INSERT INTO vehicles (fleet_id, plate) VALUES ($1, $2) RETURNING id`,
		f.fleet, fmt.Sprintf("ETA-%d", time.Now().UnixNano())).Scan(&id); err != nil {
		f.t.Fatalf("insert vehicle: %v", err)
	}
	if at != nil {
		if _, err := f.db.Exec(`
			INSERT INTO vehicles_latest (vehicle_id, fleet_id, geom, device_ts, server_ts)
			VALUES ($1, $2, ST_SetSRID(ST_MakePoint($3, $4), 4326)::geography, $5, $5)`,
			id, f.fleet, at.Lng, at.Lat, f.now); err != nil {
			f.t.Fatalf("insert vehicles_latest: %v", err)
		}
	}
	return id
}

// driver inserts a driver in the fleet.
func (f *loopFx) driver() int64 {
	f.t.Helper()
	var id int64
	// The license hash is unique per fleet, so each driver gets its own value.
	if err := f.db.QueryRow(`INSERT INTO drivers (fleet_id, name, license_no_hash) VALUES ($1, 'd', $2) RETURNING id`,
		f.fleet, []byte(fmt.Sprintf("lic-%d", time.Now().UnixNano()))).Scan(&id); err != nil {
		f.t.Fatalf("insert driver: %v", err)
	}
	return id
}

// job inserts a job in state with the vehicle and driver (0 means NULL), with the actor
// set in the same transaction so the creation trigger can write its event.
func (f *loopFx) job(state string, vehicle, driver int64) int64 {
	f.t.Helper()
	tx, err := f.db.BeginTx(context.Background(), nil)
	if err != nil {
		f.t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`SELECT set_config('app.actor_id', $1::text, true)`, f.actor); err != nil {
		f.t.Fatalf("set actor: %v", err)
	}
	var id int64
	if err := tx.QueryRow(`
		INSERT INTO jobs (fleet_id, state, vehicle_id, driver_id, pickup, dropoff)
		VALUES ($1, $2, NULLIF($3, 0), NULLIF($4, 0),
		        ST_SetSRID(ST_MakePoint($5, $6), 4326)::geography,
		        ST_SetSRID(ST_MakePoint($7, $8), 4326)::geography)
		RETURNING id`,
		f.fleet, state, vehicle, driver,
		pickupPt.Lng, pickupPt.Lat, dropoffPt.Lng, dropoffPt.Lat).Scan(&id); err != nil {
		f.t.Fatalf("insert job: %v", err)
	}
	if err := tx.Commit(); err != nil {
		f.t.Fatalf("commit: %v", err)
	}
	return id
}

// eta reads jobs.eta_minutes. ok is false when it is NULL.
func (f *loopFx) eta(job int64) (int, bool) {
	f.t.Helper()
	var v sql.NullInt64
	if err := f.db.QueryRow(`SELECT eta_minutes FROM jobs WHERE id = $1`, job).Scan(&v); err != nil {
		f.t.Fatalf("read eta: %v", err)
	}
	return int(v.Int64), v.Valid
}

// events counts job_events rows for the job. The ETA write must add none (GF-309).
func (f *loopFx) events(job int64) int {
	f.t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM job_events WHERE job_id = $1`, job).Scan(&n); err != nil {
		f.t.Fatalf("count events: %v", err)
	}
	return n
}

// counter reads one pre-seeded counter series.
func counter(t *testing.T, vec *prometheus.CounterVec, label string) float64 {
	t.Helper()
	var m dto.Metric
	if err := vec.WithLabelValues(label).Write(&m); err != nil {
		t.Fatalf("read counter %s: %v", label, err)
	}
	return m.GetCounter().GetValue()
}

// TestLoopWritesETAAndHoldsWindow: the ETA is written within one pass, a pass inside 5 min
// makes no call, and one more call happens after the window (GF-307).
func TestLoopWritesETAAndHoldsWindow(t *testing.T) {
	f := newLoopFx(t)
	stub := newOSRMStub(t, 754)
	lp := f.loop(NewOSRMRouter(stub.srv.URL))

	v := f.vehicle(&vehiclePt)
	job := f.job("assigned", v, f.driver())
	before := f.events(job)

	if err := lp.Pass(context.Background()); err != nil {
		t.Fatalf("pass 1: %v", err)
	}
	if got, ok := f.eta(job); !ok || got != 13 {
		t.Fatalf("eta after pass 1 = %d (valid %t), want 13", got, ok)
	}
	if n := stub.calls.Load(); n != 1 {
		t.Fatalf("calls after pass 1 = %d, want 1", n)
	}
	if after := f.events(job); after != before {
		t.Fatalf("ETA write added %d job_events rows, want none (GF-309)", after-before)
	}

	// Inside the window: the router would now say 20 min, but no call is made.
	stub.seconds.Store(1200)
	f.now = loopBase.Add(4 * time.Minute)
	if err := lp.Pass(context.Background()); err != nil {
		t.Fatalf("pass 2: %v", err)
	}
	if n := stub.calls.Load(); n != 1 {
		t.Fatalf("calls inside the window = %d, want still 1", n)
	}
	if got, _ := f.eta(job); got != 13 {
		t.Fatalf("eta inside the window = %d, want 13", got)
	}

	// After the window: one more call and the new value is written.
	f.now = loopBase.Add(AttemptWindow)
	if err := lp.Pass(context.Background()); err != nil {
		t.Fatalf("pass 3: %v", err)
	}
	if n := stub.calls.Load(); n != 2 {
		t.Fatalf("calls after the window = %d, want 2", n)
	}
	if got, _ := f.eta(job); got != 20 {
		t.Fatalf("eta after the window = %d, want 20", got)
	}
}

// TestLoopTargetFollowsState: assigned and accepted route to pickup, picked_up to dropoff (D-35, GF-316).
func TestLoopTargetFollowsState(t *testing.T) {
	f := newLoopFx(t)
	stub := newOSRMStub(t, 60)
	lp := f.loop(NewOSRMRouter(stub.srv.URL))

	v := f.vehicle(&vehiclePt)
	d := f.driver()
	f.job("assigned", v, d)
	f.job("picked_up", v, d)

	if err := lp.Pass(context.Background()); err != nil {
		t.Fatalf("pass: %v", err)
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.paths) != 2 {
		t.Fatalf("calls = %d, want 2", len(stub.paths))
	}
	pickupLeg := fmt.Sprintf("/route/v1/driving/%.6f,%.6f;%.6f,%.6f", vehiclePt.Lng, vehiclePt.Lat, pickupPt.Lng, pickupPt.Lat)
	dropoffLeg := fmt.Sprintf("/route/v1/driving/%.6f,%.6f;%.6f,%.6f", vehiclePt.Lng, vehiclePt.Lat, dropoffPt.Lng, dropoffPt.Lat)
	if stub.paths[0] != pickupLeg {
		t.Errorf("assigned path = %q, want pickup leg %q", stub.paths[0], pickupLeg)
	}
	if stub.paths[1] != dropoffLeg {
		t.Errorf("picked_up path = %q, want dropoff leg %q", stub.paths[1], dropoffLeg)
	}
}

// TestLoopAssignedJobComputedWithinOneTick: a job assigned after a pass gets its ETA on the next pass.
func TestLoopAssignedJobComputedWithinOneTick(t *testing.T) {
	f := newLoopFx(t)
	stub := newOSRMStub(t, 754)
	lp := f.loop(NewOSRMRouter(stub.srv.URL))

	v := f.vehicle(&vehiclePt)
	d := f.driver()
	job := f.job("created", 0, 0)

	if err := lp.Pass(context.Background()); err != nil {
		t.Fatalf("pass before assign: %v", err)
	}
	if n := stub.calls.Load(); n != 0 {
		t.Fatalf("calls before assign = %d, want 0", n)
	}

	tx, err := f.db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(`SELECT set_config('app.actor_id', $1::text, true)`, f.actor); err != nil {
		t.Fatalf("set actor: %v", err)
	}
	if _, err := tx.Exec(`UPDATE jobs SET state = 'assigned', vehicle_id = $2, driver_id = $3 WHERE id = $1`, job, v, d); err != nil {
		_ = tx.Rollback()
		t.Fatalf("assign: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit assign: %v", err)
	}

	if err := lp.Pass(context.Background()); err != nil {
		t.Fatalf("pass after assign: %v", err)
	}
	if got, ok := f.eta(job); !ok || got != 13 {
		t.Fatalf("eta after assign = %d (valid %t), want 13", got, ok)
	}
}

// TestLoopClampsAtWrite: an ETA above 1440 writes 1440, through the router and through the loop guard.
func TestLoopClampsAtWrite(t *testing.T) {
	f := newLoopFx(t)
	v := f.vehicle(&vehiclePt)
	job := f.job("en_route", v, f.driver())

	// Router path: 90000 s is 1500 min and clamps to 1440 in the router.
	stub := newOSRMStub(t, 90000)
	if err := f.loop(NewOSRMRouter(stub.srv.URL)).Pass(context.Background()); err != nil {
		t.Fatalf("router clamp pass: %v", err)
	}
	if got, _ := f.eta(job); got != 1440 {
		t.Fatalf("eta from 90000 s = %d, want 1440", got)
	}

	// Loop guard: a router that returns 1500 still writes 1440. The CHECK allows 0..1440.
	f.now = loopBase.Add(AttemptWindow)
	if err := f.loop(fixedRouter{minutes: 1500}).Pass(context.Background()); err != nil {
		t.Fatalf("guard clamp pass: %v", err)
	}
	if got, _ := f.eta(job); got != 1440 {
		t.Fatalf("eta from a raw 1500 = %d, want 1440", got)
	}
}

// TestLoopMissingPositionSkipsWithoutCall: a vehicle with no latest position makes no call and writes nothing.
func TestLoopMissingPositionSkipsWithoutCall(t *testing.T) {
	f := newLoopFx(t)
	stub := newOSRMStub(t, 754)
	lp := f.loop(NewOSRMRouter(stub.srv.URL))

	v := f.vehicle(nil)
	job := f.job("assigned", v, f.driver())

	if err := lp.Pass(context.Background()); err != nil {
		t.Fatalf("pass: %v", err)
	}
	if n := stub.calls.Load(); n != 0 {
		t.Fatalf("calls = %d, want 0", n)
	}
	if _, ok := f.eta(job); ok {
		t.Error("eta written without a position")
	}
	if got := counter(t, lp.Metrics.Skipped, reasonNoPosition); got != 1 {
		t.Errorf("no_position skips = %v, want 1", got)
	}
}

// TestLoopLockContentionSkipsPass: a pass that cannot take the advisory lock does nothing and is counted (GF-306).
func TestLoopLockContentionSkipsPass(t *testing.T) {
	f := newLoopFx(t)
	stub := newOSRMStub(t, 754)
	lp := f.loop(NewOSRMRouter(stub.srv.URL))

	v := f.vehicle(&vehiclePt)
	job := f.job("assigned", v, f.driver())

	holder, err := f.db.Begin()
	if err != nil {
		t.Fatalf("begin holder: %v", err)
	}
	defer func() { _ = holder.Rollback() }()
	if _, err := holder.Exec(`SELECT pg_advisory_xact_lock($1)`, passLockKey); err != nil {
		t.Fatalf("hold lock: %v", err)
	}

	if err := lp.Pass(context.Background()); err != nil {
		t.Fatalf("pass under contention: %v", err)
	}
	if n := stub.calls.Load(); n != 0 {
		t.Errorf("calls under contention = %d, want 0", n)
	}
	if _, ok := f.eta(job); ok {
		t.Error("eta written under contention")
	}
	var m dto.Metric
	if err := lp.Metrics.PassesSkipped.Write(&m); err != nil {
		t.Fatalf("read passes skipped: %v", err)
	}
	if got := m.GetCounter().GetValue(); got != 1 {
		t.Errorf("passes skipped = %v, want 1", got)
	}
}
