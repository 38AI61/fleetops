package ingest

import (
	"slices"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// TestMetricsRegisteredNames pins the GF-93 table: exact names, types, label
// names, and fixed label values. It fails if a name is missing, renamed, or extra.
func TestMetricsRegisteredNames(t *testing.T) {
	reg := prometheus.NewRegistry()
	NewMetrics(reg)
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	want := map[string]struct {
		typ    string
		labels []string
		values []string
	}{
		"fleetops_ingest_messages_total":                     {"COUNTER", []string{"outcome"}, outcomeValues},
		"fleetops_ingest_flush_size":                         {"HISTOGRAM", nil, nil},
		"fleetops_ingest_flush_duration_seconds":             {"HISTOGRAM", nil, nil},
		"fleetops_ingest_flush_failures_total":               {"COUNTER", []string{"reason"}, failureReasons},
		"fleetops_ingest_missing_partition_total":            {"COUNTER", nil, nil},
		"fleetops_ingest_terminated_total":                   {"COUNTER", []string{"reason"}, terminateReasons},
		"fleetops_ingest_ack_errors_total":                   {"COUNTER", nil, nil},
		"fleetops_ingest_partitions_created_total":           {"COUNTER", nil, nil},
		"fleetops_ingest_partitions_dropped_total":           {"COUNTER", nil, nil},
		"fleetops_ingest_partition_maintenance_errors_total": {"COUNTER", []string{"op"}, maintenanceOps},
		"fleetops_fanout_publish_failures_total":             {"COUNTER", []string{"kind"}, fanoutKinds},
		"fleetops_presence_transitions_total":                {"COUNTER", []string{"state"}, presenceStates},
	}

	got := map[string]bool{}
	for _, mf := range families {
		name := mf.GetName()
		got[name] = true
		exp, ok := want[name]
		if !ok {
			t.Fatalf("unexpected metric family %s", name)
		}
		if mf.GetType().String() != exp.typ {
			t.Fatalf("%s type = %s, want %s", name, mf.GetType(), exp.typ)
		}
		var labels, values []string
		for _, metric := range mf.GetMetric() {
			for _, lp := range metric.GetLabel() {
				if !slices.Contains(labels, lp.GetName()) {
					labels = append(labels, lp.GetName())
				}
				values = append(values, lp.GetValue())
			}
		}
		if !slices.Equal(labels, exp.labels) {
			t.Fatalf("%s labels = %v, want %v", name, labels, exp.labels)
		}
		for _, v := range exp.values {
			if !slices.Contains(values, v) {
				t.Fatalf("%s has no series for %q", name, v)
			}
		}
	}
	for name := range want {
		if !got[name] {
			t.Fatalf("metric family %s not registered", name)
		}
	}
	if len(families) != len(want) {
		t.Fatalf("gathered %d families, want %d", len(families), len(want))
	}
}
