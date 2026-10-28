package hub

import (
	"context"
	"testing"
	"time"

	"github.com/coder/websocket"

	"fleetops/internal/alerts"
)

// readWithin reads one frame and fails if it does not arrive within d (GF-299).
func readWithin(t *testing.T, c *websocket.Conn, d time.Duration) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read within %s: %v", d, err)
	}
	return data
}

// TestAlertRaisedReachesDispatcherNotDriver: an alert encoded by the alerts
// package travels alerts.<fleet_id> over NATS, reaches a dispatcher WS client
// within 5 s, and never reaches a driver client (GF-299, notDriver).
func TestAlertRaisedReachesDispatcherNotDriver(t *testing.T) {
	const fleet = 3010
	r := newRig(t, Options{})
	b := newBus(t, r)
	disp := r.dialAuth(fleet, r.token(9, fleet, "dispatcher"))
	readAuthOK(t, disp, fleet, "dispatcher")
	drv := r.dialAuth(fleet, r.token(42, fleet, "driver"))
	readAuthOK(t, drv, fleet, "driver")
	joined(t, r, 2)

	subj, body, err := alerts.Encode(alerts.Raised{
		ID:       91,
		FleetID:  fleet,
		Kind:     alerts.KindDeviceSilent,
		Severity: alerts.SeverityWarning,
		Payload:  []byte(`{"vehicle_id":7,"last_seen_at":"2026-10-28T09:00:00Z","silent_for_s":700}`),
		RaisedAt: time.Date(2026, 10, 28, 9, 11, 40, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("encode alert: %v", err)
	}
	if subj != "alerts.3010" {
		t.Fatalf("subject = %s, want alerts.3010", subj)
	}
	b.publishNATS(subj, string(body))

	if got := readWithin(t, disp, 5*time.Second); string(got) != string(body) {
		t.Fatalf("dispatcher got %s, want the alert", got)
	}

	// The driver's first frame is its own job update, so the alert never reached it.
	marker := `{"type":"job.updated","fleet_id":3010,"job_id":5,"driver_id":42,"driver_user_id":42}`
	b.publishNATS("jobs.events.3010", marker)
	if got := string(readMsg(t, drv)); got != marker {
		t.Fatalf("driver's first frame %s, want its own job marker (an alert leaked)", got)
	}
}
