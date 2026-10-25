package ingest

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/google/uuid"

	"fleetops/internal/registry"
)

// Clock returns the server instant. Tests inject a fixed one (GF-57).
type Clock func() time.Time

// Message is one accepted position, stamped by the gateway (D-15). It exists
// only after auth and validation succeed (GF-57).
type Message struct {
	MsgID    uuid.UUID
	FleetID  int64
	DeviceID int64
	Position Position
	ServerTS time.Time
}

// wirePayload is the GF-58 JSON body. The key set is exact.
type wirePayload struct {
	MsgID      string  `json:"msg_id"`
	FleetID    int64   `json:"fleet_id"`
	DeviceID   int64   `json:"device_id"`
	Lat        float64 `json:"lat"`
	Lng        float64 `json:"lng"`
	SpeedKmh   float64 `json:"speed_kmh"`
	HeadingDeg float64 `json:"heading_deg"`
	AccuracyM  float64 `json:"accuracy_m"`
	DeviceTS   string  `json:"device_ts"`
	ServerTS   string  `json:"server_ts"`
}

// Stamp assigns msg_id (UUIDv7) and server_ts (UTC, one clock read). The fleet
// comes from the authenticated device, never from the body (GF-58).
func Stamp(dev registry.Device, p Position, clock Clock, newID func() (uuid.UUID, error)) (Message, error) {
	id, err := newID()
	if err != nil {
		return Message{}, err
	}
	return Message{
		MsgID:    id,
		FleetID:  dev.FleetID,
		DeviceID: dev.ID,
		Position: p,
		ServerTS: clock().UTC(),
	}, nil
}

// Subject is gps.raw.<fleet_id> (GF-58).
func (m Message) Subject() string {
	return "gps.raw." + strconv.FormatInt(m.FleetID, 10)
}

// Payload is the GF-58 JSON body: no key, no secret, no license data.
func (m Message) Payload() ([]byte, error) {
	return json.Marshal(wirePayload{
		MsgID:      m.MsgID.String(),
		FleetID:    m.FleetID,
		DeviceID:   m.DeviceID,
		Lat:        m.Position.Lat,
		Lng:        m.Position.Lng,
		SpeedKmh:   m.Position.Speed,
		HeadingDeg: m.Position.Heading,
		AccuracyM:  m.Position.Accuracy,
		DeviceTS:   m.Position.DeviceTS.UTC().Format(time.RFC3339Nano),
		ServerTS:   m.ServerTS.UTC().Format(time.RFC3339Nano),
	})
}
