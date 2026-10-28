package alerts

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"fleetops/internal/testinfra"
)

// base is the injected clock origin for silence tests (GF-292).
var base = time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)

// fx is one migrated database with helpers for fleets, vehicles, reports and fences.
type fx struct {
	t  *testing.T
	db *sql.DB
	m  *Metrics
}

func newFx(t *testing.T) *fx {
	t.Helper()
	return &fx{t: t, db: testinfra.NewMigratedDB(t), m: NewMetrics(prometheus.NewRegistry())}
}

// fleet inserts a fleet and seeds its three rules.
func (f *fx) fleet(name string) int64 {
	f.t.Helper()
	id := newFleet(f.t, f.db, name)
	tx, err := f.db.Begin()
	if err != nil {
		f.t.Fatalf("begin: %v", err)
	}
	if err := SeedRules(context.Background(), tx, id); err != nil {
		_ = tx.Rollback()
		f.t.Fatalf("SeedRules: %v", err)
	}
	if err := tx.Commit(); err != nil {
		f.t.Fatalf("commit: %v", err)
	}
	return id
}

// vehicle inserts a vehicle with the status, and a paired tracker when paired is set.
func (f *fx) vehicle(fleet int64, status string, paired bool) int64 {
	f.t.Helper()
	var id int64
	if err := f.db.QueryRow(
		`INSERT INTO vehicles (fleet_id, plate, status) VALUES ($1, $2, $3) RETURNING id`,
		fleet, fmt.Sprintf("P-%d", time.Now().UnixNano()), status,
	).Scan(&id); err != nil {
		f.t.Fatalf("insert vehicle: %v", err)
	}
	if paired {
		var dev int64
		if err := f.db.QueryRow(
			`INSERT INTO devices (fleet_id, kind, api_key_hash) VALUES ($1, 'tracker', '\x00') RETURNING id`, fleet,
		).Scan(&dev); err != nil {
			f.t.Fatalf("insert device: %v", err)
		}
		if _, err := f.db.Exec(`UPDATE vehicles SET paired_device_id = $2 WHERE id = $1`, id, dev); err != nil {
			f.t.Fatalf("pair vehicle: %v", err)
		}
	}
	return id
}

// report upserts vehicles_latest with the server_ts of a position (GF-292).
func (f *fx) report(fleet, vehicle int64, serverTS time.Time) {
	f.t.Helper()
	if _, err := f.db.Exec(`
		INSERT INTO vehicles_latest (vehicle_id, fleet_id, geom, device_ts, server_ts)
		VALUES ($1, $2, ST_SetSRID(ST_MakePoint(13.4, 52.5), 4326)::geography, $3, $3)
		ON CONFLICT (vehicle_id) DO UPDATE SET server_ts = EXCLUDED.server_ts`,
		vehicle, fleet, serverTS,
	); err != nil {
		f.t.Fatalf("report: %v", err)
	}
}

// setRule changes one rule row.
func (f *fx) setRule(fleet int64, kind string, enabled bool, params string) {
	f.t.Helper()
	if _, err := f.db.Exec(
		`UPDATE alert_rules SET enabled = $3, params = COALESCE(NULLIF($4, '')::jsonb, params) WHERE fleet_id = $1 AND kind = $2`,
		fleet, kind, enabled, params,
	); err != nil {
		f.t.Fatalf("setRule: %v", err)
	}
}

// fence inserts a circle fence with an optional schedule JSON ("" for NULL).
func (f *fx) fence(fleet int64, schedule string) int64 {
	f.t.Helper()
	var sched any
	if schedule != "" {
		sched = schedule
	}
	var id int64
	if err := f.db.QueryRow(`
		INSERT INTO geofences (fleet_id, name, kind, geom, radius_m, bbox, schedule)
		VALUES ($1, $2, 'circle', ST_SetSRID(ST_MakePoint(13.4, 52.5), 4326)::geography, 100,
		        ST_MakeEnvelope(13.39, 52.49, 13.41, 52.51, 4326), $3::jsonb)
		RETURNING id`,
		fleet, fmt.Sprintf("zone-%d", time.Now().UnixNano()), sched,
	).Scan(&id); err != nil {
		f.t.Fatalf("insert fence: %v", err)
	}
	return id
}

// exit inserts an exit event; created_at is the real insert time, inside the 24 h window.
func (f *fx) exit(fleet, fence, vehicle int64, occurred time.Time) int64 {
	f.t.Helper()
	var id int64
	if err := f.db.QueryRow(`
		INSERT INTO geofence_events (fleet_id, geofence_id, vehicle_id, kind, occurred_at)
		VALUES ($1, $2, $3, 'exit', $4) RETURNING id`,
		fleet, fence, vehicle, occurred,
	).Scan(&id); err != nil {
		f.t.Fatalf("insert exit: %v", err)
	}
	return id
}

