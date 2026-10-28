package ingest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"fleetops/internal/fanout"
	"fleetops/internal/geofence"
)

// hookEnv is a real flush path (writer, publisher, hook) with a manual clock.
// Flushes are driven by calling flush directly, so each call is one batch.
type hookEnv struct {
	t       *testing.T
	fe      *flushEnv
	cons    *Consumer
	m       *Metrics
	reg     *prometheus.Registry
	vehicle int64
	device  int64
	now     time.Time
}

// latLng of the test square fence and of points outside it.
const (
	hookInLat, hookInLng   = 41.0, 29.0
	hookOutLat, hookOutLng = 40.5, 29.0
)

func newHookEnv(t *testing.T) *hookEnv {
	t.Helper()
	fe := newFlushEnv(t)
	vehicle, device := fe.pairedVehicle(0)
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	h := &hookEnv{t: t, fe: fe, m: m, reg: reg, vehicle: vehicle, device: device, now: flushBase}
	pub := &Publisher{Fanout: fanout.Nop{}, Tracker: nopTracker{}, Metrics: m}
	h.cons = NewConsumer(nil, NewBuffer(time.Second, 500, 50, fe.counters), fe.writer, m,
		func() time.Time { return h.now }, pub)
	return h
}

// fence adds a 0.2 by 0.2 degree square around the inside point, with an optional dwell_min.
func (h *hookEnv) fence(name string, dwell *int) {
	h.t.Helper()
	s := &geofence.Store{DB: h.fe.conn}
	ring := [][2]float64{
		{28.9, 40.9}, {29.1, 40.9}, {29.1, 41.1}, {28.9, 41.1}, {28.9, 40.9},
	}
	if _, err := s.Create(context.Background(), h.fe.fleet, geofence.Input{
		Name: name, Kind: geofence.KindPolygon, Ring: ring, DwellMin: dwell,
	}); err != nil {
		h.t.Fatalf("create fence: %v", err)
	}
}

// fix is a paired message of the test vehicle at ts, inside or outside the square.
func (h *hookEnv) fix(ts time.Time, inside bool) Message {
	lat, lng := hookOutLat, hookOutLng
	if inside {
		lat, lng = hookInLat, hookInLng
	}
	m := h.fe.msg(h.device, ts, lat, lng, 90)
	m.Position.Accuracy = 5
	return m
}

// flush runs one batch through the real flush path. The clock is set just after
// the oldest fix, so no fix of the batch is stale (staleAfter is 60 s).
func (h *hookEnv) flush(msgs ...Message) {
	h.t.Helper()
	oldest := msgs[0].ServerTS
	for _, m := range msgs {
		if m.ServerTS.Before(oldest) {
			oldest = m.ServerTS
		}
	}
	h.now = oldest.Add(time.Second)
	h.cons.flush(context.Background(), msgs, nil)
}

// events counts the rows of one kind for the test vehicle.
func (h *hookEnv) events(kind geofence.EventKind) int {
	h.t.Helper()
	return h.fe.count(`SELECT count(*) FROM geofence_events WHERE vehicle_id = $1 AND kind = $2`, h.vehicle, string(kind))
}

// at returns flushBase plus a whole number of seconds.
func at(sec int) time.Time { return flushBase.Add(time.Duration(sec) * time.Second) }

// TestGeofenceHookCrossingOneEnterOneExit: a crossing through real flushes gives
// one enter and one exit, and the fixes are stored.
func TestGeofenceHookCrossingOneEnterOneExit(t *testing.T) {
	h := newHookEnv(t)
	h.fence("crossing depot", nil)

	h.flush(h.fix(at(0), false))
	h.flush(h.fix(at(60), true), h.fix(at(120), true))
	h.flush(h.fix(at(200), false))

	if got := h.events(geofence.EventEnter); got != 1 {
		t.Fatalf("enter rows = %d, want 1", got)
	}
	if got := h.events(geofence.EventExit); got != 1 {
		t.Fatalf("exit rows = %d, want 1", got)
	}
	if got := h.fe.count(`SELECT count(*) FROM positions WHERE vehicle_id = $1`, h.vehicle); got != 4 {
		t.Fatalf("stored positions = %d, want 4", got)
	}
	if got := counterValue(t, h.reg, "fleetops_geofence_events_total", "kind", "enter"); got != 1 {
		t.Fatalf("events_total{enter} = %v, want 1", got)
	}
}

