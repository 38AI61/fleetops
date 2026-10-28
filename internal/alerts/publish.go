package alerts

import (
	"encoding/json"
	"fmt"
	"log"
	"time"
)

// Publisher is the core NATS seam (GF-4, GF-299). *nats.Conn satisfies it.
type Publisher interface {
	Publish(subject string, data []byte) error
}

// eventType is the type field of every alerts.<fleet_id> message (GF-299).
const eventType = "alert.raised"

// envelope is the alert.raised body (GF-299). Field order is the wire order.
type envelope struct {
	Type     string          `json:"type"`
	FleetID  int64           `json:"fleet_id"`
	AlertID  int64           `json:"alert_id"`
	Kind     string          `json:"kind"`
	Severity string          `json:"severity"`
	RaisedAt string          `json:"raised_at"`
	Payload  json.RawMessage `json:"payload"`
}

// Subject returns alerts.<fleet_id> with the decimal fleet id (GF-299).
func Subject(fleetID int64) string {
	return fmt.Sprintf("alerts.%d", fleetID)
}

// Encode returns the subject and body for one committed alert (GF-299).
func Encode(a Raised) (string, []byte, error) {
	body, err := json.Marshal(envelope{
		Type:     eventType,
		FleetID:  a.FleetID,
		AlertID:  a.ID,
		Kind:     a.Kind,
		Severity: a.Severity,
		RaisedAt: a.RaisedAt.UTC().Format(time.RFC3339Nano),
		Payload:  a.Payload,
	})
	if err != nil {
		return "", nil, err
	}
	return Subject(a.FleetID), body, nil
}

// publishRaised sends each committed alert after the commit (GF-299). A failure
// is logged and counted and never changes the alert row (GF-4, GF-301).
func publishRaised(p Publisher, m *Metrics, raised []Raised) {
	for _, a := range raised {
		subj, body, err := Encode(a)
		if err == nil {
			err = p.Publish(subj, body)
		}
		if err != nil {
			m.fail(stagePublish)
			log.Printf("alerts: publish %s: %v", subj, err)
		}
	}
}
