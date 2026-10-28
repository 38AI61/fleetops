package eta

import "github.com/prometheus/client_golang/prometheus"

// Skip reasons, the label of fleetops_eta_rule_skipped_total (GF-314, GF-315).
const (
	reasonOSRMUnavailable = "osrm_unavailable"
	reasonNoPosition      = "no_position"
	reasonNoTarget        = "no_target"
)

// skipReasons lists every reason label value, for pre-seeding.
var skipReasons = []string{reasonOSRMUnavailable, reasonNoPosition, reasonNoTarget}

// passBuckets bound the pass latency, 1 ms to 10 s (GF-93 style, as in the alert evaluator).
var passBuckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// Metrics are the GF-315 series. Label sets are pre-seeded to zero (GF-93, GF-315).
// They are registered in the caller's registry and not exposed until P22.
// A nil *Metrics is a no-op, so tests can omit it.
type Metrics struct {
	Calls         *prometheus.CounterVec
	Skipped       *prometheus.CounterVec
	PassDuration  prometheus.Histogram
	PassesSkipped prometheus.Counter
}

// NewMetrics builds the eta series and registers them with reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		Calls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fleetops_eta_calls_total",
			Help: "OSRM route calls by outcome.",
		}, []string{"outcome"}),
		Skipped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fleetops_eta_rule_skipped_total",
			Help: "Jobs skipped by the ETA pass by reason.",
		}, []string{"reason"}),
		PassDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "fleetops_eta_pass_duration_seconds",
			Help:    "Wall time of one ETA pass that took the lock.",
			Buckets: passBuckets,
		}),
		PassesSkipped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "fleetops_eta_passes_skipped_total",
			Help: "ETA passes that did not get the advisory lock.",
		}),
	}
	for _, o := range Outcomes {
		m.Calls.WithLabelValues(string(o))
	}
	for _, r := range skipReasons {
		m.Skipped.WithLabelValues(r)
	}
	reg.MustRegister(m.Calls, m.Skipped, m.PassDuration, m.PassesSkipped)
	return m
}

func (m *Metrics) call(o Outcome) {
	if m != nil {
		m.Calls.WithLabelValues(string(o)).Inc()
	}
}

func (m *Metrics) skip(reason string) {
	if m != nil {
		m.Skipped.WithLabelValues(reason).Inc()
	}
}

func (m *Metrics) passSkipped() {
	if m != nil {
		m.PassesSkipped.Inc()
	}
}
