package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"

	"fleetops/internal/auth"
	"fleetops/internal/fanout"
	"fleetops/internal/ingest"
	"fleetops/internal/presence"
	"fleetops/internal/testinfra"
)

// testSecret signs test tokens. It is a fixture, not a deployment secret.
var testSecret = strings.Repeat("e", 32)

// startService runs run(ctx) on a free port, with env pointing at the shared
// Redis and NATS. The service stops at test end. It returns host:port.
func startService(t *testing.T) (addr string, stop func() error) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://unused@127.0.0.1:1/fleetops")
	t.Setenv("JWT_SECRET", testSecret)
	t.Setenv("DEVICE_KEY_SALT", strings.Repeat("d", 32))
	t.Setenv("NATS_URL", testinfra.NATSURL(t))
	t.Setenv("REDIS_URL", testinfra.NewRedisURL(t))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	addr = ln.Addr().String()
	_ = ln.Close()
	*listen = addr

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx) }()
	stop = func() error {
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(10 * time.Second):
			return fmt.Errorf("run did not return within 10s")
		}
	}
	t.Cleanup(func() { _ = stop() })

	// Wait for the listener: a refused dial means it is not up yet. If run has
	// already returned, its error is the failure.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("run returned before listening: %v", err)
		default:
		}
		c, _, err := websocket.Dial(context.Background(), "ws://"+addr+"/stream?fleet=1", nil)
		if err == nil {
			_ = c.CloseNow()
			return addr, stop
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("service not listening on %s within 10s", addr)
	return addr, stop
}

// dialAuthed opens a dispatcher client to fleet's room and reads auth.ok.
func dialAuthed(t *testing.T, addr string, fleet int64) *websocket.Conn {
	t.Helper()
	iss, err := auth.NewIssuer([]byte(testSecret), time.Now)
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	tok, err := iss.Issue(9, fleet, "dispatcher")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws://"+addr+"/stream?fleet="+strconv.FormatInt(fleet, 10), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	c.SetReadLimit(1 << 20)
	body, _ := json.Marshal(map[string]string{"type": "auth", "token": tok})
	if err := c.Write(ctx, websocket.MessageText, body); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	_, ack, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("auth.ok: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(ack, &got); err != nil || got["type"] != "auth.ok" {
		t.Fatalf("first frame %s, want auth.ok", ack)
	}
	return c
}

// pipeline is the production ingest path: the consumer on GPS_RAW, flushing to
// Postgres, then publishing to Redis through the real Publisher and Tracker.
type pipeline struct {
	t       *testing.T
	db      *sql.DB
	js      jetstream.JetStream
	gate    *ingest.Gate
	tracker *presence.Tracker
	fleet   int64
	device  int64
}

// newPipeline migrates a database, pairs a device to a vehicle in a fresh
// fleet, and starts the consumer. The consumer stops at test end.
func newPipeline(t *testing.T) *pipeline {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	db := testinfra.NewMigratedDB(t)
	if err := ingest.EnsurePartitions(ctx, db, time.Now()); err != nil {
		t.Fatalf("partitions: %v", err)
	}
	js := testinfra.NewJetStream(t)
	if err := ingest.EnsureStream(ctx, js); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if err := ingest.EnsureConsumer(ctx, js); err != nil {
		t.Fatalf("consumer: %v", err)
	}

	p := &pipeline{t: t, db: db, js: js, gate: ingest.NewGate()}
	p.fleet, p.device = seedPairing(t, db)

	opts, err := redis.ParseURL(testinfra.NewRedisURL(t))
	if err != nil {
		t.Fatalf("redis url: %v", err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })
	// Device ids restart in every database, so clear the key this device may own.
	if err := rdb.Del(ctx, fmt.Sprintf("device:%d", p.device)).Err(); err != nil {
		t.Fatalf("clear device key: %v", err)
	}

	fan := fanout.NewRedisFanout(rdb)
	p.tracker = presence.NewTracker(presence.NewRedisStore(rdb), fan, 120*time.Second)
	metrics := ingest.NewMetrics(prometheus.NewRegistry())
	counters := &ingest.Counters{}
	buf := ingest.NewBuffer(time.Second, 500, 50, counters)
	writer := ingest.NewWriter(db, counters)
	pub := &ingest.Publisher{Fanout: fan, Tracker: p.tracker, Metrics: metrics}
	cons := ingest.NewConsumer(js, buf, writer, metrics, time.Now, pub)
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Run returns an error only on a startup failure, which newPipeline has already passed.
		_ = cons.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return p
}

