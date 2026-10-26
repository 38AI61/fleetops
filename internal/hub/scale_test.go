package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
