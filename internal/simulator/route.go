// Package simulator drives straight-line legs between the seeded waypoints in
// devices.json and POSTs them to /ingest/position at a fixed rate (D-12, P8).
// It reads no database, NATS, or Redis, and calls no routing service.
package simulator

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"time"

	"fleetops/internal/devicesfile"
)

// earthRadiusM is the mean Earth radius used by the haversine length.
const earthRadiusM = 6371008.8

// routeStream is the second PCG seed for leg speeds. The device id is the
// first, so the same device always draws the same speeds (GF-143).
const routeStream = 0x51ab_c0de_0007_0001

// Speed bounds for a leg, km/h (GF-143). Ingest accepts [0, 300].
const (
	minLegSpeedKmh = 20.0
	maxLegSpeedKmh = 90.0
)

// Fix is one position on a route, with the speed and heading of its leg.
type Fix struct {
	Lat        float64
	Lng        float64
	SpeedKmh   float64
	HeadingDeg float64
}

// Leg is one straight segment. Its duration is its haversine length divided by its speed (GF-143).
type Leg struct {
	From       devicesfile.Point
	To         devicesfile.Point
	SpeedKmh   float64
	HeadingDeg float64
	Duration   time.Duration

	start time.Duration // offset of this leg within the cycle
}

// Route is a closed loop of legs. w0 to w1 to w2 to w3 back to w0 (GF-142).
type Route struct {
	legs  []Leg
	cycle time.Duration
}

// NewRoute builds the leg model for one device (GF-142, GF-143). The waypoints
// come from devices.json. The simulator does not invent them. Speeds come
// from a PCG seeded by the device id, so the same id gives the same route.
func NewRoute(deviceID int64, wps []devicesfile.Point) (Route, error) {
	if len(wps) != devicesfile.WaypointsPerDevice {
		return Route{}, fmt.Errorf("route: want %d waypoints, got %d", devicesfile.WaypointsPerDevice, len(wps))
	}
	rng := rand.New(rand.NewPCG(uint64(deviceID), routeStream)) //nolint:gosec // seeded simulation speeds, not a credential
	r := Route{legs: make([]Leg, 0, len(wps))}
	for i := range wps {
		from, to := wps[i], wps[(i+1)%len(wps)]
		length := haversineM(from, to)
		if length <= 0 {
			return Route{}, errors.New("route: consecutive waypoints are equal")
		}
		v := minLegSpeedKmh + (maxLegSpeedKmh-minLegSpeedKmh)*rng.Float64()
		dur := time.Duration(length / (v / 3.6) * float64(time.Second))
		r.legs = append(r.legs, Leg{
			From:       from,
			To:         to,
			SpeedKmh:   v,
			HeadingDeg: initialBearingDeg(from, to),
			Duration:   dur,
			start:      r.cycle,
		})
		r.cycle += dur
	}
	return r, nil
}

// PositionAt returns the position at elapsed time since the route epoch (GF-143).
// It takes elapsed modulo the cycle, finds the leg by cumulative duration, and
// interpolates linearly in lat and lng, so points stay collinear with their leg.
func (r Route) PositionAt(elapsed time.Duration) Fix {
	t := elapsed % r.cycle
	if t < 0 {
		t += r.cycle
	}
	leg := r.legs[len(r.legs)-1]
	for _, l := range r.legs {
		if t < l.start+l.Duration {
			leg = l
			break
		}
	}
	f := float64(t-leg.start) / float64(leg.Duration)
	p := interpolate(leg.From, leg.To, f)
	return Fix{Lat: p.Lat, Lng: p.Lng, SpeedKmh: leg.SpeedKmh, HeadingDeg: leg.HeadingDeg}
}

// Legs returns a copy of the route's legs, in order, for tests and reporting.
func (r Route) Legs() []Leg {
	return append([]Leg(nil), r.legs...)
}

// interpolate returns the point at fraction f along the straight lat/lng segment a to b.
func interpolate(a, b devicesfile.Point, f float64) devicesfile.Point {
	return devicesfile.Point{
		Lat: a.Lat + f*(b.Lat-a.Lat),
		Lng: a.Lng + f*(b.Lng-a.Lng),
	}
}

// haversineM is the great-circle distance in metres.
func haversineM(a, b devicesfile.Point) float64 {
	phi1, phi2 := rad(a.Lat), rad(b.Lat)
	dPhi := rad(b.Lat - a.Lat)
	dLam := rad(b.Lng - a.Lng)
	h := math.Sin(dPhi/2)*math.Sin(dPhi/2) + math.Cos(phi1)*math.Cos(phi2)*math.Sin(dLam/2)*math.Sin(dLam/2)
	return 2 * earthRadiusM * math.Asin(math.Min(1, math.Sqrt(h)))
}

// initialBearingDeg is the great-circle bearing from a to b, normalized to [0, 360).
func initialBearingDeg(a, b devicesfile.Point) float64 {
	phi1, phi2 := rad(a.Lat), rad(b.Lat)
	dLam := rad(b.Lng - a.Lng)
	y := math.Sin(dLam) * math.Cos(phi2)
	x := math.Cos(phi1)*math.Sin(phi2) - math.Sin(phi1)*math.Cos(phi2)*math.Cos(dLam)
	deg := math.Mod(math.Atan2(y, x)*180/math.Pi+360, 360)
	if deg >= 360 {
		deg = 0
	}
	return deg
}

func rad(d float64) float64 { return d * math.Pi / 180 }
