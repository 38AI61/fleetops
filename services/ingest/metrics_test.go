package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"fleetops/internal/observability"
)

// TestIngestMetricsMux checks the ingest listener serves /metrics and nothing else.
func TestIngestMetricsMux(t *testing.T) {
	srv := httptest.NewServer(metricsMux(observability.NewRegistry()))
	defer srv.Close()

	cases := map[string]int{
		"/metrics":         http.StatusOK,
		"/":                http.StatusNotFound,
		"/ingest/position": http.StatusNotFound,
		"/stream":          http.StatusNotFound,
	}
	for path, want := range cases {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("GET %s = %d, want %d", path, resp.StatusCode, want)
		}
	}
}
