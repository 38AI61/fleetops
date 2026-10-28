package main

import (
	"net/http"
	"sort"
	"strings"
	"testing"
)

// typeLines returns the sorted "# TYPE" lines of a scrape. Sample values move between
// scrapes, so the family set is what must match.
func typeLines(body []byte) []string {
	var out []string
	for _, l := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(l, "# TYPE ") {
			out = append(out, l)
		}
	}
	sort.Strings(out)
	return out
}

// TestAPIMetricsOutsideAuth checks /metrics is served with no JWT and with a driver JWT,
// and that both scrapes expose the same families (GF-329).
func TestAPIMetricsOutsideAuth(t *testing.T) {
	h := startServer(t)
	f := h.fleet(t, "metrics-fleet")
	h.user(t, f, "metrics-driver@metrics.example", "driver")
	tok := h.login(t, "metrics-driver@metrics.example")

	anon := h.do(t, http.MethodGet, "/metrics", "", "", nil)
	drv := h.do(t, http.MethodGet, "/metrics", tok, "", nil)
	if anon.status != http.StatusOK {
		t.Fatalf("GET /metrics without JWT = %d, want 200", anon.status)
	}
	if drv.status != http.StatusOK {
		t.Fatalf("GET /metrics with driver JWT = %d, want 200", drv.status)
	}
	a, d := typeLines(anon.body), typeLines(drv.body)
	if len(a) == 0 || strings.Join(a, "\n") != strings.Join(d, "\n") {
		t.Fatalf("scrape families differ: anon %d, driver %d", len(a), len(d))
	}
	if !strings.Contains(string(anon.body), "# TYPE go_goroutines") {
		t.Fatal("api /metrics missing Go collector")
	}
}
