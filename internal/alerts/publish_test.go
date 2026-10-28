package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// fakePublisher records each Publish call and returns err when set.
type fakePublisher struct {
	subjects []string
	bodies   [][]byte
	err      error
}

func (p *fakePublisher) Publish(subject string, data []byte) error {
	p.subjects = append(p.subjects, subject)
	p.bodies = append(p.bodies, data)
	return p.err
}

// TestPublishAfterCommit: each raised alert is published once, after commit, on
// alerts.<fleet_id> with the GF-299 envelope (GF-4, GF-299).
func TestPublishAfterCommit(t *testing.T) {
	f := newFx(t)
	fleet := f.fleet("publish")
	v := f.vehicle(fleet, "active", true)
	f.report(fleet, v, base)
	pub := &fakePublisher{}
	ev := &Evaluator{DB: f.db, Clock: func() time.Time { return base.Add(time.Hour) }, Metrics: f.m, Publisher: pub}

	raised, err := ev.Pass(context.Background())
	if err != nil || len(raised) != 1 {
		t.Fatalf("Pass = %d alerts, %v; want 1", len(raised), err)
	}
	if len(pub.subjects) != 1 || pub.subjects[0] != Subject(fleet) {
		t.Fatalf("subjects = %v, want [%s]", pub.subjects, Subject(fleet))
	}
	var env struct {
		Type     string          `json:"type"`
		FleetID  int64           `json:"fleet_id"`
		AlertID  int64           `json:"alert_id"`
		Kind     string          `json:"kind"`
		Severity string          `json:"severity"`
		RaisedAt string          `json:"raised_at"`
		Payload  json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(pub.bodies[0], &env); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if env.Type != "alert.raised" || env.FleetID != fleet || env.AlertID != raised[0].ID ||
		env.Kind != KindDeviceSilent || env.Severity != SeverityWarning || env.RaisedAt == "" || len(env.Payload) == 0 {
		t.Fatalf("envelope = %+v, want alert %d for fleet %d", env, raised[0].ID, fleet)
	}
	if f.count(fleet, KindDeviceSilent) != 1 {
		t.Fatal("published alert is not in the table")
	}
}

// TestPublishFailureKeepsRow: a failing publish is logged and counted, the alert
// row stays, and the pass still returns the alert (GF-299, GF-4).
func TestPublishFailureKeepsRow(t *testing.T) {
	f := newFx(t)
	fleet := f.fleet("publish-fail")
	v := f.vehicle(fleet, "active", true)
	f.report(fleet, v, base)
	pub := &fakePublisher{err: errors.New("nats down")}
	ev := &Evaluator{DB: f.db, Clock: func() time.Time { return base.Add(time.Hour) }, Metrics: f.m, Publisher: pub}

	raised, err := ev.Pass(context.Background())
	if err != nil || len(raised) != 1 {
		t.Fatalf("Pass = %d alerts, %v; want 1 and no error", len(raised), err)
	}
	if f.count(fleet, KindDeviceSilent) != 1 {
		t.Fatal("publish failure removed the alert row")
	}
	if n := counterValue(t, f.m.Failures.WithLabelValues(stagePublish)); n != 1 {
		t.Fatalf("publish failures = %v, want 1", n)
	}
}
