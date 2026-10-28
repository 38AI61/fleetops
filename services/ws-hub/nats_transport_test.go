package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"

	"fleetops/internal/auth"
	"fleetops/internal/fanout"
	"fleetops/internal/hub"
	"fleetops/internal/ingest"
	"fleetops/internal/presence"
	"fleetops/internal/testinfra"
)

// natsHub boots a hub on the NATS transport with no Redis client (GF-347). The
// hub gets nil for Redis, so any Redis touch would panic. The test serves it
// over httptest and returns its address.
func natsHub(t *testing.T) (addr string, nc *nats.Conn) {
	t.Helper()
	conn := testinfra.EmbeddedNATS(t)
	issuer, err := auth.NewIssuer([]byte(testSecret), time.Now)
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	base, cancelBase := context.WithCancel(context.Background())
	t.Cleanup(cancelBase)
	h := hub.New(base, hub.Options{RoomCap: 2000, Verifier: issuer})
	subs, err := h.Subscribe(base, hub.NewNATSTransport(conn), conn)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	t.Cleanup(func() { _ = subs.Close() })
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://"), conn
}

// TestIngestToHubInProcessNoRedis (GF-347): a stored batch published through the
// ingest Publisher and NATS fanout reaches the fleet's own WS clients, and no
// client of another fleet receives it.
func TestIngestToHubInProcessNoRedis(t *testing.T) {
	addr, conn := natsHub(t)
	const fleetA, fleetB = 1, 2
	a := dialAuthed(t, addr, fleetA)
	b := dialAuthed(t, addr, fleetB)

	fan := fanout.NewNATSFanout(conn)
	mem := presence.NewMemoryStore(time.Now)
	pub := &ingest.Publisher{
		Fanout:  fan,
		Tracker: presence.NewTracker(mem, fan, 120*time.Second),
		Metrics: ingest.NewMetrics(prometheus.NewRegistry()),
	}
	now := time.Now().UTC()
	pub.Publish(context.Background(), []ingest.Message{{
		MsgID:    uuid.New(),
		FleetID:  fleetA,
		DeviceID: 7,
		Position: ingest.Position{Lat: 41.0, Lng: 29.0},
		ServerTS: now,
	}})

	// Presence for the same device rides a separate subscription, so its frame
	// may come first. Read until the position batch arrives.
	for {
		if f := nextFrame(t, a, 10*time.Second); f.Type == "position.batch" {
			break
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if _, data, err := b.Read(ctx); err == nil {
		t.Fatalf("fleet B received %s; it must receive nothing", data)
	}
}

// TestPresenceChangedOverNATS (GF-347): presence from MemoryStore reaches the
// hub over NATS as presence.changed.
func TestPresenceChangedOverNATS(t *testing.T) {
	addr, conn := natsHub(t)
	const fleet = 3
	c := dialAuthed(t, addr, fleet)

	fan := fanout.NewNATSFanout(conn)
	tr := presence.NewTracker(presence.NewMemoryStore(time.Now), fan, 120*time.Second)
	now := time.Now().UTC()
	if _, err := tr.Observe(context.Background(), []presence.Observation{
		{DeviceID: 9, FleetID: fleet, ServerTS: now},
	}, now); err != nil {
		t.Fatalf("observe: %v", err)
	}

	f := nextFrame(t, c, 10*time.Second)
	if f.Type != "presence.changed" || f.FleetID != fleet {
		t.Fatalf("frame %+v, want presence.changed for fleet %d", f, fleet)
	}
}
