// Package observability exposes the private Prometheus registries that each
// service owns (GF-329). Registries are not consolidated: a service builds its
// own with NewRegistry and serves it with Handler. Nothing here carries a
// fleet_id label, so the unauthenticated /metrics route exposes no tenant data.
package observability

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NewRegistry returns a private registry with the Go and process collectors.
// Callers register their own series on it.
func NewRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return reg
}

// Handler serves reg in the Prometheus text format.
func Handler(reg *prometheus.Registry) http.Handler {
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg})
}
