package observability_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"fleetops/internal/alerts"
	"fleetops/internal/eta"
	"fleetops/internal/ingest"
	"fleetops/internal/observability"
	"fleetops/internal/reports"
)

// ingestRegistry builds the registry services/ingest/main.go builds: one registry
// carrying the ingest, eta, reports and alerts series (GF-329).
func ingestRegistry() *prometheus.Registry {
	reg := observability.NewRegistry()
	ingest.NewMetrics(reg)
	eta.NewMetrics(reg)
	reports.NewMetrics(reg)
	alerts.NewMetrics(reg)
	return reg
}

// expectedFamilies are the README §9 names and the other GF-93, GF-301, GF-315 and GF-325 series.
var expectedFamilies = []string{
	"fleetops_ingest_messages_total",
	"fleetops_ingest_flush_size",
	"fleetops_ingest_flush_duration_seconds",
	"fleetops_ingest_flush_failures_total",
	"fleetops_ingest_missing_partition_total",
	"fleetops_ingest_terminated_total",
	"fleetops_ingest_ack_errors_total",
	"fleetops_ingest_partitions_created_total",
	"fleetops_ingest_partitions_dropped_total",
	"fleetops_ingest_partition_maintenance_errors_total",
	"fleetops_fanout_publish_failures_total",
	"fleetops_presence_transitions_total",
	"fleetops_geofence_evaluation_duration_seconds",
	"fleetops_geofence_events_total",
	"fleetops_geofence_debounced_total",
	"fleetops_geofence_evaluation_failures_total",
	"fleetops_eta_calls_total",
	"fleetops_eta_rule_skipped_total",
	"fleetops_eta_pass_duration_seconds",
	"fleetops_eta_passes_skipped_total",
	"fleetops_reports_rollup_pass_duration_seconds",
	"fleetops_reports_rollup_passes_skipped_total",
	"fleetops_alerts_raised_total",
	"fleetops_alert_evaluation_duration_seconds",
	"fleetops_alert_evaluation_failures_total",
	"fleetops_alert_passes_skipped_total",
}

// seededLabels lists each labelled family's pre-seeded label values (GF-93, GF-301, GF-315, GF-325).
// Every value must be present at zero before any event.
var seededLabels = map[string]map[string][]string{
	"fleetops_ingest_messages_total":                     {"outcome": {"stored", "stale", "unpaired", "dropped_accuracy"}},
	"fleetops_ingest_flush_failures_total":               {"reason": {"db", "missing_partition", "timeout", "result_mismatch"}},
	"fleetops_ingest_terminated_total":                   {"reason": {"max_deliver", "malformed"}},
	"fleetops_ingest_partition_maintenance_errors_total": {"op": {"ensure", "retain"}},
	"fleetops_fanout_publish_failures_total":             {"kind": {"position_batch", "presence"}},
	"fleetops_presence_transitions_total":                {"state": {"online", "offline"}},
	"fleetops_geofence_events_total":                     {"kind": {"enter", "exit", "dwell"}},
	"fleetops_geofence_evaluation_failures_total":        {"stage": {"candidates", "state", "insert"}},
	"fleetops_eta_calls_total":                           {"outcome": {"ok", "unreachable", "timeout", "http_error", "malformed", "no_route"}},
	"fleetops_eta_rule_skipped_total":                    {"reason": {"osrm_unavailable", "no_position", "no_target"}},
	"fleetops_reports_rollup_passes_skipped_total":       {"reason": {"lock_held", "fleet_tz"}},
	"fleetops_alert_evaluation_failures_total":           {"stage": {"rules", "candidates", "insert", "publish", "notify"}},
}

// allowedLabelNames is the closed set of label names in the fleetops registries.
// fleet_id is absent by design (GF-93, GF-301): the unauthenticated scrape carries no tenant.
var allowedLabelNames = map[string]bool{"outcome": true, "reason": true, "op": true, "kind": true, "state": true, "stage": true, "severity": true}

// gathered indexes a registry by family name, then by the sorted "label=value" key of each sample.
func gathered(t *testing.T, reg *prometheus.Registry) map[string]map[string]float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	out := map[string]map[string]float64{}
	for _, f := range families {
		out[f.GetName()] = map[string]float64{}
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, lp := range m.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}
			key := make([]string, 0, len(labels))
			for k, v := range labels {
				key = append(key, k+"="+v)
			}
			sort.Strings(key)
			var v float64
			switch {
			case m.GetCounter() != nil:
				v = m.GetCounter().GetValue()
			case m.GetGauge() != nil:
				v = m.GetGauge().GetValue()
			}
			out[f.GetName()][strings.Join(key, ",")] = v
		}
	}
	return out
}

// TestRegisteredSeriesAndLabels checks every README §9 series is registered, every seeded label value
// is present at zero, and no series carries a fleet_id or unexpected label name.
func TestRegisteredSeriesAndLabels(t *testing.T) {
	series := gathered(t, ingestRegistry())

	for _, name := range expectedFamilies {
		if _, ok := series[name]; !ok {
			t.Errorf("series %s is not registered", name)
		}
	}
	for family, byLabel := range seededLabels {
		for label, values := range byLabel {
			for _, v := range values {
				got, ok := series[family][label+"="+v]
				if !ok {
					t.Errorf("%s: seeded %s=%s is missing", family, label, v)
					continue
				}
				if got != 0 {
					t.Errorf("%s{%s=%s} = %v before any event, want 0", family, label, v, got)
				}
			}
		}
	}
	for family, samples := range series {
		// The Go and process collectors are standard series, not fleetops ones (GF-331).
		if !strings.HasPrefix(family, "fleetops_") {
			continue
		}
		for key := range samples {
			if key == "" {
				continue
			}
			for _, pair := range strings.Split(key, ",") {
				name := strings.SplitN(pair, "=", 2)[0]
				if name == "fleet_id" {
					t.Errorf("%s carries fleet_id", family)
				}
				if !allowedLabelNames[name] && name != "le" {
					t.Errorf("%s has unexpected label %q", family, name)
				}
			}
		}
	}
}
