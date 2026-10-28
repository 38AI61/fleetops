// Package deploy holds config tests for the compose file, the Prometheus and Grafana
// provisioning, the dashboard JSON and PORTS.md (P22, GF-333). It has no runtime code.
package deploy

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// readFile reads a file under deploy/ and fails the test on error.
func readFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// topLevelBlock returns the lines of the block that starts at the given indentation key
// (for example "  prometheus:") and ends at the next key at the same or lower indentation.
func topLevelBlock(text, key string, indent int) string {
	lines := strings.Split(text, "\n")
	var out []string
	in := false
	pad := strings.Repeat(" ", indent)
	for _, l := range lines {
		if !in {
			if l == pad+key+":" {
				in = true
				out = append(out, l)
			}
			continue
		}
		if l != "" && !strings.HasPrefix(l, pad+" ") {
			break
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// serviceNames lists the keys directly under the top-level services: block.
func serviceNames(text string) []string {
	var names []string
	re := regexp.MustCompile(`^  ([a-z][a-z0-9-]*):\s*$`)
	in := false
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "services:") {
			in = true
			continue
		}
		if in && l != "" && !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "#") {
			break
		}
		if in {
			if m := re.FindStringSubmatch(l); m != nil {
				names = append(names, m[1])
			}
		}
	}
	return names
}

// TestComposeObservabilityBlocks checks the two P22 service blocks: host networking, loopback
// binds on 10014 and 10015, and no other service added or dropped (GF-332, GF-336).
func TestComposeObservabilityBlocks(t *testing.T) {
	text := readFile(t, "docker-compose.yml")

	want := []string{"postgres", "redis", "nats", "osrm", "minio", "prometheus", "grafana"}
	got := serviceNames(text)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("services = %v, want %v", got, want)
	}

	prom := topLevelBlock(text, "prometheus", 2)
	for _, s := range []string{"network_mode: host", "127.0.0.1:10014", "image: prom/prometheus:v3.5.0", "./prometheus:/etc/prometheus:ro"} {
		if !strings.Contains(prom, s) {
			t.Errorf("prometheus block missing %q", s)
		}
	}
	if strings.Contains(prom, "volumes:\n      - postgres") || strings.Contains(prom, "ports:") {
		t.Error("prometheus block has ports or a named volume")
	}

	graf := topLevelBlock(text, "grafana", 2)
	for _, s := range []string{"network_mode: host", "image: grafana/grafana:12.1.0", "GF_SERVER_HTTP_ADDR: 127.0.0.1", `GF_SERVER_HTTP_PORT: "10015"`, "GF_AUTH_ANONYMOUS_ORG_ROLE: Viewer", "./grafana/provisioning:/etc/grafana/provisioning:ro", "./grafana/dashboards:/var/lib/grafana/dashboards:ro"} {
		if !strings.Contains(graf, s) {
			t.Errorf("grafana block missing %q", s)
		}
	}
	if strings.Contains(graf, "ports:") {
		t.Error("grafana block has ports")
	}

	// Neither new service adds a named volume: the top-level volumes list is unchanged (GF-332).
	vols := topLevelBlock(text, "volumes", 0)
	for _, v := range []string{"postgres_data", "redis_data", "osrm_data", "minio_data"} {
		if !strings.Contains(vols, "  "+v+":") {
			t.Errorf("top-level volume %s missing", v)
		}
	}
	if strings.Count(vols, "\n  ") != 4 {
		t.Errorf("top-level volumes = %q, want exactly the four existing volumes", vols)
	}
}

// TestPrometheusConfigTargets checks the three scrape jobs, their /metrics path and loopback targets (GF-333).
func TestPrometheusConfigTargets(t *testing.T) {
	text := readFile(t, "prometheus/prometheus.yml")
	if !strings.Contains(text, "scrape_interval: 15s") {
		t.Error("global scrape_interval is not 15s")
	}
	jobs := map[string]string{
		"fleetops-api":    "127.0.0.1:10010",
		"fleetops-ingest": "127.0.0.1:10008",
		"fleetops-ws-hub": "127.0.0.1:10011",
	}
	for job, target := range jobs {
		i := strings.Index(text, "job_name: "+job+"\n")
		if i < 0 {
			t.Errorf("job %s missing", job)
			continue
		}
		block := text[i:]
		if next := strings.Index(block[1:], "- job_name:"); next >= 0 {
			block = block[:next+1]
		}
		if !strings.Contains(block, "metrics_path: /metrics") {
			t.Errorf("job %s has no metrics_path /metrics", job)
		}
		if !strings.Contains(block, `targets: ["`+target+`"]`) {
			t.Errorf("job %s does not target %s", job, target)
		}
	}
}

// TestGrafanaProvisioningParses checks the datasource and dashboard provisioning files (GF-333).
// They are line-checked because yaml.v3 is only an indirect module here.
func TestGrafanaProvisioningParses(t *testing.T) {
	ds := readFile(t, "grafana/provisioning/datasources/prometheus.yml")
	for _, s := range []string{"apiVersion: 1", "uid: prometheus", "type: prometheus", "url: http://127.0.0.1:10014", "isDefault: true"} {
		if !strings.Contains(ds, s) {
			t.Errorf("datasource provisioning missing %q", s)
		}
	}
	dash := readFile(t, "grafana/provisioning/dashboards/fleetops.yml")
	for _, s := range []string{"apiVersion: 1", "type: file", "path: /var/lib/grafana/dashboards"} {
		if !strings.Contains(dash, s) {
			t.Errorf("dashboard provisioning missing %q", s)
		}
	}
	// Every provisioning file is indented with spaces only, never tabs.
	for _, f := range []string{"grafana/provisioning/datasources/prometheus.yml", "grafana/provisioning/dashboards/fleetops.yml", "prometheus/prometheus.yml"} {
		if strings.Contains(readFile(t, f), "\t") {
			t.Errorf("%s contains a tab", f)
		}
	}
}

// TestPortsDocAssignsObservabilityPorts checks PORTS.md names 10008, 10014 and 10015 and
// removes them from the unassigned line (k).
func TestPortsDocAssignsObservabilityPorts(t *testing.T) {
	text := readFile(t, "PORTS.md")
	for _, s := range []string{"| ingest metrics", "| 10008 |", "| prometheus", "| 10014 |", "| grafana", "| 10015 |", "Unassigned in range: 10000, 10001, 10009, 10016–10018."} {
		if !strings.Contains(text, s) {
			t.Errorf("PORTS.md missing %q", s)
		}
	}
	if strings.Contains(text, "10008, 10009") {
		t.Error("PORTS.md still lists 10008 as unassigned")
	}
}
