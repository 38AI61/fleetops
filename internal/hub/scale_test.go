package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// scaleConns is the connection count for TestScaleConnections (GF-343). It is a
// test flag, not an environment variable, so no D-22 row applies.
var scaleConns = flag.Int("scale.conns", 1000, "connections for TestScaleConnections")

// scaleWorkers bounds the client-side goroutines of TestScaleConnections (GF-343).
const scaleWorkers = 64

// scaleOpTimeout bounds one dial, read or close in TestScaleConnections (G2).
const scaleOpTimeout = 60 * time.Second

// synthPoint and synthBatch match the GF-101 position.batch body for the
// 1,000-connection test.
type synthPoint struct {
	MsgID    string  `json:"msg_id"`
	DeviceID int64   `json:"device_id"`
	Lat      float64 `json:"lat"`
	Lng      float64 `json:"lng"`
	ServerTS string  `json:"server_ts"`
}

type synthBatch struct {
	Type    string       `json:"type"`
	FleetID int64        `json:"fleet_id"`
	Points  []synthPoint `json:"points"`
}

// TestThousandSyntheticConnections: 1,000 authenticated clients in one room each
// receive one 200-point batch. Afterwards the goroutines return to baseline (GF-125).
func TestThousandSyntheticConnections(t *testing.T) {
	const n, fleet = 1000, 4001
	r := newRig(t, Options{})
	b := newBus(t, r)
	baseline := runtime.NumGoroutine()
	start := time.Now()

	conns := make([]*websocket.Conn, n)
	dialAll(t, r, conns, fleet)
	joined(t, r, n)

	var batch synthBatch
	batch.Type, batch.FleetID = "position.batch", fleet
	for i := range 200 {
		batch.Points = append(batch.Points, synthPoint{
			MsgID:    fmt.Sprintf("0193-%04d", i),
			DeviceID: 42,
			Lat:      52.5,
			Lng:      13.4,
			ServerTS: "2026-10-25T10:00:01Z",
		})
	}
	body, err := json.Marshal(batch)
	if err != nil {
		t.Fatalf("marshal batch: %v", err)
	}
	b.publishRedis(fmt.Sprintf("positions.batch.%d", fleet), string(body))

	readAll(t, conns, body)
	closeAll(conns)

	elapsed := time.Since(start)
	t.Logf("1000 synthetic connections: auth, batch, and close in %s", elapsed)
	if elapsed > 60*time.Second {
		t.Fatalf("1000 connections took %s, budget is 60s", elapsed)
	}
	waitGoroutines(t, baseline+50)
}

// dialAll dials and authenticates n clients, 64 at a time. Each client reads
// its auth.ok before the caller publishes.
func dialAll(t *testing.T, r *rig, conns []*websocket.Conn, fleet int64) {
	t.Helper()
	sem := make(chan struct{}, 64)
	errs := make(chan error, len(conns))
	var wg sync.WaitGroup
	for i := range conns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			c, err := dialAuthRaw(r, fleet, int64(i))
			if err != nil {
				errs <- err
				return
			}
			conns[i] = c
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("dial: %v", err)
	}
}

// dialAuthRaw is dialAuth without t, for goroutines.
func dialAuthRaw(r *rig, fleet, user int64) (*websocket.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), scaleOpTimeout)
	defer cancel()
	c, _, err := websocket.Dial(ctx, r.url+"?fleet="+itoa(fleet), nil)
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(1 << 20)
	tok, err := r.issuer.Issue(user+1, fleet, "dispatcher")
	if err != nil {
		_ = c.CloseNow()
		return nil, err
	}
	body, _ := json.Marshal(map[string]string{"type": "auth", "token": tok})
	if err := c.Write(ctx, websocket.MessageText, body); err != nil {
		_ = c.CloseNow()
		return nil, err
	}
	if _, _, err := c.Read(ctx); err != nil {
		_ = c.CloseNow()
		return nil, fmt.Errorf("auth.ok: %w", err)
	}
	return c, nil
}

// readAll checks that every client receives want as its next frame.
func readAll(t *testing.T, conns []*websocket.Conn, want []byte) {
	t.Helper()
	errs := make(chan error, len(conns))
	var wg sync.WaitGroup
	for _, c := range conns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, got, err := c.Read(ctx)
			if err != nil {
				errs <- err
				return
			}
			if !bytes.Equal(got, want) {
				errs <- fmt.Errorf("client received %d bytes, want the %d-byte batch", len(got), len(want))
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("read batch: %v", err)
	}
}

// closeAll closes every client with a normal close.
func closeAll(conns []*websocket.Conn) {
	var wg sync.WaitGroup
	for _, c := range conns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = c.Close(websocket.StatusNormalClosure, "")
		}()
	}
	wg.Wait()
}

