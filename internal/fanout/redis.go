package fanout

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Channel names and body types (GF-101).
const (
	positionBatchType = "position.batch"
	presenceType      = "presence.changed"
)

// RedisFanout publishes to Redis pub/sub (GF-100, GF-101).
type RedisFanout struct {
	rdb *redis.Client
}

// NewRedisFanout wraps an open client. The caller owns its lifetime.
func NewRedisFanout(rdb *redis.Client) *RedisFanout {
	return &RedisFanout{rdb: rdb}
}

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

// PublishPositions publishes each chunk to positions.batch.<fleet_id> in order.
// Failed chunks do not stop the rest; their errors are joined (GF-100).
func (f *RedisFanout) PublishPositions(ctx context.Context, points []Point) error {
	var errs []error
	for _, b := range PlanPositionBatches(points, PositionBatchMax) {
		body, err := json.Marshal(positionBatchBody{Type: positionBatchType, FleetID: b.FleetID, Points: b.Points})
		if err != nil {
			errs = append(errs, fmt.Errorf("marshal position batch: %w", err))
			continue
		}
		channel := fmt.Sprintf("positions.batch.%d", b.FleetID)
		if err := f.rdb.Publish(ctx, channel, body).Err(); err != nil {
			errs = append(errs, fmt.Errorf("publish %s: %w", channel, err))
		}
	}
	return errors.Join(errs...)
}

// PublishPresence publishes one presence.changed event to presence.changed.<fleet_id>.
func (f *RedisFanout) PublishPresence(ctx context.Context, ev PresenceEvent) error {
	body, err := json.Marshal(presenceBody{
		Type:     presenceType,
		FleetID:  ev.FleetID,
		DeviceID: ev.DeviceID,
		State:    ev.State,
		TS:       ev.TS.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return fmt.Errorf("marshal presence: %w", err)
	}
	channel := fmt.Sprintf("presence.changed.%d", ev.FleetID)
	if err := f.rdb.Publish(ctx, channel, body).Err(); err != nil {
		return fmt.Errorf("publish %s: %w", channel, err)
	}
	return nil
}
