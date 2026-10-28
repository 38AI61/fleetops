package reports

import (
	"reflect"
	"testing"
	"time"
)

// TestAttributionPairingChange: a vehicle moves from D1 to D2 at 10:30 UTC.
// Samples at 10:26, 10:30, 10:34, 10:38, 10:42 (lat 40.000 to 40.004, speed 30,
// UTC fleet). The leg 10:26 to 10:30 opens before the change, so it goes wholly
// to D1 even though the change falls inside it (GF-321). The leg opening at
// 10:30 is attributed to D2, because started_at is inclusive and ended_at is
// exclusive. D1 gets one leg, D2 gets three.
func TestAttributionPairingChange(t *testing.T) {
	f := newFx(t)
	fleet := f.fleet("attr", "UTC")
	d1 := f.driver(fleet, "Ann")
	d2 := f.driver(fleet, "Ben")
	v := f.vehicle(fleet)
	change := at(10, 30, 0)
	f.pair(fleet, v, d1, civil(2026, time.March, 1), &change)
	f.pair(fleet, v, d2, change, nil)

	var ps []pos
	for i, ts := range []time.Time{at(10, 26, 0), at(10, 30, 0), at(10, 34, 0), at(10, 38, 0), at(10, 42, 0)} {
		ps = append(ps, pos{fleet: fleet, vehicle: v, ts: ts, lat: 40.000 + float64(i)*0.001, lng: -74, speed: 30})
	}
	f.positions(ps)

	day := civil(2026, time.March, 8)
	f.rollup(fleet, time.UTC, day)
	got := f.stats(fleet, day)

	want := []stat{
		{DriverID: d1, DistanceM: haversineM(40.000, -74, 40.001, -74)},
		{DriverID: d2, DistanceM: haversineM(40.001, -74, 40.004, -74)},
	}
	if len(got) != len(want) {
		t.Fatalf("rows = %+v, want %+v", got, want)
	}
	for i := range want {
		if d := got[i].DistanceM - want[i].DistanceM; d > 0.5 || d < -0.5 {
			t.Fatalf("driver %d distance = %v, want %v", want[i].DriverID, got[i].DistanceM, want[i].DistanceM)
		}
		got[i].DistanceM, want[i].DistanceM = 0, 0
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %+v, want %+v", got, want)
	}
}
