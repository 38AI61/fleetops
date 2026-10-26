package simulator

import (
	"math"
	"reflect"
	"testing"
	"time"

	"pgregory.net/rapid"

	"fleetops/internal/devicesfile"
)

// testWaypoints is a closed loop inside the demo box (GF-142).
func testWaypoints() []devicesfile.Point {
	return []devicesfile.Point{
		{Lat: 52.51, Lng: 13.34}, {Lat: 52.55, Lng: 13.42}, {Lat: 52.48, Lng: 13.47}, {Lat: 52.58, Lng: 13.31},
	}
}

// drawWaypoints draws four points in the demo box, redrawing any loop with two equal consecutive points.
func drawWaypoints(rt *rapid.T) []devicesfile.Point {
	pts := make([]devicesfile.Point, 4)
	for i := range pts {
		pts[i] = devicesfile.Point{
			Lat: rapid.Float64Range(52.45, 52.60).Draw(rt, "lat"),
			Lng: rapid.Float64Range(13.30, 13.50).Draw(rt, "lng"),
		}
	}
	for i := range pts {
		if pts[i] == pts[(i+1)%4] {
			rt.Skip()
		}
	}
	return pts
}

// legAt returns the leg that PositionAt uses for elapsed time t, by the GF-143 rule.
func legAt(r Route, t time.Duration) Leg {
	t %= r.cycle
	for _, l := range r.legs {
		if t < l.start+l.Duration {
			return l
		}
	}
	return r.legs[len(r.legs)-1]
}

// TestPropInterpolatedPointsStayOnLeg: a point on a leg lies inside its lat/lng
// box, and its cross product with the leg vector is near zero (GF-150).
func TestPropInterpolatedPointsStayOnLeg(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		wps := drawWaypoints(rt)
		r, err := NewRoute(rapid.Int64Range(1, 1<<40).Draw(rt, "device"), wps)
		if err != nil {
			rt.Fatalf("NewRoute: %v", err)
		}
		leg := r.legs[rapid.IntRange(0, len(r.legs)-1).Draw(rt, "leg")]
		f := rapid.Float64Range(0, 1).Draw(rt, "f")

		p := interpolate(leg.From, leg.To, f)
		const eps = 1e-12
		if p.Lat < math.Min(leg.From.Lat, leg.To.Lat)-eps || p.Lat > math.Max(leg.From.Lat, leg.To.Lat)+eps ||
			p.Lng < math.Min(leg.From.Lng, leg.To.Lng)-eps || p.Lng > math.Max(leg.From.Lng, leg.To.Lng)+eps {
			rt.Fatalf("point %+v outside the leg's box %+v -> %+v", p, leg.From, leg.To)
		}
		cross := (p.Lng-leg.From.Lng)*(leg.To.Lat-leg.From.Lat) - (p.Lat-leg.From.Lat)*(leg.To.Lng-leg.From.Lng)
		if math.Abs(cross) > 1e-9 {
			rt.Fatalf("cross product %g exceeds 1e-9 deg^2", cross)
		}

		// The same holds for any elapsed time, through PositionAt.
		elapsed := time.Duration(rapid.Float64Range(0, 1).Draw(rt, "frac") * float64(r.cycle))
		fix := r.PositionAt(elapsed)
		l := legAt(r, elapsed)
		if c := (fix.Lng-l.From.Lng)*(l.To.Lat-l.From.Lat) - (fix.Lat-l.From.Lat)*(l.To.Lng-l.From.Lng); math.Abs(c) > 1e-9 {
			rt.Fatalf("PositionAt cross product %g exceeds 1e-9 deg^2", c)
		}
	})
}

// TestPropLegSpeedAndHeadingWithinBounds: every leg speed is in [20, 90], every
// heading is in [0, 360), and realized speed between two points at least 1 s
// apart on one leg is within 1% of the reported speed (GF-143, GF-150).
func TestPropLegSpeedAndHeadingWithinBounds(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		wps := drawWaypoints(rt)
		r, err := NewRoute(rapid.Int64Range(1, 1<<40).Draw(rt, "device"), wps)
		if err != nil {
			rt.Fatalf("NewRoute: %v", err)
		}
		leg := r.legs[rapid.IntRange(0, len(r.legs)-1).Draw(rt, "leg")]
		if leg.SpeedKmh < 20 || leg.SpeedKmh > 90 {
			rt.Fatalf("speed %g outside [20, 90]", leg.SpeedKmh)
		}
		if leg.HeadingDeg < 0 || leg.HeadingDeg >= 360 {
			rt.Fatalf("heading %g outside [0, 360)", leg.HeadingDeg)
		}

		// Two instants on this leg, at least 1 s apart.
		startAt := leg.start + time.Duration(rapid.Float64Range(0, 0.9).Draw(rt, "a")*float64(leg.Duration))
		endAt := leg.start + leg.Duration
		if endAt-startAt < time.Second {
			rt.Skip()
		}
		gap := time.Second + time.Duration(rapid.Float64Range(0, 1).Draw(rt, "gap")*float64(endAt-startAt-time.Second))
		p1 := r.PositionAt(startAt)
		p2 := r.PositionAt(startAt + gap)
		dist := haversineM(devicesfile.Point{Lat: p1.Lat, Lng: p1.Lng}, devicesfile.Point{Lat: p2.Lat, Lng: p2.Lng})
		realized := dist / gap.Seconds() * 3.6
		if math.Abs(realized-leg.SpeedKmh)/leg.SpeedKmh > 0.01 {
			rt.Fatalf("realized %g km/h vs reported %g km/h", realized, leg.SpeedKmh)
		}
	})
}

// TestRouteDeterministic: the same device id gives the same route (GF-142, GF-143).
func TestRouteDeterministic(t *testing.T) {
	a, err := NewRoute(42, testWaypoints())
	if err != nil {
		t.Fatalf("NewRoute: %v", err)
	}
	b, err := NewRoute(42, testWaypoints())
	if err != nil {
		t.Fatalf("NewRoute: %v", err)
	}
	if !reflect.DeepEqual(a.Legs(), b.Legs()) || a.cycle != b.cycle {
		t.Fatal("same device id produced different routes")
	}
	other, err := NewRoute(43, testWaypoints())
	if err != nil {
		t.Fatalf("NewRoute: %v", err)
	}
	if reflect.DeepEqual(a.Legs(), other.Legs()) {
		t.Fatal("different device ids produced identical speeds")
	}
}

func TestNewRouteRejectsBadWaypoints(t *testing.T) {
	if _, err := NewRoute(1, testWaypoints()[:3]); err == nil {
		t.Fatal("three waypoints accepted")
	}
	dup := testWaypoints()
	dup[2] = dup[1]
	if _, err := NewRoute(1, dup); err == nil {
		t.Fatal("equal consecutive waypoints accepted")
	}
}
