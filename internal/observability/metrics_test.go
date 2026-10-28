package observability

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMetricsHandlerServesRegistry checks the handler serves the Go and process
// collectors from the registry it was built on.
func TestMetricsHandlerServesRegistry(t *testing.T) {
	srv := httptest.NewServer(Handler(NewRegistry()))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	for _, want := range []string{"# TYPE go_goroutines", "process_cpu_seconds_total"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("body missing %q", want)
		}
	}
}
