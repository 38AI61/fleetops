package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
)

// Publisher is the core NATS seam (GF-4, GF-179). *nats.Conn satisfies it.
type Publisher interface {
	Publish(subject string, data []byte) error
}

// eventType is the type field of every jobs.events message (GF-179).
const eventType = "job.updated"

// event is the jobs.events body (GF-179). Field order is the wire order.
// DriverUserID is the users.id linked to DriverID, or null when unlinked. The
// hub admits a driver only when it equals the connection subject (GF-192, GF-341).
type event struct {
	Type         string `json:"type"`
	FleetID      int64  `json:"fleet_id"`
	DriverID     *int64 `json:"driver_id"`
	DriverUserID *int64 `json:"driver_user_id"`
	JobID        int64  `json:"job_id"`
	FromState    State  `json:"from_state"`
	ToState      State  `json:"to_state"`
	At           stamp  `json:"at"`
}

// subject is jobs.events.<fleet_id> with the decimal fleet id (GF-179).
func subject(fleetID int64) string {
	return fmt.Sprintf("jobs.events.%d", fleetID)
}

// publishChange sends one applied transition after its commit. A failure is
// logged and never changes the HTTP answer; job_events is the durable record (GF-4).
// The driver's users.id is resolved here, after commit (GF-341). A failed lookup
// is logged and sent as null, so no driver receives the event (fail closed).
func publishChange(ctx context.Context, s *Store, p Publisher, ch Change) {
	subj := subject(ch.Job.FleetID)
	userID, err := s.DriverUserID(ctx, ch.Job.FleetID, ch.Job.DriverID)
	if err != nil {
		log.Printf("jobs: resolve driver user for job %d: %v", ch.Job.ID, err)
		userID = nil
	}
	body, err := json.Marshal(event{
		Type:         eventType,
		FleetID:      ch.Job.FleetID,
		DriverID:     ch.Job.DriverID,
		DriverUserID: userID,
		JobID:        ch.Job.ID,
		FromState:    ch.From,
		ToState:      ch.To,
		At:           stamp(ch.At),
	})
	if err == nil {
		err = p.Publish(subj, body)
	}
	if err != nil {
		log.Printf("jobs: publish %s: %v", subj, err)
	}
}
