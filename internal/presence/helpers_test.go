package presence

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"fleetops/internal/testinfra"
)

// presenceEvent is the GF-101 presence.changed body, decoded for assertions.
type presenceEvent struct {
	Type     string `json:"type"`
	FleetID  int64  `json:"fleet_id"`
	DeviceID int64  `json:"device_id"`
	State    string `json:"state"`
}

// newRedis opens a client on the shared container. It closes with the test.
func newRedis(t *testing.T) *redis.Client {
	t.Helper()
	opts, err := redis.ParseURL(testinfra.NewRedisURL(t))
	if err != nil {
		t.Fatalf("parse redis url: %v", err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

// subscribe listens on one presence channel for fleet. Fleet ids are unique per
// test, so the channel carries only this test's events.
func subscribe(t *testing.T, rdb *redis.Client, fleet int64) <-chan *redis.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sub := rdb.Subscribe(ctx, presenceChannel(fleet))
	t.Cleanup(func() { _ = sub.Close() })
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatalf("subscribe confirm: %v", err)
	}
	return sub.Channel()
}

func presenceChannel(fleet int64) string {
	return "presence.changed." + strconv.FormatInt(fleet, 10)
}

// nextEvent waits for one event and decodes it.
func nextEvent(t *testing.T, ch <-chan *redis.Message) presenceEvent {
	t.Helper()
	select {
	case msg := <-ch:
		var ev presenceEvent
		if err := json.Unmarshal([]byte(msg.Payload), &ev); err != nil {
			t.Fatalf("decode %q: %v", msg.Payload, err)
		}
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no presence event within 5s")
	}
	return presenceEvent{}
}

// expectNoEvent fails if an event arrives within the quiet window.
func expectNoEvent(t *testing.T, ch <-chan *redis.Message) {
	t.Helper()
	select {
	case msg := <-ch:
		t.Fatalf("unexpected presence event: %s", msg.Payload)
	case <-time.After(300 * time.Millisecond):
	}
}
