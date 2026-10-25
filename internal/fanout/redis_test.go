package fanout

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"fleetops/internal/testinfra"
)

// newRedisClient opens a client on url and closes it with the test.
func newRedisClient(t *testing.T, url string) *redis.Client {
	t.Helper()
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("parse redis url: %v", err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

func TestRedisFanoutRoundTrip(t *testing.T) {
	rdb := newRedisClient(t, testinfra.NewRedisURL(t))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const fleet = 91001
	sub := rdb.PSubscribe(ctx, "positions.batch.*")
	t.Cleanup(func() { _ = sub.Close() })
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatalf("subscribe confirm: %v", err)
	}

	id := uuid.MustParse("01930000-0000-7000-8000-000000000001")
	p := Point{
		MsgID:      id,
		FleetID:    fleet,
		DeviceID:   42,
		Lat:        52.5,
		Lng:        13.25,
		SpeedKmh:   41,
		HeadingDeg: 90,
		AccuracyM:  4.5,
		DeviceTS:   time.Date(2026, 10, 25, 10, 0, 0, 500_000_000, time.UTC),
		ServerTS:   time.Date(2026, 10, 25, 10, 0, 1, 0, time.UTC),
	}
	f := NewRedisFanout(rdb)
	if err := f.PublishPositions(ctx, []Point{p}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	msg, err := sub.ReceiveMessage(ctx)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if msg.Channel != "positions.batch.91001" {
		t.Fatalf("channel %q", msg.Channel)
	}
	const want = `{"type":"position.batch","fleet_id":91001,"points":[{"msg_id":"01930000-0000-7000-8000-000000000001","device_id":42,"lat":52.5,"lng":13.25,"speed_kmh":41,"heading_deg":90,"accuracy_m":4.5,"device_ts":"2026-10-25T10:00:00.5Z","server_ts":"2026-10-25T10:00:01Z"}]}`
	if msg.Payload != want {
		t.Fatalf("body mismatch\n got: %s\nwant: %s", msg.Payload, want)
	}
}

func TestRedisFanoutDownReturnsWithinBudget(t *testing.T) {
	rdb := newRedisClient(t, "redis://127.0.0.1:1/0")
	f := NewRedisFanout(rdb)

	start := time.Now()
	err := f.PublishPositions(context.Background(), []Point{{FleetID: 1, MsgID: uuid.New()}})
	if err == nil {
		t.Fatal("publish to a dead endpoint returned nil")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("publish took %s, want within 2s", d)
	}
}
