package alerts

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestRaiseETASlipWritesWarningRow: the wrapper writes an eta_slip warning with the payload as given.
func TestRaiseETASlipWritesWarningRow(t *testing.T) {
	f := newFx(t)
	fleet := f.fleet("eta-raise")
	payload := json.RawMessage(`{"job_id":7,"vehicle_id":3,"eta_minutes":75,"threshold_min":15}`)

	tx, err := f.db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	raised, err := RaiseETASlip(context.Background(), tx, fleet, payload)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("RaiseETASlip: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if raised.Kind != KindETASlip || raised.Severity != SeverityWarning || raised.FleetID != fleet {
		t.Fatalf("raised = %+v, want eta_slip warning in fleet %d", raised, fleet)
	}
	var kind, severity, job string
	if err := f.db.QueryRow(
		`SELECT kind, severity, payload->>'job_id' FROM alerts WHERE id = $1`, raised.ID,
	).Scan(&kind, &severity, &job); err != nil {
		t.Fatalf("read alert: %v", err)
	}
	if kind != "eta_slip" || severity != "warning" || job != "7" {
		t.Fatalf("row = (%s, %s, job %s), want (eta_slip, warning, job 7)", kind, severity, job)
	}
}

// TestPublishRaisedEnvelopeForETASlip: the exported publish sends the GF-299 envelope on alerts.<fleet_id>.
func TestPublishRaisedEnvelopeForETASlip(t *testing.T) {
	pub := &fakePublisher{}
	raised := []Raised{{ID: 9, FleetID: 4, Kind: KindETASlip, Severity: SeverityWarning,
		Payload: json.RawMessage(`{"job_id":1}`), RaisedAt: base}}

	PublishRaised(pub, nil, raised)

	if len(pub.subjects) != 1 || pub.subjects[0] != "alerts.4" {
		t.Fatalf("subjects = %v, want [alerts.4]", pub.subjects)
	}
	if !strings.Contains(string(pub.bodies[0]), `"type":"alert.raised"`) ||
		!strings.Contains(string(pub.bodies[0]), `"kind":"eta_slip"`) {
		t.Fatalf("body = %s, want alert.raised eta_slip envelope", pub.bodies[0])
	}
}
