package main

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"

	"fleetops/internal/observability"
)

// metricsMux serves only /metrics. Every other path is a 404 (GF-330).
func metricsMux(reg *prometheus.Registry) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/metrics", observability.Handler(reg))
	return mux
}
