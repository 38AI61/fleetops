package main

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/prometheus/client_golang/prometheus"

	"fleetops/internal/hub"
)

// gaugeSample gathers reg and returns the single fleetops_ws_connections sample and its label count.
func gaugeSample(t *testing.T, reg *prometheus.Registry) (value float64, labels int) {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, f := range families {
		if f.GetName() == "fleetops_ws_connections" {
			m := f.GetMetric()[0]
			return m.GetGauge().GetValue(), len(m.GetLabel())
		}
	}
	t.Fatal("fleetops_ws_connections not gathered")
	return 0, 0
}

// TestWSConnectionsGauge checks the gauge follows ConnsOpen and carries no labels.
func TestWSConnectionsGauge(t *testing.T) {
	var c hub.Counters
	reg := prometheus.NewRegistry()
	reg.MustRegister(newWSConnectionsGauge(&c))

	c.ConnsOpen.Store(3)
	if v, labels := gaugeSample(t, reg); v != 3 || labels != 0 {
		t.Fatalf("fleetops_ws_connections = %v labels %d, want 3 and 0", v, labels)
	}
	c.ConnsOpen.Store(0)
	if v, _ := gaugeSample(t, reg); v != 0 {
		t.Fatalf("fleetops_ws_connections = %v, want 0", v)
	}
}

// scrapeWSConnections polls the live /metrics until fleetops_ws_connections equals want.
func scrapeWSConnections(t *testing.T, addr string, want float64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/metrics")
		if err != nil {
			t.Fatalf("GET /metrics: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		for _, l := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(l, "fleetops_ws_connections ") {
				last = strings.TrimPrefix(l, "fleetops_ws_connections ")
				if v, err := strconv.ParseFloat(last, 64); err == nil && v == want {
					return
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("fleetops_ws_connections = %q, want %v", last, want)
}

// TestWSHubMetricsServed checks the real ws-hub mux reports one open /stream connection,
// then zero after it closes.
func TestWSHubMetricsServed(t *testing.T) {
	addr, _ := startService(t)
	scrapeWSConnections(t, addr, 0)

	c := dialAuthed(t, addr, 1)
	scrapeWSConnections(t, addr, 1)

	_ = c.Close(websocket.StatusNormalClosure, "")
	scrapeWSConnections(t, addr, 0)
}
