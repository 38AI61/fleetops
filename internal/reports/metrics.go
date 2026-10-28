package reports

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// passBuckets bound the rollup pass latency, 10 ms to 60 s.
var passBuckets = []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}

// Metrics are the GF-325 series. Label sets are pre-seeded to zero (GF-93).
// They are registered in the caller's registry and not exposed until P22.
type Metrics struct {
	PassDuration  prometheus.Histogram
	PassesSkipped *prometheus.CounterVec
}

// NewMetrics builds the rollup series and registers them with reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		PassDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "fleetops_reports_rollup_pass_duration_seconds",
			Help:    "Wall time of one rollup pass that took the lock.",
			Buckets: passBuckets,
		}),
		PassesSkipped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fleetops_reports_rollup_passes_skipped_total",
			Help: "Rollup passes skipped by reason. A fleet_tz skip counts one fleet, not the pass.",
		}, []string{"reason"}),
	}
	for _, r := range []string{reasonLockHeld, reasonFleetTZ} {
		m.PassesSkipped.WithLabelValues(r)
	}
	reg.MustRegister(m.PassDuration, m.PassesSkipped)
	return m
}

// observe records one pass that took the lock. A nil Metrics is a no-op so tests can omit it.
func (m *Metrics) observe(d time.Duration) {
	if m != nil {
		m.PassDuration.Observe(d.Seconds())
	}
}

// skipped counts one skip with its reason. A nil Metrics is a no-op.
func (m *Metrics) skipped(reason string) {
	if m != nil {
		m.PassesSkipped.WithLabelValues(reason).Inc()
	}
}
