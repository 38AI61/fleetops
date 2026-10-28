package fanout

import (
	"encoding/json"
	"fmt"
	"time"
)

// Channel names and body types (GF-101). The same subjects carry the events in
// redis and nats modes (GF-347).
const (
	positionBatchType = "position.batch"
	presenceType      = "presence.changed"
)

// positionBatchBody is the GF-101 position.batch wire body.
type positionBatchBody struct {
	Type    string  `json:"type"`
	FleetID int64   `json:"fleet_id"`
	Points  []Point `json:"points"`
}

// presenceBody is the GF-101 presence.changed wire body.
type presenceBody struct {
	Type     string `json:"type"`
	FleetID  int64  `json:"fleet_id"`
	DeviceID int64  `json:"device_id"`
	State    string `json:"state"`
	TS       string `json:"ts"`
}

// message is one encoded event and the subject it goes to.
type message struct {
	subject string
	body    []byte
}

// encodePositions splits points into position.batch messages in order. A chunk
// that fails to marshal is reported and skipped; the rest still encode (GF-100).
func encodePositions(points []Point) ([]message, []error) {
	var msgs []message
	var errs []error
	for _, b := range PlanPositionBatches(points, PositionBatchMax) {
		body, err := json.Marshal(positionBatchBody{Type: positionBatchType, FleetID: b.FleetID, Points: b.Points})
		if err != nil {
			errs = append(errs, fmt.Errorf("marshal position batch: %w", err))
			continue
		}
		msgs = append(msgs, message{subject: fmt.Sprintf("positions.batch.%d", b.FleetID), body: body})
	}
	return msgs, errs
}

// encodePresence builds one presence.changed message for ev.
func encodePresence(ev PresenceEvent) (message, error) {
	body, err := json.Marshal(presenceBody{
		Type:     presenceType,
		FleetID:  ev.FleetID,
		DeviceID: ev.DeviceID,
		State:    ev.State,
		TS:       ev.TS.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return message{}, fmt.Errorf("marshal presence: %w", err)
	}
	return message{subject: fmt.Sprintf("presence.changed.%d", ev.FleetID), body: body}, nil
}