// pass runs one evaluation with the injected clock.
func (f *fx) pass(now time.Time) []Raised {
	f.t.Helper()
	ev := &Evaluator{DB: f.db, Clock: func() time.Time { return now }, Metrics: f.m}
	got, err := ev.Pass(context.Background())
	if err != nil {
		f.t.Fatalf("Pass(%s): %v", now, err)
	}
	return got
}

// count returns the alerts of one kind in one fleet.
func (f *fx) count(fleet int64, kind string) int {
	f.t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM alerts WHERE fleet_id = $1 AND kind = $2`, fleet, kind).Scan(&n); err != nil {
		f.t.Fatalf("count: %v", err)
	}
	return n
}

// counterValue reads one counter without the testutil package.
func counterValue(t *testing.T, c prometheus.Counter) float64 {
	t.Helper()
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		t.Fatalf("read counter: %v", err)
	}
	return m.GetCounter().GetValue()
}

// TestEvaluatorSilenceRaisesOnceAndRearms: one alert per silence episode, raised
// on the first pass past the threshold, re-armed by a new report (GF-292).
func TestEvaluatorSilenceRaisesOnceAndRearms(t *testing.T) {
	f := newFx(t)
	fleet := f.fleet("silence")
	v := f.vehicle(fleet, "active", true)
	f.report(fleet, v, base)

	if got := f.pass(base.Add(10 * time.Minute)); len(got) != 0 {
		t.Fatalf("at exactly the threshold raised %d, want 0", len(got))
	}
	first := f.pass(base.Add(10*time.Minute + time.Second))
	if len(first) != 1 || first[0].Kind != KindDeviceSilent || first[0].Severity != SeverityWarning {
		t.Fatalf("first silence = %+v, want one device_silent warning", first)
	}
	var p struct {
		VehicleID  int64  `json:"vehicle_id"`
		LastSeenAt string `json:"last_seen_at"`
		SilentForS int64  `json:"silent_for_s"`
	}
	if err := json.Unmarshal(first[0].Payload, &p); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if p.VehicleID != v || p.SilentForS != 601 || !strings.HasPrefix(p.LastSeenAt, "2026-10-28T09:00:00") {
		t.Fatalf("payload = %+v, want vehicle %d, 601 s, last seen 09:00", p, v)
	}

	if got := f.pass(base.Add(30 * time.Minute)); len(got) != 0 {
		t.Fatalf("same episode raised again: %d", len(got))
	}
	if n := f.count(fleet, KindDeviceSilent); n != 1 {
		t.Fatalf("alerts after same episode = %d, want 1", n)
	}

	reportAt := base.Add(31 * time.Minute)
	f.report(fleet, v, reportAt)
	if got := f.pass(reportAt.Add(5 * time.Minute)); len(got) != 0 {
		t.Fatalf("fresh report raised: %d", len(got))
	}
	second := f.pass(reportAt.Add(10*time.Minute + time.Second))
	if len(second) != 1 {
		t.Fatalf("second silence raised %d, want 1", len(second))
	}
	if n := f.count(fleet, KindDeviceSilent); n != 2 {
		t.Fatalf("alerts after re-arm = %d, want 2", n)
	}
}

// TestEvaluatorSilenceExclusions: a vehicle that is not active, not paired, or
// has no latest row never alerts. A disabled rule raises nothing, and enabling
// it again raises for a silence that is still open (GF-297).
func TestEvaluatorSilenceExclusions(t *testing.T) {
	f := newFx(t)
	fleet := f.fleet("excl")
	maint := f.vehicle(fleet, "maintenance", true)
	retired := f.vehicle(fleet, "retired", true)
	unpaired := f.vehicle(fleet, "active", false)
	// Active and paired, but never reported: no vehicles_latest row.
	f.vehicle(fleet, "active", true)
	f.report(fleet, maint, base)
	f.report(fleet, retired, base)
	f.report(fleet, unpaired, base)

	if got := f.pass(base.Add(time.Hour)); len(got) != 0 {
		t.Fatalf("excluded vehicles raised %d alerts", len(got))
	}

	live := f.vehicle(fleet, "active", true)
	f.report(fleet, live, base)
	f.setRule(fleet, KindDeviceSilent, false, "")
	if got := f.pass(base.Add(time.Hour)); len(got) != 0 {
		t.Fatalf("disabled rule raised %d", len(got))
	}
	f.setRule(fleet, KindDeviceSilent, true, "")
	if got := f.pass(base.Add(time.Hour)); len(got) != 1 {
		t.Fatalf("re-enabled rule raised %d for an open silence, want 1", len(got))
	}
}

// TestEvaluatorThresholdFromRule: the rule's threshold_s is read per row (GF-297).
func TestEvaluatorThresholdFromRule(t *testing.T) {
	f := newFx(t)
	fleet := f.fleet("threshold")
	v := f.vehicle(fleet, "active", true)
	f.report(fleet, v, base)
	f.setRule(fleet, KindDeviceSilent, true, `{"threshold_s":300}`)
	if got := f.pass(base.Add(4 * time.Minute)); len(got) != 0 {
		t.Fatalf("below 300 s raised %d", len(got))
	}
	if got := f.pass(base.Add(5*time.Minute + time.Second)); len(got) != 1 {
		t.Fatalf("past 300 s raised %d, want 1", len(got))
	}
}

// TestEvaluatorExitOutsideSchedule: an exit outside the window is one critical
// alert across two passes. An exit inside the window and a NULL schedule raise nothing (GF-293, GF-296).
func TestEvaluatorExitOutsideSchedule(t *testing.T) {
	f := newFx(t)
	fleet := f.fleet("exit")
	v := f.vehicle(fleet, "active", true)
	// Monday to Friday, 08:00 to 18:00 UTC.
	office := f.fence(fleet, `{"days":[1,2,3,4,5],"from":"08:00","to":"18:00","tz":"UTC"}`)
	anytime := f.fence(fleet, "")

	outside := time.Date(2026, 10, 28, 20, 0, 0, 0, time.UTC) // Wednesday evening
	inside := time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)  // Wednesday morning
	f.exit(fleet, office, v, inside)
	f.exit(fleet, anytime, v, outside)
	eid := f.exit(fleet, office, v, outside)

	now := time.Now()
	first := f.pass(now)
	if len(first) != 1 {
		t.Fatalf("first pass raised %d, want 1", len(first))
	}
	if first[0].Kind != KindGeofenceExit || first[0].Severity != SeverityCritical {
		t.Fatalf("alert = %+v, want critical exit", first[0])
	}
	var p struct {
		EventID    int64  `json:"event_id"`
		GeofenceID int64  `json:"geofence_id"`
		VehicleID  int64  `json:"vehicle_id"`
		OccurredAt string `json:"occurred_at"`
	}
	if err := json.Unmarshal(first[0].Payload, &p); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if p.EventID != eid || p.GeofenceID != office || p.VehicleID != v || !strings.HasPrefix(p.OccurredAt, "2026-10-28T20:00:00") {
		t.Fatalf("payload = %+v, want event %d on fence %d", p, eid, office)
	}
	if strings.Contains(string(first[0].Payload), "lat") || strings.Contains(string(first[0].Payload), "geom") {
		t.Fatalf("payload carries coordinates: %s", first[0].Payload)
	}

	if got := f.pass(now.Add(time.Minute)); len(got) != 0 {
		t.Fatalf("second pass raised %d, want 0", len(got))
	}
	if n := f.count(fleet, KindGeofenceExit); n != 1 {
		t.Fatalf("exit alerts = %d, want 1", n)
	}
}

// TestEvaluatorCrossFleetIsolation: a rule in one fleet does not touch another.
func TestEvaluatorCrossFleetIsolation(t *testing.T) {
	f := newFx(t)
	a := f.fleet("iso-a")
	b := f.fleet("iso-b")
	va := f.vehicle(a, "active", true)
	vb := f.vehicle(b, "active", true)
	f.report(a, va, base)
	f.report(b, vb, base)
	f.setRule(b, KindDeviceSilent, false, "")

	got := f.pass(base.Add(time.Hour))
	if len(got) != 1 || got[0].FleetID != a {
		t.Fatalf("raised = %+v, want one alert for fleet %d only", got, a)
	}
	if f.count(b, KindDeviceSilent) != 0 {
		t.Fatal("disabled fleet raised an alert")
	}
}

// TestEvaluatorLockSkipsPass: a pass that cannot take the advisory lock does nothing (GF-291).
func TestEvaluatorLockSkipsPass(t *testing.T) {
	f := newFx(t)
	fleet := f.fleet("lock")
	v := f.vehicle(fleet, "active", true)
	f.report(fleet, v, base)

	holder, err := f.db.Begin()
	if err != nil {
		t.Fatalf("begin holder: %v", err)
	}
	if _, err := holder.Exec(`SELECT pg_advisory_xact_lock($1)`, passLockKey); err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	if got := f.pass(base.Add(time.Hour)); len(got) != 0 {
		t.Fatalf("locked pass raised %d", len(got))
	}
	if n := counterValue(t, f.m.PassesSkipped); n != 1 {
		t.Fatalf("passes skipped = %v, want 1", n)
	}
	if err := holder.Commit(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got := f.pass(base.Add(time.Hour)); len(got) != 1 {
		t.Fatalf("after release raised %d, want 1", len(got))
	}
}
