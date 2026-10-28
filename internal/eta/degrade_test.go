package eta

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// TestMetricsPreSeededToZero: every outcome and skip reason exists at zero on registration (GF-93, GF-315).
func TestMetricsPreSeededToZero(t *testing.T) {
	reg := prometheus.NewRegistry()
	NewMetrics(reg)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	want := map[string][]string{
		"fleetops_eta_calls_total":        labelValues(Outcomes),
		"fleetops_eta_rule_skipped_total": skipReasons,
	}
	seen := map[string]map[string]float64{}
	for _, fam := range families {
		series := map[string]float64{}
		for _, m := range fam.GetMetric() {
			if len(m.GetLabel()) == 1 {
				series[m.GetLabel()[0].GetValue()] = m.GetCounter().GetValue()
			}
		}
		seen[fam.GetName()] = series
	}
	for name, labels := range want {
		for _, l := range labels {
			v, ok := seen[name][l]
			if !ok {
				t.Errorf("%s{%s} not pre-seeded", name, l)
				continue
			}
			if v != 0 {
				t.Errorf("%s{%s} = %v at registration, want 0", name, l, v)
			}
		}
	}
	for _, name := range []string{"fleetops_eta_pass_duration_seconds", "fleetops_eta_passes_skipped_total"} {
		if !hasFamily(families, name) {
			t.Errorf("family %s not registered", name)
		}
	}
}

func labelValues(os []Outcome) []string {
	out := make([]string, 0, len(os))
	for _, o := range os {
		out = append(out, string(o))
	}
	return out
}

func hasFamily(families []*dto.MetricFamily, name string) bool {
	for _, f := range families {
		if f.GetName() == name {
			return true
		}
	}
	return false
}

// TestDegradationSkipsWithoutWriteOrAlert: each failure outcome skips the job, keeps the previous
// ETA, raises no eta_slip even though the window has ended, and counts the outcome (GF-314, D-10).
func TestDegradationSkipsWithoutWriteOrAlert(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		closed  bool
		outcome Outcome
	}{
		{name: "unreachable", closed: true, outcome: OutcomeUnreachable},
		{name: "timeout", outcome: OutcomeTimeout, handler: func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}},
		{name: "http_error", outcome: OutcomeHTTPError, handler: func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "down", http.StatusServiceUnavailable)
		}},
		{name: "malformed", outcome: OutcomeMalformed, handler: func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"code":"Ok","routes":[{"distance":1}]}`))
		}},
		{name: "no_route", outcome: OutcomeNoRoute, handler: func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"code":"NoRoute","routes":[]}`))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newLoopFx(t)
			f.seedRules(true)

			var base string
			if tc.closed {
				srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
				base = srv.URL
				srv.Close()
			} else {
				srv := httptest.NewServer(tc.handler)
				t.Cleanup(srv.Close)
				base = srv.URL
			}
			lp := f.loop(NewOSRMRouter(base))

			v := f.vehicle(&vehiclePt)
			job := f.job("assigned", v, f.driver())
			f.setWindowEnd(job, loopBase.Add(-time.Hour))
			if _, err := f.db.Exec(`UPDATE jobs SET eta_minutes = 13 WHERE id = $1`, job); err != nil {
				t.Fatalf("seed eta: %v", err)
			}

			if err := lp.Pass(context.Background()); err != nil {
				t.Fatalf("pass: %v", err)
			}
			if got, ok := f.eta(job); !ok || got != 13 {
				t.Errorf("eta = %d (valid %t), want the previous 13 kept", got, ok)
			}
			if n := f.slipRows(); n != 0 {
				t.Errorf("eta_slip rows = %d, want 0 while OSRM is down", n)
			}
			if got := counter(t, lp.Metrics.Calls, string(tc.outcome)); got != 1 {
				t.Errorf("calls{%s} = %v, want 1", tc.outcome, got)
			}
			if got := counter(t, lp.Metrics.Skipped, reasonOSRMUnavailable); got != 1 {
				t.Errorf("rule_skipped{osrm_unavailable} = %v, want 1", got)
			}
		})
	}
}

// TestDegradationOneLogLinePerPass: many failing jobs in one pass log one line, not one per job (GF-314).
func TestDegradationOneLogLinePerPass(t *testing.T) {
	f := newLoopFx(t)
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	base := srv.URL
	srv.Close()
	lp := f.loop(NewOSRMRouter(base))

	for i := 0; i < 3; i++ {
		v := f.vehicle(&vehiclePt)
		f.job("assigned", v, f.driver())
	}

	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	if err := lp.Pass(context.Background()); err != nil {
		t.Fatalf("pass: %v", err)
	}
	lines := strings.Count(strings.TrimSpace(buf.String()), "\n") + 1
	if got := counter(t, lp.Metrics.Skipped, reasonOSRMUnavailable); got != 3 {
		t.Fatalf("rule_skipped{osrm_unavailable} = %v, want 3 (one per job)", got)
	}
	if lines != 1 || !strings.Contains(buf.String(), "OSRM unavailable") {
		t.Errorf("log output = %q, want one OSRM-unavailable line for the pass", buf.String())
	}
}
