package ingest

import "github.com/prometheus/client_golang/prometheus"

// flushSizeBuckets bound the batch size histogram (GF-93). The top bucket is
// the FLEETOPS_FLUSH_MAX ceiling.
var flushSizeBuckets = []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000}

// Label values are fixed sets (GF-93). Every series is created at zero in
// NewMetrics, so a gather lists the full set before the first event.
var (
	outcomeValues    = []string{"stored", "stale", "unpaired", "dropped_accuracy"}
	failureReasons   = []string{"db", "missing_partition", "timeout", "result_mismatch"}
	terminateReasons = []string{"max_deliver", "malformed"}
	maintenanceOps   = []string{"ensure", "retain"}
	fanoutKinds      = []string{"position_batch", "presence"}
	presenceStates   = []string{"online", "offline"}
	geofenceKinds    = []string{"enter", "exit", "dwell"}
	geofenceStages   = []string{"candidates", "state", "insert"}
)

// evalBuckets bound the geofence evaluation latency, 0.5 ms to 1 s (GF-285).
var evalBuckets = []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1}

// Metrics is the ingest metric set (GF-93). Counters are incremented from Offer,
// the flush result, and the maintenance passes, never from Counters (GF-83).
// The set is registered but not exposed; /metrics is P22.
type Metrics struct {
	MessagesTotal              *prometheus.CounterVec
	FlushSize                  prometheus.Histogram
	FlushDuration              prometheus.Histogram
	FlushFailures              *prometheus.CounterVec
	MissingPartition           prometheus.Counter
	Terminated                 *prometheus.CounterVec
	AckErrors                  prometheus.Counter
	PartitionsCreated          prometheus.Counter
	PartitionsDropped          prometheus.Counter
	PartitionMaintenanceErrors *prometheus.CounterVec
	FanoutPublishFailures      *prometheus.CounterVec
	PresenceTransitions        *prometheus.CounterVec
	GeofenceEvalDuration       prometheus.Histogram
	GeofenceEvents             *prometheus.CounterVec
	GeofenceDebounced          prometheus.Counter
	GeofenceEvalFailures       *prometheus.CounterVec
}

// NewMetrics builds the GF-93 set and registers it with reg. It panics on a
// duplicate registration, as prometheus.MustRegister does.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		MessagesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fleetops_ingest_messages_total",
			Help: "Messages by outcome.",
		}, []string{"outcome"}),
		FlushSize: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "fleetops_ingest_flush_size",
			Help:    "Batch size per successful flush.",
			Buckets: flushSizeBuckets,
		}),
		FlushDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "fleetops_ingest_flush_duration_seconds",
			Help: "Flush wall time, success and failure.",
		}),
		FlushFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fleetops_ingest_flush_failures_total",
			Help: "Failed flushes by reason.",
		}, []string{"reason"}),
		MissingPartition: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "fleetops_ingest_missing_partition_total",
			Help: "Messages NAKed for a missing partition.",
		}),
		Terminated: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fleetops_ingest_terminated_total",
			Help: "Messages terminated by reason.",
		}, []string{"reason"}),
		AckErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "fleetops_ingest_ack_errors_total",
			Help: "Ack publish failures.",
		}),
		PartitionsCreated: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "fleetops_ingest_partitions_created_total",
			Help: "Partitions created by ensure-ahead.",
		}),
		PartitionsDropped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "fleetops_ingest_partitions_dropped_total",
			Help: "Partitions dropped by retention.",
		}),
		PartitionMaintenanceErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fleetops_ingest_partition_maintenance_errors_total",
			Help: "Failed partition maintenance passes by op.",
		}, []string{"op"}),
		FanoutPublishFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fleetops_fanout_publish_failures_total",
			Help: "Failed fanout publishes by kind.",
		}, []string{"kind"}),
		PresenceTransitions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fleetops_presence_transitions_total",
			Help: "Presence transitions published by state.",
		}, []string{"state"}),
		GeofenceEvalDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "fleetops_geofence_evaluation_duration_seconds",
			Help:    "Geofence evaluation wall time per flush that reaches the hook, success or failure.",
			Buckets: evalBuckets,
		}),
		GeofenceEvents: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fleetops_geofence_events_total",
			Help: "Geofence events written by kind.",
		}, []string{"kind"}),
		GeofenceDebounced: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "fleetops_geofence_debounced_total",
			Help: "Enter and exit candidates suppressed by the 30 s debounce.",
		}),
		GeofenceEvalFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fleetops_geofence_evaluation_failures_total",
			Help: "Failed geofence evaluations by stage.",
		}, []string{"stage"}),
	}
	for _, v := range outcomeValues {
		m.MessagesTotal.WithLabelValues(v)
	}
	for _, v := range failureReasons {
		m.FlushFailures.WithLabelValues(v)
	}
	for _, v := range terminateReasons {
		m.Terminated.WithLabelValues(v)
	}
	for _, v := range maintenanceOps {
		m.PartitionMaintenanceErrors.WithLabelValues(v)
	}
	for _, v := range fanoutKinds {
		m.FanoutPublishFailures.WithLabelValues(v)
	}
	for _, v := range presenceStates {
		m.PresenceTransitions.WithLabelValues(v)
	}
	for _, v := range geofenceKinds {
		m.GeofenceEvents.WithLabelValues(v)
	}
	for _, v := range geofenceStages {
		m.GeofenceEvalFailures.WithLabelValues(v)
	}
	reg.MustRegister(
		m.MessagesTotal, m.FlushSize, m.FlushDuration, m.FlushFailures,
		m.MissingPartition, m.Terminated, m.AckErrors,
		m.PartitionsCreated, m.PartitionsDropped, m.PartitionMaintenanceErrors,
		m.FanoutPublishFailures, m.PresenceTransitions,
		m.GeofenceEvalDuration, m.GeofenceEvents, m.GeofenceDebounced, m.GeofenceEvalFailures,
	)
	return m
}
