// Package eta recomputes job ETAs from OSRM and raises eta_slip alerts (P20, D-34, D-35).
package eta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RouteTimeout bounds one /route call, end to end (GF-303). There are no retries (GF-303).
const RouteTimeout = 2 * time.Second

// MaxETAMinutes is the jobs.eta_minutes CHECK ceiling (migration 009). Values clamp to it (GF-304, GF-309).
const MaxETAMinutes = 1440

// maxRouteBody bounds the OSRM response read. overview=false keeps real replies far below this.
const maxRouteBody = 1 << 20

// Outcome is one result of a /route call. The string is the metric label (GF-304).
type Outcome string

const (
	OutcomeOK          Outcome = "ok"
	OutcomeUnreachable Outcome = "unreachable"
	OutcomeTimeout     Outcome = "timeout"
	OutcomeHTTPError   Outcome = "http_error"
	OutcomeMalformed   Outcome = "malformed"
	OutcomeNoRoute     Outcome = "no_route"
)

// Outcomes lists every label value, in a fixed order, for metric pre-seeding (GF-315).
var Outcomes = []Outcome{OutcomeOK, OutcomeUnreachable, OutcomeTimeout, OutcomeHTTPError, OutcomeMalformed, OutcomeNoRoute}

// Point is a WGS84 coordinate.
type Point struct {
	Lat float64
	Lng float64
}

// Router returns the driving time in whole minutes from one point to another.
type Router interface {
	Route(ctx context.Context, from, to Point) (int, error)
}

// RouteError is a failed /route call. Outcome is its taxonomy label.
type RouteError struct {
	Outcome Outcome
}

func (e *RouteError) Error() string {
	return "osrm route " + string(e.Outcome)
}

// OSRMRouter calls an OSRM HTTP server (GF-303, GF-316).
type OSRMRouter struct {
	BaseURL string
	HTTP    *http.Client
}

// NewOSRMRouter returns a router with the 2 s client timeout.
func NewOSRMRouter(baseURL string) *OSRMRouter {
	return &OSRMRouter{BaseURL: baseURL, HTTP: &http.Client{Timeout: RouteTimeout}}
}

// Route returns ceil(duration/60) clamped to [0, 1440] (GF-304).
func (r *OSRMRouter) Route(ctx context.Context, from, to Point) (int, error) {
	// GF-316: lng,lat order, overview=false, no steps, no alternatives.
	u := fmt.Sprintf("%s/route/v1/driving/%s;%s?overview=false",
		strings.TrimRight(r.BaseURL, "/"), coord(from), coord(to))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, &RouteError{Outcome: OutcomeMalformed}
	}
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return 0, &RouteError{Outcome: transportOutcome(err)}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return 0, &RouteError{Outcome: OutcomeHTTPError}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRouteBody))
	if err != nil {
		return 0, &RouteError{Outcome: transportOutcome(err)}
	}
	return parseRoute(body)
}

// osrmReply is the subset of the OSRM /route reply the client reads.
type osrmReply struct {
	Code   string `json:"code"`
	Routes []struct {
		Duration *float64 `json:"duration"`
	} `json:"routes"`
}

// parseRoute applies GF-304 to a 200 body. Code and route count are checked before the
// duration, so a NoRoute reply is no_route even when it carries no duration.
func parseRoute(body []byte) (int, error) {
	var reply osrmReply
	if err := json.Unmarshal(body, &reply); err != nil {
		return 0, &RouteError{Outcome: OutcomeMalformed}
	}
	if reply.Code != "Ok" || len(reply.Routes) == 0 {
		return 0, &RouteError{Outcome: OutcomeNoRoute}
	}
	d := reply.Routes[0].Duration
	if d == nil || math.IsNaN(*d) || math.IsInf(*d, 0) || *d < 0 {
		return 0, &RouteError{Outcome: OutcomeMalformed}
	}
	return Minutes(*d), nil
}

// Minutes converts a duration in seconds to whole minutes, rounded up and clamped to
// [0, MaxETAMinutes] (GF-304). The clamp runs on the float so a huge duration cannot overflow int.
func Minutes(seconds float64) int {
	m := math.Ceil(seconds / 60)
	switch {
	case m < 0:
		return 0
	case m > MaxETAMinutes:
		return MaxETAMinutes
	}
	return int(m)
}

// transportOutcome maps a transport error to the taxonomy: a timeout (client or context
// deadline) is timeout, and any other failure (refused, DNS, reset) is unreachable.
func transportOutcome(err error) Outcome {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return OutcomeTimeout
	}
	return OutcomeUnreachable
}

// coord formats one point as lng,lat with six decimals (about 0.1 m).
func coord(p Point) string {
	return strconv.FormatFloat(p.Lng, 'f', 6, 64) + "," + strconv.FormatFloat(p.Lat, 'f', 6, 64)
}
