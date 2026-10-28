package fanout

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// RedisFanout publishes to Redis pub/sub (GF-100, GF-101).
type RedisFanout struct {
	rdb *redis.Client
}

// NewRedisFanout wraps an open client. The caller owns its lifetime.
func NewRedisFanout(rdb *redis.Client) *RedisFanout {
	return &RedisFanout{rdb: rdb}
}

// PublishPositions publishes each chunk to positions.batch.<fleet_id> in order.
// Failed chunks do not stop the rest; their errors are joined (GF-100).
func (f *RedisFanout) PublishPositions(ctx context.Context, points []Point) error {
	msgs, errs := encodePositions(points)
	for _, m := range msgs {
		if err := f.rdb.Publish(ctx, m.subject, m.body).Err(); err != nil {
			errs = append(errs, fmt.Errorf("publish %s: %w", m.subject, err))
		}
	}
	return errors.Join(errs...)
}

// PublishPresence publishes one presence.changed event to presence.changed.<fleet_id>.
func (f *RedisFanout) PublishPresence(ctx context.Context, ev PresenceEvent) error {
	m, err := encodePresence(ev)
	if err != nil {
		return err
	}
	if err := f.rdb.Publish(ctx, m.subject, m.body).Err(); err != nil {
		return fmt.Errorf("publish %s: %w", m.subject, err)
	}
	return nil
}
