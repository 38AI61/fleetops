package eta

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Pickup and dropoff used by every contract case. Coordinates are Berlin, for readability only.
var (
	testFrom = Point{Lat: 52.520009, Lng: 13.400012}
	testTo   = Point{Lat: 52.530012, Lng: 13.410033}
)

// fixtureServer serves one recorded JSON file with HTTP 200.
func fixtureServer(t *testing.T, file string) *httptest.Server {
	t.Helper()
	body, err := os.ReadFile("testdata/" + file) //nolint:gosec // fixed test directory, file names come from the test table
	if err != nil {
		t.Fatalf("read fixture %s: %v", file, err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
}

// wantOutcome asserts a failed route call carries the taxonomy label.
func wantOutcome(t *testing.T, err error, want Outcome) {
	t.Helper()
	var re *RouteError
	if !errors.As(err, &re) {
		t.Fatalf("error = %v, want *RouteError with outcome %q", err, want)
	}
	if re.Outcome != want {
		t.Fatalf("outcome = %q, want %q", re.Outcome, want)
	}
}

func TestRouteRecordedFixtures(t *testing.T) {
	cases := []struct {
		name    string
		file    string
		minutes int
		outcome Outcome
	}{
		{"happy path 754.3 s rounds up to 13", "route_ok_754s.json", 13, OutcomeOK},
		{"exact multiple of 60 s", "route_ok_exact_600s.json", 10, OutcomeOK},
		{"zero duration is a valid zero", "route_ok_zero.json", 0, OutcomeOK},
		{"code NoRoute", "route_no_route_code.json", 0, OutcomeNoRoute},
		{"empty routes", "route_no_route_empty.json", 0, OutcomeNoRoute},
		{"truncated JSON", "route_malformed_truncated.json", 0, OutcomeMalformed},
		{"duration missing", "route_malformed_no_duration.json", 0, OutcomeMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := fixtureServer(t, tc.file)
			defer srv.Close()

			got, err := NewOSRMRouter(srv.URL).Route(context.Background(), testFrom, testTo)
			if tc.outcome == OutcomeOK {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != tc.minutes {
					t.Fatalf("minutes = %d, want %d", got, tc.minutes)
				}
				return
			}
			wantOutcome(t, err, tc.outcome)
		})
	}
}

// TestRouteRequestShape pins GF-316: lng,lat order, overview=false, one GET to the route path.
func TestRouteRequestShape(t *testing.T) {
	var gotPath, gotQuery, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotMethod = r.URL.Path, r.URL.RawQuery, r.Method
		_, _ = w.Write([]byte(`{"code":"Ok","routes":[{"duration":60}]}`))
	}))
	defer srv.Close()

	if _, err := NewOSRMRouter(srv.URL+"/").Route(context.Background(), testFrom, testTo); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %s, want GET", gotMethod)
	}
	if want := "/route/v1/driving/13.400012,52.520009;13.410033,52.530012"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if gotQuery != "overview=false" {
		t.Errorf("query = %q, want overview=false", gotQuery)
	}
}

func TestRouteHTTPErrorIsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := NewOSRMRouter(srv.URL).Route(context.Background(), testFrom, testTo)
	wantOutcome(t, err, OutcomeHTTPError)
}

func TestRouteStalledServerIsTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	start := time.Now()
	_, err := NewOSRMRouter(srv.URL).Route(context.Background(), testFrom, testTo)
	wantOutcome(t, err, OutcomeTimeout)
	if elapsed := time.Since(start); elapsed > RouteTimeout+time.Second {
		t.Errorf("call took %s, want about %s", elapsed, RouteTimeout)
	}
}

func TestRouteClosedListenerIsUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	base := srv.URL
	srv.Close()

	_, err := NewOSRMRouter(base).Route(context.Background(), testFrom, testTo)
	wantOutcome(t, err, OutcomeUnreachable)
}

// TestRouteOutcomeLabelsAreStable pins the metric label set (GF-304, GF-315).
func TestRouteOutcomeLabelsAreStable(t *testing.T) {
	want := []string{"ok", "unreachable", "timeout", "http_error", "malformed", "no_route"}
	if len(Outcomes) != len(want) {
		t.Fatalf("Outcomes has %d labels, want %d", len(Outcomes), len(want))
	}
	for i, o := range Outcomes {
		if string(o) != want[i] {
			t.Errorf("label %d = %q, want %q", i, o, want[i])
		}
	}
	if strings.ContainsAny(string(OutcomeOK), " -") {
		t.Errorf("label %q is not snake_case", OutcomeOK)
	}
}

func TestMinutesCeilingAndClamp(t *testing.T) {
	cases := []struct {
		seconds float64
		want    int
	}{
		{0, 0},
		{0.5, 1},
		{59.9, 1},
		{60, 1},
		{60.0001, 2},
		{600, 10},
		{754.3, 13},
		{86400, 1440},
		{86401, 1440},
		{1e18, 1440},
	}
	for _, tc := range cases {
		if got := Minutes(tc.seconds); got != tc.want {
			t.Errorf("Minutes(%v) = %d, want %d", tc.seconds, got, tc.want)
		}
	}
}