// seedPairing inserts a fleet, a device, and a vehicle paired to that device.
func seedPairing(t *testing.T, db *sql.DB) (fleet, device int64) {
	t.Helper()
	ctx := context.Background()
	if err := db.QueryRowContext(ctx, `INSERT INTO fleets (name) VALUES ('e2e') RETURNING id`).Scan(&fleet); err != nil {
		t.Fatalf("fleet: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO devices (fleet_id, kind, api_key_hash) VALUES ($1, 'tracker', '\x00') RETURNING id`, fleet,
	).Scan(&device); err != nil {
		t.Fatalf("device: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO vehicles (fleet_id, plate, paired_device_id) VALUES ($1, 'E2E-1', $2)`, fleet, device,
	); err != nil {
		t.Fatalf("vehicle: %v", err)
	}
	return fleet, device
}

// publish sends one accepted position stamped serverTS through the gateway path
// and returns its msg_id. The accuracy passes the default 50 m gate.
func (p *pipeline) publish(serverTS time.Time) string {
	p.t.Helper()
	id := uuid.New()
	m := ingest.Message{
		MsgID:    id,
		FleetID:  p.fleet,
		DeviceID: p.device,
		Position: ingest.Position{Lat: 41, Lng: 29, Speed: 30, Heading: 90, Accuracy: 5, DeviceTS: serverTS},
		ServerTS: serverTS,
	}
	if err := p.gate.Publish(p.js, m); err != nil {
		p.t.Fatalf("publish: %v", err)
	}
	return id.String()
}

// frame is the part of a client frame the end-to-end tests read.
type frame struct {
	Type     string `json:"type"`
	FleetID  int64  `json:"fleet_id"`
	DeviceID int64  `json:"device_id"`
	State    string `json:"state"`
	Points   []struct {
		MsgID string `json:"msg_id"`
	} `json:"points"`
}

// nextFrame reads one frame within d.
func nextFrame(t *testing.T, c *websocket.Conn, d time.Duration) frame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	var f frame
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("decode frame %s: %v", data, err)
	}
	return f
}

// TestWSHubCoalescedBatchEndToEnd (roadmap Validation 1): accepted positions
// reach a browser-free client as coalesced batches, in order, at most 200 per frame.
func TestWSHubCoalescedBatchEndToEnd(t *testing.T) {
	p := newPipeline(t)
	addr, _ := startService(t)
	c := dialAuthed(t, addr, p.fleet)

	const n = 12
	var want []string
	base := time.Now().UTC()
	for i := range n {
		want = append(want, p.publish(base.Add(time.Duration(i)*time.Millisecond)))
	}

	var got []string
	frames := 0
	for len(got) < n {
		f := nextFrame(t, c, 15*time.Second)
		if f.Type != "position.batch" {
			continue // presence frames from the same flush are allowed
		}
		frames++
		if len(f.Points) > 200 {
			t.Fatalf("frame carries %d points, want at most 200", len(f.Points))
		}
		for _, pt := range f.Points {
			got = append(got, pt.MsgID)
		}
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("points out of order or missing\n got: %v\nwant: %v", got, want)
	}
	if frames >= n {
		t.Fatalf("%d frames for %d points: nothing was coalesced", frames, n)
	}
	t.Logf("%d points arrived in %d frame(s)", n, frames)
}

// TestSilentDeviceOfflineOnceEndToEnd (roadmap Validation 3): a device goes
// online through ingest. Two sweeps at now+121s yield exactly one offline event.
func TestSilentDeviceOfflineOnceEndToEnd(t *testing.T) {
	p := newPipeline(t)
	addr, _ := startService(t)
	c := dialAuthed(t, addr, p.fleet)

	serverTS := time.Now().UTC()
	p.publish(serverTS)
	for {
		f := nextFrame(t, c, 15*time.Second)
		if f.Type == "presence.changed" && f.DeviceID == p.device && f.State == "online" {
			break
		}
	}

	// The sweeper shares the ingest process's tracker, as in production (GF-106).
	sw := presence.NewSweeper(p.db, p.tracker, nil)
	now := serverTS.Add(121 * time.Second)
	for i, want := range []int{1, 0} {
		res, err := sw.Sweep(context.Background(), now)
		if err != nil {
			t.Fatalf("sweep %d: %v", i+1, err)
		}
		if res.Offline != want {
			t.Fatalf("sweep %d emitted %d offline, want %d", i+1, res.Offline, want)
		}
	}

	offline := 0
	quiet := time.Now().Add(2 * time.Second)
	for time.Now().Before(quiet) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Until(quiet))
		_, data, err := c.Read(ctx)
		cancel()
		if err != nil {
			break
		}
		var f frame
		if json.Unmarshal(data, &f) == nil && f.Type == "presence.changed" && f.DeviceID == p.device && f.State == "offline" {
			offline++
		}
	}
	if offline != 1 {
		t.Fatalf("client saw %d offline events for the device, want exactly 1", offline)
	}
}

// TestWSHubShutdownClosesWith1001: cancelling run's context closes every client
// with 1001 and returns within 5 s. The client reads throughout, as a browser
// does, so it answers the close handshake.
func TestWSHubShutdownClosesWith1001(t *testing.T) {
	addr, stop := startService(t)
	c := dialAuthed(t, addr, 77)

	closed := make(chan error, 1)
	go func() {
		for {
			_, _, err := c.Read(context.Background())
			if err != nil {
				closed <- err
				return
			}
		}
	}()

	start := time.Now()
	if err := stop(); err != nil {
		t.Fatalf("run returned %v, want nil", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("run took %s to return, want within 5s", d)
	}
	select {
	case err := <-closed:
		if got := websocket.CloseStatus(err); got != 1001 {
			t.Fatalf("client close code %d (%v), want 1001", got, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("client was not closed within 5s")
	}
}
