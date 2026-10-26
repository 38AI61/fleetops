package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
)

// Subject and channel families (GF-108, GF-109).
const (
	posBatchPrefix = "positions.batch."
	presencePrefix = "presence.changed."
	jobsPrefix     = "jobs.events."
	alertsPrefix   = "alerts."
)

// Event types, one per family (GF-101, GF-109).
const (
	typePositionBatch = "position.batch"
	typePresence      = "presence.changed"
	typeJobUpdated    = "job.updated"
	typeAlertRaised   = "alert.raised"
)

// envelope is the part of each relayed body the hub reads. The body itself is
// forwarded verbatim (GF-101, GF-117).
type envelope struct {
	Type     string `json:"type"`
	FleetID  int64  `json:"fleet_id"`
	DriverID *int64 `json:"driver_id"`
}

// subscribeTimeout bounds the NATS flush that confirms the subscriptions.
const subscribeTimeout = 5 * time.Second

// Subscriptions is one hub's Redis and NATS subscriptions (GF-108).
type Subscriptions struct {
	ps    *redis.PubSub
	nsubs []*nats.Subscription
	done  chan struct{}
}

// Subscribe starts the Redis PSUBSCRIBE and the NATS core subscriptions. Both
// libraries reconnect on their own. Messages published during a gap are lost:
// the hub keeps no state (GF-4, GF-108).
func (h *Hub) Subscribe(ctx context.Context, rdb *redis.Client, nc *nats.Conn) (*Subscriptions, error) {
	ps := rdb.PSubscribe(ctx, posBatchPrefix+"*", presencePrefix+"*")
	// Two confirmations, one per pattern, before the channel takes over reading.
	for range 2 {
		if _, err := ps.Receive(ctx); err != nil {
			_ = ps.Close()
			return nil, fmt.Errorf("redis psubscribe: %w", err)
		}
	}
	s := &Subscriptions{ps: ps, done: make(chan struct{})}
	msgs := ps.Channel()
	go func() {
		defer close(s.done)
		for msg := range msgs {
			h.relayRedis(msg.Channel, []byte(msg.Payload))
		}
	}()

	// Core NATS, not queue groups: every hub instance sees every event (GF-108).
	for _, subject := range []string{jobsPrefix + "*", "alerts.*"} {
		sub, err := nc.Subscribe(subject, func(m *nats.Msg) {
			h.relayNATS(m.Subject, m.Data)
		})
		if err != nil {
			_ = s.Close()
			return nil, fmt.Errorf("nats subscribe %s: %w", subject, err)
		}
		s.nsubs = append(s.nsubs, sub)
	}
	flushCtx, cancel := context.WithTimeout(ctx, subscribeTimeout)
	defer cancel()
	if err := nc.FlushWithContext(flushCtx); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("nats flush: %w", err)
	}
	return s, nil
}

// Close unsubscribes NATS, closes the PubSub, and waits for the Redis reader.
func (s *Subscriptions) Close() error {
	for _, sub := range s.nsubs {
		_ = sub.Unsubscribe()
	}
	err := s.ps.Close()
	<-s.done
	return err
}

// relayRedis forwards one Redis message to its fleet room (GF-108, GF-116).
func (h *Hub) relayRedis(channel string, body []byte) {
	var family string
	var suffix string
	switch {
	case strings.HasPrefix(channel, posBatchPrefix):
		family, suffix = typePositionBatch, channel[len(posBatchPrefix):]
	case strings.HasPrefix(channel, presencePrefix):
		family, suffix = typePresence, channel[len(presencePrefix):]
	default:
		return
	}
	fleet, err := strconv.ParseInt(suffix, 10, 64)
	if err != nil {
		h.counters.DroppedEnvelope.Add(1)
		return
	}
	if !envelopeMatches(body, family, fleet) {
		h.counters.DroppedEnvelope.Add(1)
		return
	}
	h.broadcast(fleet, body, notDriver)
}

// relayNATS forwards one NATS message to its fleet room (GF-108, GF-109, GF-116).
func (h *Hub) relayNATS(subject string, body []byte) {
	var family string
	var suffix string
	switch {
	case strings.HasPrefix(subject, jobsPrefix):
		family, suffix = typeJobUpdated, subject[len(jobsPrefix):]
	case strings.HasPrefix(subject, alertsPrefix):
		family, suffix = typeAlertRaised, subject[len(alertsPrefix):]
	default:
		return
	}
	fleet, err := strconv.ParseInt(suffix, 10, 64)
	if err != nil || !envelopeMatches(body, family, fleet) {
		h.counters.DroppedEnvelope.Add(1)
		return
	}
	if family != typeJobUpdated {
		h.broadcast(fleet, body, notDriver)
		return
	}
	// A job update must name its driver, so the driver filter can apply (GF-109).
	env, _ := parseEnvelope(body)
	if env.DriverID == nil {
		h.counters.DroppedEnvelope.Add(1)
		return
	}
	h.broadcast(fleet, body, jobFor(*env.DriverID))
}

// envelopeMatches reports whether body is the family's type and carries fleet.
func envelopeMatches(body []byte, family string, fleet int64) bool {
	env, ok := parseEnvelope(body)
	return ok && env.Type == family && env.FleetID == fleet
}

func parseEnvelope(body []byte) (envelope, bool) {
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return envelope{}, false
	}
	return env, true
}

// notDriver admits every role but driver. Drivers see only their own job updates (GF-116).
func notDriver(c *conn) bool {
	return c.role != "driver"
}

// jobFor admits non-drivers, and a driver only for their own job updates.
func jobFor(driverID int64) func(*conn) bool {
	return func(c *conn) bool {
		return c.role != "driver" || c.subject == driverID
	}
}
