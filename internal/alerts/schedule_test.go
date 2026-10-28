package alerts

import (
	"testing"
	"time"

	"fleetops/internal/geofence"
)

// mustLoc loads a zone for a test table and fails the build on a typo.
func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation %s: %v", name, err)
	}
	return loc
}

// wall builds an instant from a wall-clock reading in loc.
func wall(loc *time.Location, s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
	if err != nil {
		panic(err)
	}
	return t
}

// Dates used below (2026): 25 Oct Sun, 26 Oct Mon, 31 Oct Sat, 1 Nov Sun, 2 Nov Mon.
func TestInScheduleWindows(t *testing.T) {
	utc := time.UTC
	weekdays := &geofence.Schedule{Days: []int{1, 2, 3, 4, 5}, From: "08:00", To: "18:00", Tz: "UTC"}
	nightMon := &geofence.Schedule{Days: []int{1}, From: "22:00", To: "06:00", Tz: "UTC"}
	nightSun := &geofence.Schedule{Days: []int{7}, From: "22:00", To: "06:00", Tz: "UTC"}
	nightMonOnly := &geofence.Schedule{Days: []int{1}, From: "22:00", To: "06:00", Tz: "UTC"}

	cases := []struct {
		name string
		s    *geofence.Schedule
		at   time.Time
		want bool
	}{
		{"same-day inside", weekdays, wall(utc, "2026-10-26 10:00"), true},
		{"from is inclusive", weekdays, wall(utc, "2026-10-26 08:00"), true},
		{"before from", weekdays, wall(utc, "2026-10-26 07:59"), false},
		{"to is exclusive", weekdays, wall(utc, "2026-10-26 18:00"), false},
		{"last minute before to", weekdays, wall(utc, "2026-10-26 17:59"), true},
		{"Saturday not an active day", weekdays, wall(utc, "2026-10-31 10:00"), false},
		{"Sunday not an active day", weekdays, wall(utc, "2026-10-25 10:00"), false},

		{"overnight, Monday after from", nightMon, wall(utc, "2026-10-26 23:00"), true},
		{"overnight, Tuesday before to", nightMon, wall(utc, "2026-10-27 03:00"), true},
		{"overnight, to is exclusive", nightMon, wall(utc, "2026-10-27 06:00"), false},
		{"overnight, Monday early morning", nightMon, wall(utc, "2026-10-26 03:00"), false},
		{"overnight, Tuesday night", nightMon, wall(utc, "2026-10-27 23:00"), false},

		// Weekday wrap: a Sunday window carries into Monday morning (ISO 7 -> 1).
		{"wrap Sunday night", nightSun, wall(utc, "2026-11-01 23:00"), true},
		{"wrap Monday before to", nightSun, wall(utc, "2026-11-02 03:00"), true},
		{"wrap Monday after to", nightSun, wall(utc, "2026-11-02 06:00"), false},
		{"wrap needs Sunday active", nightMonOnly, wall(utc, "2026-11-01 03:00"), false},

		{"NULL schedule is never outside", nil, wall(utc, "2026-10-31 03:00"), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := InSchedule(c.s, c.at); got != c.want {
				t.Fatalf("InSchedule(%v, %s) = %v, want %v", c.s, c.at, got, c.want)
			}
		})
	}
}

// TestInScheduleDST: the wall clock is read in schedule.tz, and the repeated
// fall-back hour gives the same answer for both occurrences (GF-296).
func TestInScheduleDST(t *testing.T) {
	sunNY := &geofence.Schedule{Days: []int{7}, From: "01:00", To: "02:00", Tz: "America/New_York"}
	sunNY3 := &geofence.Schedule{Days: []int{7}, From: "02:00", To: "03:00", Tz: "America/New_York"}
	sunLondon := &geofence.Schedule{Days: []int{7}, From: "01:00", To: "02:00", Tz: "Europe/London"}
	sunLondonDawn := &geofence.Schedule{Days: []int{7}, From: "00:00", To: "02:00", Tz: "Europe/London"}

	cases := []struct {
		name string
		s    *geofence.Schedule
		at   time.Time
		want bool
	}{
		// New York fall-back, Sunday 1 Nov 2026: 01:30 happens twice (EDT then EST).
		{"NY fall-back, first 01:30 EDT", sunNY, time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC), true},
		{"NY fall-back, second 01:30 EST", sunNY, time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC), true},
		{"NY fall-back, 02:30 EST is outside", sunNY, time.Date(2026, 11, 1, 7, 30, 0, 0, time.UTC), false},
		// New York spring-forward, Sunday 8 Mar 2026: 02:00 EST jumps to 03:00 EDT.
		{"NY spring-forward, 01:59 EST inside", sunNY, time.Date(2026, 3, 8, 6, 59, 0, 0, time.UTC), true},
		{"NY spring-forward, 03:00 EDT outside", sunNY, time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC), false},
		{"NY spring-forward, 03:30 EDT outside a 02:00-03:00 window", sunNY3, time.Date(2026, 3, 8, 7, 30, 0, 0, time.UTC), false},
		// London spring-forward, Sunday 29 Mar 2026: 01:00 GMT jumps to 02:00 BST.
		{"London spring-forward, 00:30 GMT inside", sunLondonDawn, time.Date(2026, 3, 29, 0, 30, 0, 0, time.UTC), true},
		{"London spring-forward, 01:00 UTC is 02:00 BST, outside", sunLondonDawn, time.Date(2026, 3, 29, 1, 0, 0, 0, time.UTC), false},
		// London fall-back, Sunday 25 Oct 2026: 02:00 BST falls back to 01:00 GMT.
		{"London fall-back, first 01:30 BST", sunLondon, time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC), true},
		{"London fall-back, second 01:30 GMT", sunLondon, time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC), true},
		{"London fall-back, 02:30 GMT outside", sunLondon, time.Date(2026, 10, 25, 2, 30, 0, 0, time.UTC), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := InSchedule(c.s, c.at); got != c.want {
				t.Fatalf("InSchedule = %v, want %v (local %s)", got, c.want, c.at.In(mustLoc(t, c.s.Tz)))
			}
		})
	}
}
