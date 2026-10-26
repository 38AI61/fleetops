// Package fanout carries accepted positions and presence transitions from
// ingest to the WS hub (D-17, D-41). Redis pub/sub is the only transport; the
// Fanout interface is the seam that keeps the cloud profile open (D-41).
package fanout

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Fanout publishes client-facing events. Errors are reported to the caller;
// implementations never retry (GF-100).
type Fanout interface {
	PublishPositions(ctx context.Context, points []Point) error
	PublishPresence(ctx context.Context, ev PresenceEvent) error
}

// Point is one durable position in the position.batch body (GF-101). The field
// set mirrors the GF-58 wire payload. FleetID routes the point and is not sent
// per point, since the batch carries it once.
type Point struct {
	MsgID      uuid.UUID `json:"msg_id"`
	FleetID    int64     `json:"-"`
	DeviceID   int64     `json:"device_id"`
	Lat        float64   `json:"lat"`
	Lng        float64   `json:"lng"`
	SpeedKmh   float64   `json:"speed_kmh"`
	HeadingDeg float64   `json:"heading_deg"`
	AccuracyM  float64   `json:"accuracy_m"`
	DeviceTS   time.Time `json:"device_ts"`
	ServerTS   time.Time `json:"server_ts"`
}

// PresenceEvent is one online or offline transition for a device (GF-101).
type PresenceEvent struct {
	FleetID  int64
	DeviceID int64
	State    string
	TS       time.Time
}

// Nop discards every event. Tests that do not exercise fanout pass it (GF-100).
type Nop struct{}

// PublishPositions discards points.
func (Nop) PublishPositions(context.Context, []Point) error { return nil }

// PublishPresence discards the event.
func (Nop) PublishPresence(context.Context, PresenceEvent) error { return nil }
