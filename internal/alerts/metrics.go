package alerts

import "github.com/prometheus/client_golang/prometheus"

// Stages of a failed pass, the label of fleetops_alert_evaluation_failures_total (GF-301).
const (
	stageRules      = "rules"
	stageCandidates = "candidates"
	stageInsert     = "insert"
	stagePublish    = "publish"
	stageNotify     = "notify"
)

// evalBuckets bound the pass latency, 1 ms to 10 s (GF-93 style).
var evalBuckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// Metrics are the GF-301 series. Label sets are pre-seeded to zero (GF-93).
// They are registered in the caller's registry and not exposed until P22.
type Metrics struct {
	Raised        *prometheus.CounterVec
	EvalDuration  prometheus.Histogram
	Failures      *prometheus.CounterVec
	PassesSkipped prometheus.Counter
}

// NewMetrics builds the alert series and registers them with reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		Raised: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fleetops_alerts_raised_total",
			Help: "Alerts raised by kind and severity.",
		}, []string{"kind", "severity"}),
		EvalDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "fleetops_alert_evaluation_duration_seconds",
			Help:    "Wall time of one alert evaluation pass that took the lock.",
			Buckets: evalBuckets,
		}),
		Failures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fleetops_alert_evaluation_failures_total",
			Help: "Failed alert evaluation steps by stage.",
		}, []string{"stage"}),
		PassesSkipped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "fleetops_alert_passes_skipped_total",
			Help: "Alert passes that did not get the advisory lock.",
		}),
	}
	for _, k := range Kinds {
		for _, s := range []string{SeverityWarning, SeverityCritical} {
			m.Raised.WithLabelValues(k, s)
		}
	}
	for _, s := range []string{stageRules, stageCandidates, stageInsert, stagePublish, stageNotify} {
		m.Failures.WithLabelValues(s)
	}
	reg.MustRegister(m.Raised, m.EvalDuration, m.Failures, m.PassesSkipped)
	return m
}

// fail counts one failed stage. A nil Metrics is a no-op so tests can omit it.
func (m *Metrics) fail(stage string) {
	if m != nil {
		m.Failures.WithLabelValues(stage).Inc()
	}
}
