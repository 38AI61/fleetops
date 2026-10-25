package ingest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"fleetops/internal/registry"
)

// TestFullPathGatewayToPG is the roadmap P6 Testing item. A real gateway handler
// publishes to a real JetStream, the consumer and the partition maintenance run,
// and each posted position ends as one row. Nothing is left unacked.
func TestFullPathGatewayToPG(t *testing.T) {
	l := newLoopEnv(t, defaultConsumerSpec(), 500)
	ctx := context.Background()

	// A fresh device paired to its own vehicle, so the key is known to the test.
	vehicleID := l.fe.vehicle(2)
	issued, err := l.fe.svc.IssueDevice(ctx, l.fe.actor, registry.KindTracker)
	if err != nil {
		t.Fatalf("issue device: %v", err)
	}
	l.fe.pair(vehicleID, issued.ID)

	h := &Handler{DB: l.fe.conn, Salt: l.fe.svc.Salt, Clock: time.Now, Gate: l.gate, JS: l.js}
	e := echo.New()
	h.Register(e)
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)

	// Boot order (GF-91, GF-92): maintenance first pass, then the consumer.
	maintCtx, cancelMaint := context.WithCancel(ctx)
	maintDone := make(chan struct{})
	booted := make(chan struct{})
	maint := &Maintenance{DB: l.fe.conn, Clock: time.Now, Metrics: l.m}
	go func() {
		defer close(maintDone)
		maint.Run(maintCtx, func() { close(booted) })
	}()
	t.Cleanup(func() { cancelMaint(); <-maintDone })
	select {
	case <-booted:
	case <-time.After(30 * time.Second):
		t.Fatal("maintenance boot pass did not finish")
	}
	if err := EnsureConsumer(ctx, l.js); err != nil {
		t.Fatalf("EnsureConsumer: %v", err)
	}
	stop := l.start()

	const n = 20
	client := srv.Client()
	for i := range n {
		body := fmt.Sprintf(`{"lat":41.0%02d,"lng":29.0,"speed":50,"heading":90,"accuracy":8,"ts":%q}`,
			i, time.Now().UTC().Format(time.RFC3339))
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/ingest/position", strings.NewReader(body))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("X-Device-Key", issued.APIKey)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("post %d: %v", i, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("post %d status = %d, want 204", i, resp.StatusCode)
		}
	}

	l.waitRows(n)
	l.waitAckPending()
	if err := stop(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := l.distinctMsgIDs(); got != n {
		t.Fatalf("distinct msg_id = %d, want %d", got, n)
	}
}
