package eta

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"fleetops/internal/alerts"
)

// seedRules seeds the fleet's three alert rules (GF-290) and sets the eta_slip flag.
func (f *loopFx) seedRules(etaSlipEnabled bool) {
	f.t.Helper()
	tx, err := f.db.Begin()
	if err != nil {
		f.t.Fatalf("begin: %v", err)
	}
	if err := alerts.SeedRules(context.Background(), tx, f.fleet); err != nil {
		_ = tx.Rollback()
		f.t.Fatalf("SeedRules: %v", err)
	}
	if err := tx.Commit(); err != nil {
		f.t.Fatalf("commit: %v", err)
	}
	if _, err := f.db.Exec(`UPDATE alert_rules SET enabled = $2 WHERE fleet_id = $1 AND kind = 'eta_slip'`,
		f.fleet, etaSlipEnabled); err != nil {
		f.t.Fatalf("set eta_slip enabled: %v", err)
	}
}

// setWindowEnd sets the job's window_end. It does not change state or assignment, so the actor is not needed.
func (f *loopFx) setWindowEnd(job int64, end time.Time) {
	f.t.Helper()
	if _, err := f.db.Exec(`UPDATE jobs SET window_end = $2 WHERE id = $1`, job, end); err != nil {
		f.t.Fatalf("set window_end: %v", err)
	}
}

