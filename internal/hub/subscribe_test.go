package hub

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"

	"fleetops/internal/testinfra"
)

// bus is one hub wired to real Redis and real NATS, with publishing clients.
type bus struct {
	t   *testing.T
	rdb *redis.Client
	nc  *nats.Conn
}

// newBus subscribes r's hub to the shared Redis and NATS. Teardown closes the
// subscriptions before the clients.
func newBus(t *testing.T, r *rig) *bus {
	t.Helper()
	opts, err := redis.ParseURL(testinfra.NewRedisURL(t))
	if err != nil {
		t.Fatalf("parse redis url: %v", err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })

	nc, err := nats.Connect(testinfra.NATSURL(t))
	if err != nil {
		t.Fatalf("connect nats: %v", err)
	}
	t.Cleanup(nc.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	subs, err := r.hub.Subscribe(ctx, rdb, nc)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	t.Cleanup(func() { _ = subs.Close() })
	return &bus{t: t, rdb: rdb, nc: nc}
}

// publishRedis sends body on channel.
func (b *bus) publishRedis(channel, body string) {
	b.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.rdb.Publish(ctx, channel, body).Err(); err != nil {
		b.t.Fatalf("redis publish %s: %v", channel, err)
	}
}

// publishNATS sends body on subject and flushes, so the hub sees it in order.
func (b *bus) publishNATS(subject, body string) {
	b.t.Helper()
	if err := b.nc.Publish(subject, []byte(body)); err != nil {
		b.t.Fatalf("nats publish %s: %v", subject, err)
	}
	if err := b.nc.Flush(); err != nil {
		b.t.Fatalf("nats flush: %v", err)
	}
}

// joined waits until n clients are in a room.
func joined(t *testing.T, r *rig, n int64) {
	t.Helper()
	waitFor(t, "clients joined", func() bool { return r.hub.Counters().ConnsOpen.Load() == n })
}

const (
	batchBody    = `{"type":"position.batch","fleet_id":3001,"points":[{"msg_id":"0193-a","device_id":42,"lat":52.5,"lng":13.4,"speed_kmh":41,"heading_deg":90,"accuracy_m":4.5,"device_ts":"2026-10-25T10:00:00.5Z","server_ts":"2026-10-25T10:00:01Z"}]}`
	presenceBody = `{"type":"presence.changed","fleet_id":3002,"device_id":42,"state":"offline","ts":"2026-10-25T10:02:01Z"}`
)

func TestHubRelaysRedisBatchVerbatim(t *testing.T) {
	r := newRig(t, Options{})
	b := newBus(t, r)
	c := r.dialAuth(3001, r.token(9, 3001, "dispatcher"))
	readAuthOK(t, c, 3001, "dispatcher")
	joined(t, r, 1)

	b.publishRedis("positions.batch.3001", batchBody)
	if got := string(readMsg(t, c)); got != batchBody {
		t.Fatalf("relayed bytes differ\n got: %s\nwant: %s", got, batchBody)
	}
}

func TestHubRelaysPresenceChanged(t *testing.T) {
	r := newRig(t, Options{})
	b := newBus(t, r)
	c := r.dialAuth(3002, r.token(9, 3002, "manager"))
	readAuthOK(t, c, 3002, "manager")
	joined(t, r, 1)

	b.publishRedis("presence.changed.3002", presenceBody)
	if got := string(readMsg(t, c)); got != presenceBody {
		t.Fatalf("relayed bytes differ\n got: %s\nwant: %s", got, presenceBody)
	}
}

func TestHubRelaysNATSJobAndAlert(t *testing.T) {
	r := newRig(t, Options{})
	b := newBus(t, r)
	c := r.dialAuth(3003, r.token(9, 3003, "dispatcher"))
	readAuthOK(t, c, 3003, "dispatcher")
	joined(t, r, 1)

	job := `{"type":"job.updated","fleet_id":3003,"job_id":11,"driver_id":42,"status":"assigned"}`
	alert := `{"type":"alert.raised","fleet_id":3003,"alert_id":5,"severity":"high"}`
	b.publishNATS("jobs.events.3003", job)
	b.publishNATS("alerts.3003", alert)
	if got := string(readMsg(t, c)); got != job {
		t.Fatalf("job relayed as %s", got)
	}
	if got := string(readMsg(t, c)); got != alert {
		t.Fatalf("alert relayed as %s", got)
	}
}

// TestHubDropsMismatchedFleetEnvelope: the body fleet_id must equal the subject
// fleet. A mismatch is dropped and counted, and the next valid event still arrives.
func TestHubDropsMismatchedFleetEnvelope(t *testing.T) {
	r := newRig(t, Options{})
	b := newBus(t, r)
	c := r.dialAuth(3004, r.token(9, 3004, "dispatcher"))
	readAuthOK(t, c, 3004, "dispatcher")
	joined(t, r, 1)

	b.publishRedis("positions.batch.3004", `{"type":"position.batch","fleet_id":3005,"points":[]}`)
	valid := `{"type":"position.batch","fleet_id":3004,"points":[]}`
	b.publishRedis("positions.batch.3004", valid)

	if got := string(readMsg(t, c)); got != valid {
		t.Fatalf("first frame %s, want the valid event", got)
	}
	if got := r.hub.Counters().DroppedEnvelope.Load(); got != 1 {
		t.Fatalf("DroppedEnvelope = %d, want 1", got)
	}
}

// TestHubDriverReceivesOnlyOwnJobUpdated: a driver gets their own job updates
// and nothing else. A dispatcher in the same fleet confirms that the other
// events were relayed before the driver's end marker is sent.
func TestHubDriverReceivesOnlyOwnJobUpdated(t *testing.T) {
	r := newRig(t, Options{})
	b := newBus(t, r)
	driver := r.dialAuth(3006, r.token(42, 3006, "driver"))
	readAuthOK(t, driver, 3006, "driver")
	dispatcher := r.dialAuth(3006, r.token(9, 3006, "dispatcher"))
	readAuthOK(t, dispatcher, 3006, "dispatcher")
	joined(t, r, 2)

	jobOther := `{"type":"job.updated","fleet_id":3006,"job_id":1,"driver_id":43}`
	jobOwn := `{"type":"job.updated","fleet_id":3006,"job_id":2,"driver_id":42}`
	presence := `{"type":"presence.changed","fleet_id":3006,"device_id":7,"state":"online","ts":"2026-10-25T10:00:00Z"}`
	alert := `{"type":"alert.raised","fleet_id":3006,"alert_id":3,"severity":"low"}`
	end := `{"type":"job.updated","fleet_id":3006,"job_id":99,"driver_id":42}`

	b.publishNATS("jobs.events.3006", jobOther)
	b.publishRedis("presence.changed.3006", presence)
	b.publishNATS("alerts.3006", alert)
	b.publishNATS("jobs.events.3006", jobOwn)
	// The dispatcher receives all four before the marker, so every relay ran.
	for _, want := range []string{jobOther, presence, alert, jobOwn} {
		if got := string(readMsg(t, dispatcher)); got != want {
			t.Fatalf("dispatcher got %s, want %s", got, want)
		}
	}
	b.publishNATS("jobs.events.3006", end)

	if got := string(readMsg(t, driver)); got != jobOwn {
		t.Fatalf("driver's first frame %s, want its own job", got)
	}
	if got := string(readMsg(t, driver)); got != end {
		t.Fatalf("driver's second frame %s, want the end marker", got)
	}
}
