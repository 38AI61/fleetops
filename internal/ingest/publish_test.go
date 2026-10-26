package ingest

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"

	"fleetops/internal/fanout"
	"fleetops/internal/presence"
	"fleetops/internal/testinfra"
)

// nopTracker is the no-op presence tracker for tests that do not exercise presence.
type nopTracker struct{}

func (nopTracker) Observe(context.Context, []presence.Observation, time.Time) (presence.Result, error) {
	return presence.Result{}, nil
}

// batchBody decodes the GF-101 position.batch fields the tests check.
type batchBody struct {
	FleetID int64 `json:"fleet_id"`
	Points  []struct {
		MsgID string `json:"msg_id"`
	} `json:"points"`
}

// subscribeBatches listens on positions.batch.<fleet>. Fleet ids are unique per test.
func subscribeBatches(t *testing.T, rdb *redis.Client, fleet int64) <-chan *redis.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sub := rdb.Subscribe(ctx, batchChannel(fleet))
	t.Cleanup(func() { _ = sub.Close() })
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatalf("subscribe confirm: %v", err)
	}
	return sub.Channel()
}

func batchChannel(fleet int64) string {
	return "positions.batch." + strconv.FormatInt(fleet, 10)
}

// flushDurationSum is the total wall time of every flush so far, in seconds.
func (l *loopEnv) flushDurationSum(t *testing.T) float64 {
	t.Helper()
	families, err := l.reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, mf := range families {
		if mf.GetName() == "fleetops_ingest_flush_duration_seconds" {
			return mf.GetMetric()[0].GetHistogram().GetSampleSum()
		}
	}
	t.Fatal("flush duration histogram not registered")
	return 0
}

// redisClient opens a client on url. It closes with the test.
func redisClient(t *testing.T, url string) *redis.Client {
	t.Helper()
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("parse redis url: %v", err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

// storedMsg is a stored-shape message for the publish tests.
func storedMsg(fleet, device int64, serverTS time.Time) Message {
	return Message{
		MsgID:    uuid.New(),
		FleetID:  fleet,
		DeviceID: device,
		Position: Position{Lat: 41, Lng: 29, Speed: 30, Heading: 90, Accuracy: 5, DeviceTS: serverTS},
		ServerTS: serverTS,
	}
}

func TestPublishChunksPerFleet(t *testing.T) {
	rdb := redisClient(t, testinfra.NewRedisURL(t))
	const fleetA, fleetB = 95001, 95002
	chA := subscribeBatches(t, rdb, fleetA)
	chB := subscribeBatches(t, rdb, fleetB)

	ts := time.Now().UTC()
	var stored []Message
	for range 250 {
		stored = append(stored, storedMsg(fleetA, 1, ts))
	}
	for range 30 {
		stored = append(stored, storedMsg(fleetB, 2, ts))
	}
	// Interleave fleets so the publisher must group them.
	stored[0], stored[250] = stored[250], stored[0]

	m := NewMetrics(prometheus.NewRegistry())
	pub := &Publisher{Fanout: fanout.NewRedisFanout(rdb), Tracker: nopTracker{}, Metrics: m}
	pub.Publish(context.Background(), stored)

	wantA := []int{200, 50}
	for i, want := range wantA {
		got := receiveBatch(t, chA)
		if len(got.Points) != want {
			t.Fatalf("fleet A batch %d has %d points, want %d", i, len(got.Points), want)
		}
	}
	if got := receiveBatch(t, chB); len(got.Points) != 30 {
		t.Fatalf("fleet B batch has %d points, want 30", len(got.Points))
	}
	expectNoBatch(t, chA)
	expectNoBatch(t, chB)
}

func receiveBatch(t *testing.T, ch <-chan *redis.Message) batchBody {
	t.Helper()
	select {
	case msg := <-ch:
		var b batchBody
		if err := json.Unmarshal([]byte(msg.Payload), &b); err != nil {
			t.Fatalf("decode batch: %v", err)
		}
		if len(b.Points) > fanout.PositionBatchMax {
			t.Fatalf("batch has %d points, over the %d cap", len(b.Points), fanout.PositionBatchMax)
		}
		return b
	case <-time.After(5 * time.Second):
		t.Fatal("no position batch within 5s")
	}
	return batchBody{}
}

func expectNoBatch(t *testing.T, ch <-chan *redis.Message) {
	t.Helper()
	select {
	case msg := <-ch:
		t.Fatalf("unexpected batch: %s", msg.Payload)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestFlushPublishesStoredOnly: accuracy-dropped, stale, and unpaired messages
// produce no position.batch. Only the two committed, paired points do.
func TestFlushPublishesStoredOnly(t *testing.T) {
	l := newLoopEnv(t, defaultConsumerSpec(), 500)
	rdb := redisClient(t, testinfra.NewRedisURL(t))
	ch := subscribeBatches(t, rdb, l.fe.fleet)
	l.cons.pub = &Publisher{Fanout: fanout.NewRedisFanout(rdb), Tracker: nopTracker{}, Metrics: l.m}

	live1 := l.liveMsg(time.Now().UTC())
	live2 := l.liveMsg(time.Now().UTC())
	dropped := l.liveMsg(time.Now().UTC())
	dropped.Position.Accuracy = 60
	stale := l.liveMsg(time.Now().UTC().Add(-61 * time.Second))
	unpaired := l.liveMsg(time.Now().UTC())
	unpaired.DeviceID = l.fe.device()
	for _, msg := range []Message{live1, live2, dropped, stale, unpaired} {
		l.publish(msg)
	}
	stop := l.start()

	l.waitCounter("fleetops_ingest_messages_total", "outcome", "stored", 2)
	l.waitRows(2)
	if err := stop(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := map[string]bool{live1.MsgID.String(): true, live2.MsgID.String(): true}
	got := map[string]bool{}
	for len(got) < len(want) {
		b := receiveBatch(t, ch)
		for _, p := range b.Points {
			got[p.MsgID] = true
		}
	}
	for id := range got {
		if !want[id] {
			t.Fatalf("published msg_id %s is not a stored point", id)
		}
	}
	expectNoBatch(t, ch)
}

// TestFlushAcksWhenRedisDown: Redis at 127.0.0.1:1. The batch still commits and
// acks, the failure counters move, and the flushes take under 2.5 s in total.
func TestFlushAcksWhenRedisDown(t *testing.T) {
	l := newLoopEnv(t, defaultConsumerSpec(), 500)
	rdb := redisClient(t, "redis://127.0.0.1:1/0")
	fan := fanout.NewRedisFanout(rdb)
	l.cons.pub = &Publisher{
		Fanout:  fan,
		Tracker: presence.NewTracker(rdb, fan, 120*time.Second),
		Metrics: l.m,
	}

	l.publish(l.liveMsg(time.Now().UTC()))
	l.publish(l.liveMsg(time.Now().UTC()))
	stop := l.start()

	l.waitRows(2)
	l.waitCounter("fleetops_fanout_publish_failures_total", "kind", "position_batch", 1)
	l.waitCounter("fleetops_fanout_publish_failures_total", "kind", "presence", 1)
	if err := stop(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	l.waitAckPending()
	if got := l.rows(); got != 2 {
		t.Fatalf("rows = %d, want 2", got)
	}
	if sum := l.flushDurationSum(t); sum > 2.5 {
		t.Fatalf("flush took %.2fs with Redis down, want within 2.5s", sum)
	}
}
