package fanout

import (
	"context"
	"errors"
	"fmt"

	"github.com/nats-io/nats.go"
)

// NATSFanout publishes to core NATS (GF-347). Core subjects are at-most-once,
// the same as Redis pub/sub; no JetStream stream holds them. Use it only in the
// cloud profile, where every service shares one embedded NATS server.
type NATSFanout struct {
	nc *nats.Conn
}

// NewNATSFanout wraps an open connection. The caller owns its lifetime.
func NewNATSFanout(nc *nats.Conn) *NATSFanout {
	return &NATSFanout{nc: nc}
}

// PublishPositions publishes each chunk to positions.batch.<fleet_id>. Core NATS
// publishes do not wait on the server, so ctx only bounds the caller's budget.
func (f *NATSFanout) PublishPositions(_ context.Context, points []Point) error {
	msgs, errs := encodePositions(points)
	for _, m := range msgs {
		if err := f.nc.Publish(m.subject, m.body); err != nil {
			errs = append(errs, fmt.Errorf("publish %s: %w", m.subject, err))
		}
	}
	return errors.Join(errs...)
}

// PublishPresence publishes one presence.changed event to presence.changed.<fleet_id>.
func (f *NATSFanout) PublishPresence(_ context.Context, ev PresenceEvent) error {
	m, err := encodePresence(ev)
	if err != nil {
		return err
	}
	if err := f.nc.Publish(m.subject, m.body); err != nil {
		return fmt.Errorf("publish %s: %w", m.subject, err)
	}
	return nil
}
