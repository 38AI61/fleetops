package main

import (
	"github.com/prometheus/client_golang/prometheus"

	"fleetops/internal/hub"
)

// newWSConnectionsGauge reads the hub's open-connection count on each scrape.
// It is the README §9 "WS conns" series (GF-331). It has no labels.
func newWSConnectionsGauge(c *hub.Counters) prometheus.Collector {
	return prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "fleetops_ws_connections",
		Help: "Open WebSocket connections on this hub.",
	}, func() float64 { return float64(c.ConnsOpen.Load()) })
}
