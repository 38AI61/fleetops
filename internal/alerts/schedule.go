package alerts

import (
	"time"

	"fleetops/internal/geofence"
)

// InSchedule reports whether t is inside the schedule window (GF-296). A nil
// schedule is never outside, so it returns true. The wall clock is read in
// schedule.tz (t.In), not in the fleet zone. Both occurrences of a repeated
// fall-back hour get the same answer. A skipped spring-forward hour cannot be
// observed. P17 validates stored schedules (zone, times, days), so the
// fallback branches for a malformed schedule return true (never outside).
func InSchedule(s *geofence.Schedule, t time.Time) bool {
	if s == nil {
		return true
	}
	loc, err := time.LoadLocation(s.Tz)
	if err != nil {
		return true
	}
	from, ok := clockMinutes(s.From)
	if !ok {
		return true
	}
	to, ok := clockMinutes(s.To)
	if !ok {
		return true
	}

	local := t.In(loc)
	mins := local.Hour()*60 + local.Minute()
	today := isoWeekday(local.Weekday())
	switch {
	case from < to:
		// Same-day window: [from, to) on an active weekday.
		return dayActive(s.Days, today) && mins >= from && mins < to
	case from > to:
		// Overnight window: from to midnight on an active weekday, then
		// midnight to to on the day after an active weekday (weekday wraps 7 -> 1).
		prev := today - 1
		if prev == 0 {
			prev = 7
		}
		return (dayActive(s.Days, today) && mins >= from) ||
			(dayActive(s.Days, prev) && mins < to)
	}
	// from == to is rejected by P17 (GF-265). An empty window is never inside.
	return false
}

// clockMinutes parses an HH:MM wall-clock time into minutes after midnight.
func clockMinutes(hhmm string) (int, bool) {
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}

// isoWeekday maps Go's Sunday-based weekday to ISO 1 (Monday) through 7 (Sunday) (GF-296).
func isoWeekday(w time.Weekday) int {
	if w == time.Sunday {
		return 7
	}
	return int(w)
}

func dayActive(days []int, d int) bool {
	for _, x := range days {
		if x == d {
			return true
		}
	}
	return false
}