// waitGoroutines polls until the process holds at most limit goroutines.
func waitGoroutines(t *testing.T, limit int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for runtime.NumGoroutine() > limit {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines = %d after close, want at most %d", runtime.NumGoroutine(), limit)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestScaleConnections: N authenticated clients split across ceil(N/2000) fleets,
// each room filled to the production cap of 2,000. Every client receives its
// room's batch byte-for-byte, and the goroutines return to baseline (GF-343).
// Dials, reads and closes run on scaleWorkers goroutines, not one per connection.
func TestScaleConnections(t *testing.T) {
	n := *scaleConns
	if n < 1 {
		t.Fatalf("-scale.conns = %d, want at least 1", n)
	}
	const firstFleet = 5001
	fleetOf := func(i int) int64 { return firstFleet + int64(i/DefaultRoomCap) }
	fleets := (n + DefaultRoomCap - 1) / DefaultRoomCap
	budget := scaleBudget(n)

	r := newRig(t, Options{})
	b := newBus(t, r)
	baseline := runtime.NumGoroutine()
	start := time.Now()

	conns := make([]*websocket.Conn, n)
	if err := runPool(n, func(i int) error {
		c, err := dialAuthRaw(r, fleetOf(i), int64(i))
		if err != nil {
			return err
		}
		conns[i] = c
		return nil
	}); err != nil {
		t.Fatalf("dial and auth: %v", err)
	}
	waitJoined(t, r, int64(n), budget)

	bodies := make([][]byte, fleets)
	for f := range fleets {
		fleet := firstFleet + int64(f)
		body, err := json.Marshal(scaleBatch(fleet))
		if err != nil {
			t.Fatalf("marshal batch: %v", err)
		}
		bodies[f] = body
		b.publishRedis(fmt.Sprintf("positions.batch.%d", fleet), string(body))
	}

	if err := runPool(n, func(i int) error {
		ctx, cancel := context.WithTimeout(context.Background(), scaleOpTimeout)
		defer cancel()
		_, got, err := conns[i].Read(ctx)
		if err != nil {
			return fmt.Errorf("client %d read: %w", i, err)
		}
		if want := bodies[i/DefaultRoomCap]; !bytes.Equal(got, want) {
			return fmt.Errorf("client %d received %d bytes, want the %d-byte batch of its room", i, len(got), len(want))
		}
		return nil
	}); err != nil {
		t.Fatalf("read batch: %v", err)
	}

	if err := runPool(n, func(i int) error {
		return conns[i].Close(websocket.StatusNormalClosure, "")
	}); err != nil {
		t.Logf("close: %v", err)
	}

	elapsed := time.Since(start)
	t.Logf("%d synthetic connections in %d fleets: auth, batch, and close in %s (budget %s)", n, fleets, elapsed, budget)
	if elapsed > budget {
		t.Fatalf("%d connections took %s, budget is %s", n, elapsed, budget)
	}
	waitGoroutines(t, baseline+50)
}

// scaleBudget is 60 s per 1,000 connections, capped at 20 minutes (GF-343).
func scaleBudget(n int) time.Duration {
	d := time.Duration(n) * 60 * time.Second / 1000
	if d > 20*time.Minute {
		return 20 * time.Minute
	}
	return d
}

// scaleBatch is one 200-point position.batch for fleet, the GF-101 shape.
func scaleBatch(fleet int64) synthBatch {
	var batch synthBatch
	batch.Type, batch.FleetID = "position.batch", fleet
	for i := range 200 {
		batch.Points = append(batch.Points, synthPoint{
			MsgID:    fmt.Sprintf("0193-%04d", i),
			DeviceID: 42,
			Lat:      52.5,
			Lng:      13.4,
			ServerTS: "2026-10-25T10:00:01Z",
		})
	}
	return batch
}

// runPool calls fn for each index in 0..n-1 on scaleWorkers goroutines and
// returns the first error. Every index is attempted.
func runPool(n int, fn func(i int) error) error {
	jobs := make(chan int)
	var mu sync.Mutex
	var first error
	var wg sync.WaitGroup
	for range scaleWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if err := fn(i); err != nil {
					mu.Lock()
					if first == nil {
						first = err
					}
					mu.Unlock()
				}
			}
		}()
	}
	for i := range n {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return first
}

// waitJoined polls until n clients are in a room, within budget.
func waitJoined(t *testing.T, r *rig, n int64, budget time.Duration) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for r.hub.Counters().ConnsOpen.Load() != n {
		if time.Now().After(deadline) {
			t.Fatalf("clients joined = %d, want %d", r.hub.Counters().ConnsOpen.Load(), n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
