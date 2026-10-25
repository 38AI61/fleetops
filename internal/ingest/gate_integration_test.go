package ingest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"fleetops/internal/registry"
	"fleetops/internal/testinfra"
)

func TestGatePublishReleasesSlotOnAck(t *testing.T) {
	js := testinfra.NewJetStream(t)
	ctx := context.Background()
	if err := EnsureStream(ctx, js); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}

	const total = 2 * MaxInFlight
	g := NewGate()
	dev := registry.Device{ID: 1, FleetID: 1}
	for i := 0; i < total; i++ {
		m, err := Stamp(dev, testPosition(), fixedClock(testNow), uuid.NewV7)
		if err != nil {
			t.Fatalf("Stamp %d: %v", i, err)
		}
		if err := g.Publish(js, m); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
		// The slot returns only after the ack (or error) arrives, so waiting for
		// it is the sequential ack wait. The test does not read the future itself.
		deadline := time.Now().Add(5 * time.Second)
		for len(g.slots) != 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if n := len(g.slots); n != 0 {
			t.Fatalf("publish %d: %d slots still held after 5s; ack never arrived", i, n)
		}
	}

	s, err := js.Stream(ctx, StreamName)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	if info.State.Msgs != total {
		t.Fatalf("stream holds %d messages, want %d", info.State.Msgs, total)
	}
}
