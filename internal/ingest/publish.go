package ingest

import (
	"context"
	"log"
	"time"

	"fleetops/internal/fanout"
	"fleetops/internal/presence"
)

// publishBudget bounds each fanout stage of one flush (GF-103).
const publishBudget = time.Second

// PresenceTracker is the part of presence.Tracker the publisher uses. Tests
// pass a no-op (GF-103).
type PresenceTracker interface {
	Observe(ctx context.Context, obs []presence.Observation, now time.Time) (presence.Result, error)
}

// Publisher sends each committed batch to the fanout and to presence (GF-103).
// It never fails a flush: errors are counted and logged.
type Publisher struct {
	Fanout  fanout.Fanout
	Tracker PresenceTracker
	Metrics *Metrics
}

// Publish sends the stored messages of one committed flush. Positions go first,
// then presence, each under its own budget. The context is detached from the
// flush deadline, so a slow Redis cannot cancel a committed batch (GF-103).
func (p *Publisher) Publish(ctx context.Context, stored []Message) {
	if len(stored) == 0 {
		return
	}
	ctx = context.WithoutCancel(ctx)

	points := make([]fanout.Point, len(stored))
	obs := make([]presence.Observation, len(stored))
	for i, m := range stored {
		points[i] = m.fanoutPoint()
		obs[i] = presence.Observation{DeviceID: m.DeviceID, FleetID: m.FleetID, ServerTS: m.ServerTS}
	}

	pctx, cancel := context.WithTimeout(ctx, publishBudget)
	err := p.Fanout.PublishPositions(pctx, points)
	cancel()
	if err != nil {
		p.Metrics.FanoutPublishFailures.WithLabelValues("position_batch").Inc()
		log.Printf("ingest: publish positions: %v", err)
	}

	sctx, cancel := context.WithTimeout(ctx, publishBudget)
	res, err := p.Tracker.Observe(sctx, obs, time.Now())
	cancel()
	p.Metrics.PresenceTransitions.WithLabelValues("online").Add(float64(res.Online))
	if err != nil {
		p.Metrics.FanoutPublishFailures.WithLabelValues("presence").Inc()
		log.Printf("ingest: presence: %v", err)
	}
}

// RecordSweep counts one offline sweep (GF-121). The sweeper logs its own errors.
func (p *Publisher) RecordSweep(res presence.Result, err error) {
	p.Metrics.PresenceTransitions.WithLabelValues("offline").Add(float64(res.Offline))
	if err != nil {
		p.Metrics.FanoutPublishFailures.WithLabelValues("presence").Inc()
	}
}

// fanoutPoint converts a stored message to the client position shape (GF-101).
// Heading is the raw reading, as in the GF-58 payload.
func (m Message) fanoutPoint() fanout.Point {
	return fanout.Point{
		MsgID:      m.MsgID,
		FleetID:    m.FleetID,
		DeviceID:   m.DeviceID,
		Lat:        m.Position.Lat,
		Lng:        m.Position.Lng,
		SpeedKmh:   m.Position.Speed,
		HeadingDeg: m.Position.Heading,
		AccuracyM:  m.Position.Accuracy,
		DeviceTS:   m.Position.DeviceTS.UTC(),
		ServerTS:   m.ServerTS.UTC(),
	}
}