// slipRows counts eta_slip alerts for the fleet.
func (f *loopFx) slipRows() int {
	f.t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM alerts WHERE fleet_id = $1 AND kind = 'eta_slip'`, f.fleet).Scan(&n); err != nil {
		f.t.Fatalf("count eta_slip: %v", err)
	}
	return n
}

// recordPublisher keeps every published subject and body, for the publish-after-commit check.
type recordPublisher struct {
	mu       sync.Mutex
	subjects []string
	bodies   [][]byte
}

func (p *recordPublisher) Publish(subject string, data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.subjects = append(p.subjects, subject)
	p.bodies = append(p.bodies, data)
	return nil
}

// TestSlipAlertFiresOnDelayedJob is the required slip test: window ended an hour ago and OSRM says 3000 s.
// Exactly one eta_slip warning, with the GF-312 payload, and one publish after commit.
func TestSlipAlertFiresOnDelayedJob(t *testing.T) {
	f := newLoopFx(t)
	f.seedRules(true)
	stub := newOSRMStub(t, 3000)
	pub := &recordPublisher{}
	lp := f.loop(NewOSRMRouter(stub.srv.URL))
	lp.Publisher = pub

	v := f.vehicle(&vehiclePt)
	job := f.job("assigned", v, f.driver())
	windowEnd := loopBase.Add(-time.Hour).UTC().Truncate(time.Second)
	f.setWindowEnd(job, windowEnd)

	if err := lp.Pass(context.Background()); err != nil {
		t.Fatalf("pass: %v", err)
	}
	if n := f.slipRows(); n != 1 {
		t.Fatalf("eta_slip rows = %d, want 1", n)
	}

	var severity string
	var payload []byte
	if err := f.db.QueryRow(`SELECT severity, payload FROM alerts WHERE fleet_id = $1 AND kind = 'eta_slip'`, f.fleet).Scan(&severity, &payload); err != nil {
		t.Fatalf("read alert: %v", err)
	}
	if severity != "warning" {
		t.Errorf("severity = %q, want warning", severity)
	}
	var got struct {
		JobID        int64  `json:"job_id"`
		VehicleID    int64  `json:"vehicle_id"`
		ETAMinutes   int    `json:"eta_minutes"`
		ProjectedAt  string `json:"projected_at"`
		WindowEnd    string `json:"window_end"`
		ThresholdMin int    `json:"threshold_min"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if got.JobID != job || got.VehicleID != v || got.ETAMinutes != 50 || got.ThresholdMin != 15 {
		t.Errorf("payload = %+v, want job %d vehicle %d eta 50 threshold 15", got, job, v)
	}
	if want := windowEnd.Format(time.RFC3339); got.WindowEnd != want {
		t.Errorf("window_end = %q, want %q", got.WindowEnd, want)
	}
	wantProjected := loopBase.Add(50 * time.Minute).UTC().Format(time.RFC3339)
	if got.ProjectedAt != wantProjected {
		t.Errorf("projected_at = %q, want %q", got.ProjectedAt, wantProjected)
	}

	// The publish happens once, after commit, on the fleet subject with the alert.raised type.
	pub.mu.Lock()
	defer pub.mu.Unlock()
	if len(pub.subjects) != 1 || pub.subjects[0] != alerts.Subject(f.fleet) {
		t.Fatalf("published subjects = %v, want one %s", pub.subjects, alerts.Subject(f.fleet))
	}
	var env struct {
		Type string `json:"type"`
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(pub.bodies[0], &env); err != nil || env.Type != "alert.raised" || env.Kind != "eta_slip" {
		t.Errorf("envelope = %s, want alert.raised eta_slip", pub.bodies[0])
	}
}

// TestSlipDedupOnePerWindow: a second fresh pass on the same window raises nothing (GF-311).
func TestSlipDedupOnePerWindow(t *testing.T) {
	f := newLoopFx(t)
	f.seedRules(true)
	stub := newOSRMStub(t, 3000)
	lp := f.loop(NewOSRMRouter(stub.srv.URL))

	v := f.vehicle(&vehiclePt)
	job := f.job("assigned", v, f.driver())
	f.setWindowEnd(job, loopBase.Add(-time.Hour))

	if err := lp.Pass(context.Background()); err != nil {
		t.Fatalf("pass 1: %v", err)
	}
	f.now = loopBase.Add(AttemptWindow) // the limiter allows a fresh computation
	if err := lp.Pass(context.Background()); err != nil {
		t.Fatalf("pass 2: %v", err)
	}
	if n := stub.calls.Load(); n != 2 {
		t.Fatalf("calls = %d, want 2 fresh computations", n)
	}
	if n := f.slipRows(); n != 1 {
		t.Fatalf("eta_slip rows after two passes on one window = %d, want 1", n)
	}
}

// TestSlipNewWindowAlertsAgain: a re-planned window is a new key, so it alerts again (GF-311).
func TestSlipNewWindowAlertsAgain(t *testing.T) {
	f := newLoopFx(t)
	f.seedRules(true)
	stub := newOSRMStub(t, 3000)
	lp := f.loop(NewOSRMRouter(stub.srv.URL))

	v := f.vehicle(&vehiclePt)
	job := f.job("assigned", v, f.driver())
	f.setWindowEnd(job, loopBase.Add(-time.Hour))
	if err := lp.Pass(context.Background()); err != nil {
		t.Fatalf("pass 1: %v", err)
	}

	f.setWindowEnd(job, loopBase.Add(-30*time.Minute))
	f.now = loopBase.Add(AttemptWindow)
	if err := lp.Pass(context.Background()); err != nil {
		t.Fatalf("pass 2: %v", err)
	}
	if n := f.slipRows(); n != 2 {
		t.Fatalf("eta_slip rows across two windows = %d, want 2", n)
	}
}

// TestSlipWindowlessJobIsSilent: no window, huge ETA, no row (GF-310).
func TestSlipWindowlessJobIsSilent(t *testing.T) {
	f := newLoopFx(t)
	f.seedRules(true)
	stub := newOSRMStub(t, 86400)
	lp := f.loop(NewOSRMRouter(stub.srv.URL))

	v := f.vehicle(&vehiclePt)
	job := f.job("assigned", v, f.driver())

	if err := lp.Pass(context.Background()); err != nil {
		t.Fatalf("pass: %v", err)
	}
	if got, _ := f.eta(job); got != 1440 {
		t.Errorf("eta = %d, want the clamp 1440", got)
	}
	if n := f.slipRows(); n != 0 {
		t.Errorf("eta_slip rows for a windowless job = %d, want 0", n)
	}
}

// TestSlipRuleDisabledIsSilentButETAWritten: the rule gates the alert, not the ETA (GF-313).
func TestSlipRuleDisabledIsSilentButETAWritten(t *testing.T) {
	f := newLoopFx(t)
	f.seedRules(false)
	stub := newOSRMStub(t, 3000)
	lp := f.loop(NewOSRMRouter(stub.srv.URL))

	v := f.vehicle(&vehiclePt)
	job := f.job("assigned", v, f.driver())
	f.setWindowEnd(job, loopBase.Add(-time.Hour))

	if err := lp.Pass(context.Background()); err != nil {
		t.Fatalf("pass: %v", err)
	}
	if got, ok := f.eta(job); !ok || got != 50 {
		t.Errorf("eta = %d (valid %t), want 50 written with the rule off", got, ok)
	}
	if n := f.slipRows(); n != 0 {
		t.Errorf("eta_slip rows with the rule disabled = %d, want 0", n)
	}
}