// TestGeofenceHookJitterAtMostOneEvent: boundary jitter inside 30 s gives one
// event, and the flips inside the window are counted as debounced.
func TestGeofenceHookJitterAtMostOneEvent(t *testing.T) {
	h := newHookEnv(t)
	h.fence("jitter depot", nil)

	// Flips every 5 s over 25 s: in, out, in, out, in, out.
	for i := range 6 {
		h.flush(h.fix(at(5*i), i%2 == 0))
	}
	if got := h.events(geofence.EventEnter) + h.events(geofence.EventExit); got != 1 {
		t.Fatalf("events inside the jitter burst = %d, want 1", got)
	}
	if got := counterValue(t, h.reg, "fleetops_geofence_debounced_total", "", ""); got != 3 {
		t.Fatalf("debounced = %v, want 3", got)
	}

	h.flush(h.fix(at(60), false))
	if got := h.events(geofence.EventExit); got != 1 {
		t.Fatalf("exit after the window = %d, want 1", got)
	}
}

// TestGeofenceHookDwellOnFirstFixAtMin: dwell fires on the first inside fix at or
// after dwell_min, not before.
func TestGeofenceHookDwellOnFirstFixAtMin(t *testing.T) {
	h := newHookEnv(t)
	two := 2
	h.fence("dwell depot", &two)

	h.flush(h.fix(at(0), true))
	h.flush(h.fix(at(60), true), h.fix(at(119), true))
	if got := h.events(geofence.EventDwell); got != 0 {
		t.Fatalf("dwell before dwell_min = %d, want 0", got)
	}
	h.flush(h.fix(at(120), true))
	if got := h.events(geofence.EventDwell); got != 1 {
		t.Fatalf("dwell rows = %d, want 1", got)
	}
	var occurred time.Time
	if err := h.fe.conn.QueryRow(`SELECT occurred_at FROM geofence_events WHERE vehicle_id = $1 AND kind = 'dwell'`,
		h.vehicle).Scan(&occurred); err != nil {
		t.Fatalf("dwell row: %v", err)
	}
	if !occurred.Equal(at(120)) {
		t.Fatalf("dwell occurred_at = %v, want %v", occurred, at(120))
	}
}

// TestGeofenceHookMismatchSkipsEvaluator: a result that does not account for
// every message skips the hook, the same point where publish is skipped.
func TestGeofenceHookMismatchSkipsEvaluator(t *testing.T) {
	h := newHookEnv(t)
	h.fence("mismatch depot", nil)
	calls := &countingGeo{}
	h.cons.geo = calls
	h.cons.seams.flushResult = func(res FlushResult) FlushResult {
		res.Stale++
		return res
	}

	h.flush(h.fix(at(0), true))
	if calls.n != 0 {
		t.Fatalf("hook calls on mismatch = %d, want 0", calls.n)
	}
	if got := counterValue(t, h.reg, "fleetops_ingest_flush_failures_total", "reason", "result_mismatch"); got != 1 {
		t.Fatalf("result_mismatch failures = %v, want 1", got)
	}
}

// TestGeofenceHookReplayZeroEvents: a redelivered batch is all Stale. Stale fixes
// are not observed, so the redelivery writes no events.
func TestGeofenceHookReplayZeroEvents(t *testing.T) {
	h := newHookEnv(t)
	h.fence("replay depot", nil)

	batch := []Message{h.fix(at(0), false), h.fix(at(60), true)}
	h.flush(batch...)
	if got := h.events(geofence.EventEnter); got != 1 {
		t.Fatalf("first delivery enter rows = %d, want 1", got)
	}

	// Redeliver the same batch long after the staleness window.
	h.now = at(60).Add(staleAfter + time.Minute)
	h.cons.flush(context.Background(), batch, nil)
	if got := h.events(geofence.EventEnter) + h.events(geofence.EventExit) + h.events(geofence.EventDwell); got != 1 {
		t.Fatalf("events after redelivery = %d, want 1", got)
	}
}

