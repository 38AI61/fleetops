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
)

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
	reg.MustRegister(
		m.MessagesTotal, m.FlushSize, m.FlushDuration, m.FlushFailures,
		m.MissingPartition, m.Terminated, m.AckErrors,
		m.PartitionsCreated, m.PartitionsDropped, m.PartitionMaintenanceErrors,
		m.FanoutPublishFailures, m.PresenceTransitions,
	)
	return m
}
