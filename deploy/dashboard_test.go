package deploy

import (
	"encoding/json"
	"strings"
	"testing"
)

// dashboardPath is the one dashboard file; TestDashboardJSON loads it.
const dashboardPath = "grafana/dashboards/fleetops-observability.json"

// TestDashboardJSON checks the dashboard parses, has the uid, exactly four panels, the
// Prometheus datasource on every panel and target, and only README §9 metric names (GF-333).
func TestDashboardJSON(t *testing.T) {
	raw := readFile(t, dashboardPath)
	var d struct {
		UID    string `json:"uid"`
		Panels []struct {
			Datasource struct {
				UID string `json:"uid"`
			} `json:"datasource"`
			Targets []struct {
				Expr       string `json:"expr"`
				Datasource struct {
					UID string `json:"uid"`
				} `json:"datasource"`
			} `json:"targets"`
		} `json:"panels"`
	}
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatalf("dashboard JSON: %v", err)
	}
	if d.UID != "fleetops-observability" {
		t.Errorf("uid = %q, want fleetops-observability", d.UID)
	}
	if len(d.Panels) != 4 {
		t.Fatalf("panels = %d, want 4", len(d.Panels))
	}
	allowed := []string{
		"fleetops_ingest_messages_total",
		"fleetops_ingest_flush_size_bucket",
		"fleetops_ws_connections",
		"fleetops_geofence_evaluation_duration_seconds_bucket",
	}
	for i, p := range d.Panels {
		if p.Datasource.UID != "prometheus" {
			t.Errorf("panel %d datasource uid = %q, want prometheus", i, p.Datasource.UID)
		}
		if len(p.Targets) == 0 {
			t.Errorf("panel %d has no targets", i)
		}
		for _, tg := range p.Targets {
			if tg.Datasource.UID != "prometheus" {
				t.Errorf("panel %d target datasource uid = %q, want prometheus", i, tg.Datasource.UID)
			}
			found := false
			for _, m := range allowed {
				if strings.Contains(tg.Expr, m) {
					found = true
				}
			}
			if !found {
				t.Errorf("panel %d expr %q names no G3 metric", i, tg.Expr)
			}
		}
	}
	if strings.Contains(raw, `"uid": "${`) {
		t.Error("dashboard references an unresolved datasource variable")
	}
}