// countingGeo is a hook that records calls and returns no events.
type countingGeo struct{ n int }

func (c *countingGeo) Evaluate(context.Context, []geofence.Observation) (geofence.Result, error) {
	c.n++
	return geofence.Result{Emitted: map[geofence.EventKind]int{}}, nil
}

// flakyGeo fails its first non-empty evaluation, then delegates to the real evaluator.
type flakyGeo struct {
	inner    GeofenceEvaluator
	failures int
}

func (f *flakyGeo) Evaluate(ctx context.Context, obs []geofence.Observation) (geofence.Result, error) {
	if len(obs) > 0 && f.failures > 0 {
		f.failures--
		return geofence.Result{Emitted: map[geofence.EventKind]int{}, Failed: geofence.StageCandidates},
			&geofence.StageError{Stage: geofence.StageCandidates, Err: errors.New("injected")}
	}
	return f.inner.Evaluate(ctx, obs)
}

// TestGeofenceHookFailureAckedAndRetried runs the real NATS loop. The first
// evaluation fails. The batch must still be acked, the positions stored, the
// failure counted, and the pair state left unchanged, so the next batch emits
// the enter.
func TestGeofenceHookFailureAckedAndRetried(t *testing.T) {
	l := newLoopEnv(t, defaultConsumerSpec(), 500)
	ctx := context.Background()
	s := &geofence.Store{DB: l.fe.conn}
	ring := [][2]float64{{28.9, 40.9}, {29.1, 40.9}, {29.1, 41.1}, {28.9, 41.1}, {28.9, 40.9}}
	if _, err := s.Create(ctx, l.fe.fleet, geofence.Input{Name: "flaky depot", Kind: geofence.KindPolygon, Ring: ring}); err != nil {
		t.Fatalf("create fence: %v", err)
	}
	l.cons.geo = &flakyGeo{inner: l.cons.geo, failures: 1}
	stop := l.start()

	first := l.fe.msg(l.device, time.Now().UTC().Add(-2*time.Second), hookInLat, hookInLng, 90)
	first.Position.Accuracy = 5
	l.publish(first)
	l.waitRows(1)
	l.waitAckPending()
	l.waitCounter("fleetops_geofence_evaluation_failures_total", "stage", "candidates", 1)
	if got := l.fe.count(`SELECT count(*) FROM geofence_events WHERE vehicle_id = $1`, l.vehicle); got != 0 {
		t.Fatalf("events after the failed batch = %d, want 0", got)
	}

	second := l.fe.msg(l.device, time.Now().UTC().Add(-time.Second), hookInLat, hookInLng, 90)
	second.Position.Accuracy = 5
	l.publish(second)
	l.waitRows(2)
	l.waitAckPending()
	if err := stop(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := l.fe.count(`SELECT count(*) FROM geofence_events WHERE vehicle_id = $1 AND kind = 'enter'`, l.vehicle); got != 1 {
		t.Fatalf("enter after the retry = %d, want 1", got)
	}
}

// TestGeofenceMetricsSeriesPreCreated: every geofence series exists at zero before any event.
func TestGeofenceMetricsSeriesPreCreated(t *testing.T) {
	reg := prometheus.NewRegistry()
	NewMetrics(reg)
	for _, kind := range geofenceKinds {
		if got := counterValue(t, reg, "fleetops_geofence_events_total", "kind", kind); got != 0 {
			t.Fatalf("events_total{%s} = %v before any event", kind, got)
		}
	}
	for _, stage := range geofenceStages {
		if got := counterValue(t, reg, "fleetops_geofence_evaluation_failures_total", "stage", stage); got != 0 {
			t.Fatalf("failures_total{%s} = %v before any failure", stage, got)
		}
	}
}
