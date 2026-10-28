package fanout

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"pgregory.net/rapid"

	"fleetops/internal/testinfra"
)

// wirePair is one subject and body as a subscriber sees it.
type wirePair struct {
	subject string
	body    []byte
}

// TestNATSAndRedisFanoutPayloadParity (GF-347, property, D-37): random batches
// and presence events produce the same subjects and bytes on Redis and on an
// embedded NATS server.
func TestNATSAndRedisFanoutPayloadParity(t *testing.T) {
	opts, err := redis.ParseURL(testinfra.NewRedisURL(t))
	if err != nil {
		t.Fatalf("redis url: %v", err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })
	nc := testinfra.EmbeddedNATS(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// One subscription per family, opened before any publish.
	posPS := rdb.PSubscribe(ctx, "positions.batch.*")
	presPS := rdb.PSubscribe(ctx, "presence.changed.*")
	for _, ps := range []*redis.PubSub{posPS, presPS} {
		if _, err := ps.Receive(ctx); err != nil {
			t.Fatalf("redis subscribe: %v", err)
		}
		t.Cleanup(func() { _ = ps.Close() })
	}
	posRedis := posPS.Channel(redis.WithChannelSize(4096))
	presRedis := presPS.Channel(redis.WithChannelSize(4096))
	posNATS, err := nc.SubscribeSync("positions.batch.*")
	if err != nil {
		t.Fatalf("nats subscribe: %v", err)
	}
	presNATS, err := nc.SubscribeSync("presence.changed.*")
	if err != nil {
		t.Fatalf("nats subscribe: %v", err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatalf("nats flush: %v", err)
	}

	rf := NewRedisFanout(rdb)
	nf := NewNATSFanout(nc)
	base := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)

	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 30).Draw(rt, "n")
		points := make([]Point, n)
		for i := range points {
			points[i] = Point{
				MsgID:      uuid.NewSHA1(uuid.NameSpaceOID, []byte(rapid.String().Draw(rt, "msg"))),
				FleetID:    rapid.Int64Range(1, 3).Draw(rt, "fleet"),
				DeviceID:   rapid.Int64Range(1, 50).Draw(rt, "device"),
				Lat:        rapid.Float64Range(-90, 90).Draw(rt, "lat"),
				Lng:        rapid.Float64Range(-180, 180).Draw(rt, "lng"),
				SpeedKmh:   rapid.Float64Range(0, 200).Draw(rt, "speed"),
				HeadingDeg: rapid.Float64Range(0, 360).Draw(rt, "heading"),
				AccuracyM:  rapid.Float64Range(0, 50).Draw(rt, "accuracy"),
				DeviceTS:   base.Add(time.Duration(rapid.Int64Range(0, 3600).Draw(rt, "dts")) * time.Second),
				ServerTS:   base.Add(time.Duration(rapid.Int64Range(0, 3600).Draw(rt, "sts")) * time.Second),
			}
		}
		ev := PresenceEvent{
			FleetID:  rapid.Int64Range(1, 3).Draw(rt, "pfleet"),
			DeviceID: rapid.Int64Range(1, 50).Draw(rt, "pdevice"),
			State:    rapid.SampledFrom([]string{"online", "offline"}).Draw(rt, "state"),
			TS:       base,
		}

		expPos, encErrs := encodePositions(points)
		if len(encErrs) != 0 {
			rt.Fatalf("encode: %v", encErrs)
		}
		expPres, err := encodePresence(ev)
		if err != nil {
			rt.Fatalf("encode presence: %v", err)
		}

		if err := rf.PublishPositions(ctx, points); err != nil {
			rt.Fatalf("redis positions: %v", err)
		}
		if err := rf.PublishPresence(ctx, ev); err != nil {
			rt.Fatalf("redis presence: %v", err)
		}
		if err := nf.PublishPositions(ctx, points); err != nil {
			rt.Fatalf("nats positions: %v", err)
		}
		if err := nf.PublishPresence(ctx, ev); err != nil {
			rt.Fatalf("nats presence: %v", err)
		}

		var gotRedis, gotNATS []wirePair
		for range expPos {
			select {
			case m := <-posRedis:
				gotRedis = append(gotRedis, wirePair{m.Channel, []byte(m.Payload)})
			case <-ctx.Done():
				rt.Fatalf("redis position %d missing", len(gotRedis))
			}
			m, err := posNATS.NextMsgWithContext(ctx)
			if err != nil {
				rt.Fatalf("nats position %d missing: %v", len(gotNATS), err)
			}
			gotNATS = append(gotNATS, wirePair{m.Subject, m.Data})
		}
		var gotPresRedis wirePair
		select {
		case m := <-presRedis:
			gotPresRedis = wirePair{m.Channel, []byte(m.Payload)}
		case <-ctx.Done():
			rt.Fatal("redis presence missing")
		}
		m, err := presNATS.NextMsgWithContext(ctx)
		if err != nil {
			rt.Fatalf("nats presence missing: %v", err)
		}
		gotPresNATS := wirePair{m.Subject, m.Data}

		for i, want := range expPos {
			w := wirePair(want)
			if gotRedis[i].subject != w.subject || string(gotRedis[i].body) != string(w.body) {
				rt.Fatalf("redis position %d differs from encoder", i)
			}
			if gotNATS[i].subject != w.subject || string(gotNATS[i].body) != string(w.body) {
				rt.Fatalf("nats position %d differs from encoder", i)
			}
		}
		if gotPresRedis.subject != expPres.subject || string(gotPresRedis.body) != string(expPres.body) {
			rt.Fatal("redis presence differs from encoder")
		}
		if gotPresNATS.subject != expPres.subject || string(gotPresNATS.body) != string(expPres.body) {
			rt.Fatal("nats presence differs from encoder")
		}
	})
}
