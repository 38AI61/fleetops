package reports

import (
	"math"
	"reflect"
	"testing"
	"time"
)

// goldenDay is the fleet-local day of the golden fixture: 2026-03-08 in
// America/New_York, a 23 h day (spring forward at 02:00 local).
var goldenDay = civil(2026, time.March, 8)

// goldenLoc is the timezone of the golden fleet.
var goldenLoc = mustLoad("America/New_York")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// seedGolden builds the golden fixture and returns the fleet id and the driver
// ids. Expected values are in TestGoldenDay. Hand-computed legs for vehicle v1
// (samples in UTC, speed in km/h, lat in degrees, lng -74):
//
//	04:57 39.990 speed 10   23:57 local, before the window: with 05:00 it would be a 180 s leg and must not count
//	05:00 39.991 speed 0    first in-window sample; its leg to 06:00 is 3600 s and counts for neither
//	06:00 40.000 speed 0    ─┐ stationary leg, 60 s, prev speed 0: idle 60, distance 0
//	06:01 40.000 speed 0    ─┘
//	06:02 40.001 speed 30   60 s, prev speed 0: idle 60, distance dLat 0.001
//	06:03 40.002 speed 30   60 s, prev speed 30: moving, distance dLat 0.001
//	06:08 40.004 speed 0.99 300 s, the gap cap is inclusive: moving, distance dLat 0.002
//	06:08:30 40.005 speed 1.00  30 s, prev 0.99 < 1: idle 30, distance dLat 0.001
//	06:09 40.006 speed 0.5  30 s, prev 1.00 is not < 1: moving, distance dLat 0.001
//	06:19 40.007 speed 0    601 s gap: counts for neither distance nor idle
//	06:19:30 40.007 speed 0 30 s, prev 0: idle 30, distance 0
//
// Totals for v1: idle 60+60+30+30 = 180 s; distance = R * 0.006 deg (the legs
// lie on one meridian, so their haversines add up exactly).
//
// Jobs of D1: delivered 07:30 with window 06:00-08:00 (on time), delivered 15:00
// with window 13:00-14:00 (late), delivered 10:00 with no window (counts, no
// denominator), delivered 03:30 UTC on the 9th (23:30 local, counts). A job
// delivered 04:30 UTC on the 8th (23:30 local on the 7th) and a failed job do not count.
// Vehicle v2 has a driverless pairing: its leg counts nothing. D3 is jobs-only:
// one delivery at 10:30 inside its window. D4 has no activity, so no row.
func seedGolden(f *fx) (fleet int64, drivers map[string]int64) {
	fleet = f.fleet("golden-ny", "America/New_York")
	d1 := f.driver(fleet, "Ada")
	d3 := f.driver(fleet, `Cy, "the" jobs-only`)
	f.driver(fleet, "Dee")
	v1 := f.vehicle(fleet)
	v2 := f.vehicle(fleet)
	v3 := f.vehicle(fleet)
	since := civil(2026, time.March, 1)
	f.pair(fleet, v1, d1, since, nil)
	f.pair(fleet, v2, 0, since, nil)

	p := func(v int64, ts time.Time, lat, speed float64) pos {
		return pos{fleet: fleet, vehicle: v, ts: ts, lat: lat, lng: -74, speed: speed}
	}
	f.positions([]pos{
		p(v1, time.Date(2026, time.March, 8, 4, 57, 0, 0, time.UTC), 39.990, 10),
		p(v1, at(5, 0, 0), 39.991, 0),
		p(v1, at(6, 0, 0), 40.000, 0),
		p(v1, at(6, 1, 0), 40.000, 0),
		p(v1, at(6, 2, 0), 40.001, 30),
		p(v1, at(6, 3, 0), 40.002, 30),
		p(v1, at(6, 8, 0), 40.004, 0.99),
		p(v1, at(6, 8, 30), 40.005, 1.00),
		p(v1, at(6, 9, 0), 40.006, 0.5),
		p(v1, at(6, 19, 0), 40.007, 0),
		p(v1, at(6, 19, 30), 40.007, 0),
		p(v2, at(7, 0, 0), 40.000, 30),
		p(v2, at(7, 5, 0), 40.010, 30),
	})

	win1 := [2]time.Time{at(6, 0, 0), at(8, 0, 0)}
	win2 := [2]time.Time{at(13, 0, 0), at(14, 0, 0)}
	win3 := [2]time.Time{at(10, 0, 0), at(11, 0, 0)}
	d7 := at(7, 30, 0)
	d15 := at(15, 0, 0)
	d10 := at(10, 0, 0)
	d10b := at(10, 30, 0)
	nextMorning := time.Date(2026, time.March, 9, 3, 30, 0, 0, time.UTC)
	prevEvening := time.Date(2026, time.March, 8, 4, 30, 0, 0, time.UTC)
	f.job(fleet, "delivered", v1, d1, &win1, &d7)
	f.job(fleet, "delivered", v1, d1, &win2, &d15)
	f.job(fleet, "delivered", v1, d1, nil, &d10)
	f.job(fleet, "delivered", v1, d1, nil, &nextMorning)
	f.job(fleet, "delivered", v1, d1, nil, &prevEvening)
	f.job(fleet, "failed", v1, d1, nil, nil)
	f.job(fleet, "delivered", v3, d3, &win3, &d10b)

	return fleet, map[string]int64{"d1": d1, "d3": d3}
}

// TestGoldenDay rolls up the golden fixture on the 23 h DST day and checks each
// driver's row. Counts are exact. Distance is within 0.5 m of the Go haversine.
func TestGoldenDay(t *testing.T) {
	f := newFx(t)
	fleet, drv := seedGolden(f)
	start, end := DayBounds(goldenLoc, goldenDay)
	if got := end.Sub(start); got != 23*time.Hour {
		t.Fatalf("day length = %v, want 23h", got)
	}

	f.rollup(fleet, goldenLoc, goldenDay)
	got := f.stats(fleet, goldenDay)

	// v1 legs cover 40.000 to 40.006 on one meridian.
	wantD1Dist := haversineM(40.000, -74, 40.006, -74)
	want := []stat{
		{DriverID: drv["d1"], JobsCompleted: 4, OnTimeCount: 1, OnTimeDenom: 2, IdleS: 180},
		{DriverID: drv["d3"], JobsCompleted: 1, OnTimeCount: 1, OnTimeDenom: 1, IdleS: 0},
	}
	if len(got) != len(want) {
		t.Fatalf("rows = %+v, want %d rows", got, len(want))
	}
	for i := range want {
		g := got[i]
		distWant := 0.0
		if want[i].DriverID == drv["d1"] {
			distWant = wantD1Dist
		}
		if math.Abs(g.DistanceM-distWant) > 0.5 {
			t.Fatalf("driver %d distance = %v, want %v within 0.5 m", g.DriverID, g.DistanceM, distWant)
		}
		g.DistanceM = 0
		w := want[i]
		w.DistanceM = 0
		if !reflect.DeepEqual(g, w) {
			t.Fatalf("row %d = %+v, want %+v", i, g, w)
		}
	}
}
