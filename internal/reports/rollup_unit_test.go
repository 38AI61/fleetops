package reports

import (
	"math"
	"testing"
	"time"
)

// TestDayBoundsLength: fleet-local day bounds on DST and ordinary days (GF-326).
func TestDayBoundsLength(t *testing.T) {
	ny := mustLoad("America/New_York")
	cases := []struct {
		name string
		day  time.Time
		want time.Duration
	}{
		{"spring forward is 23h", civil(2026, time.March, 8), 23 * time.Hour},
		{"fall back is 25h", civil(2026, time.November, 1), 25 * time.Hour},
		{"ordinary day is 24h", civil(2026, time.March, 9), 24 * time.Hour},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			start, end := DayBounds(ny, c.day)
			if got := end.Sub(start); got != c.want {
				t.Fatalf("length = %v, want %v", got, c.want)
			}
		})
	}
}

// TestLocalDayUsesFleetZone: the same instant is a different civil day in two zones.
func TestLocalDayUsesFleetZone(t *testing.T) {
	now := time.Date(2026, time.March, 9, 2, 30, 0, 0, time.UTC) // 22:30 on the 8th in New York
	if got := LocalDay(now, mustLoad("America/New_York")); !got.Equal(civil(2026, time.March, 8)) {
		t.Fatalf("New York local day = %v, want 2026-03-08", got)
	}
	if got := LocalDay(now, time.UTC); !got.Equal(civil(2026, time.March, 9)) {
		t.Fatalf("UTC local day = %v, want 2026-03-09", got)
	}
}

// TestDistanceSphereMatchesHaversine: PostGIS ST_DistanceSphere agrees with the
// Go helper within 0.5 m (Testing depth, R = 6371008.8).
func TestDistanceSphereMatchesHaversine(t *testing.T) {
	f := newFx(t)
	points := [][4]float64{
		{52.5, 13.4, 52.6, 13.5},
		{40, -74, 40.006, -74},
		{-33.9, 151.2, 51.5, -0.12},
		{0, 0, 0, 1},
	}
	for _, p := range points {
		var got float64
		if err := f.db.QueryRow(`
			SELECT ST_DistanceSphere(
			  ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography::geometry,
			  ST_SetSRID(ST_MakePoint($3, $4), 4326)::geography::geometry)`,
			p[1], p[0], p[3], p[2]).Scan(&got); err != nil {
			t.Fatalf("ST_DistanceSphere: %v", err)
		}
		want := haversineM(p[0], p[1], p[2], p[3])
		if math.Abs(got-want) > 0.5 {
			t.Fatalf("points %v: ST_DistanceSphere = %v, haversine = %v", p, got, want)
		}
	}
}

// TestInYesterdayWindow: [00:00, 00:30) local is the only window (GF-320).
func TestInYesterdayWindow(t *testing.T) {
	cases := map[string]struct {
		t    time.Time
		want bool
	}{
		"00:00":    {time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC), true},
		"00:29:59": {time.Date(2026, 3, 9, 0, 29, 59, 0, time.UTC), true},
		"00:30":    {time.Date(2026, 3, 9, 0, 30, 0, 0, time.UTC), false},
		"23:59":    {time.Date(2026, 3, 8, 23, 59, 0, 0, time.UTC), false},
		"noon":     {time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC), false},
	}
	for name, c := range cases {
		if got := inYesterdayWindow(c.t); got != c.want {
			t.Fatalf("%s: inYesterdayWindow = %v, want %v", name, got, c.want)
		}
	}
}
