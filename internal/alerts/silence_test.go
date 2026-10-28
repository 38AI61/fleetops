package alerts

import (
	"testing"
	"time"
)

// TestDecideSilenceBoundary: the threshold is strict. Exactly threshold does not
// alert, one second more does, and an existing episode key never alerts again (GF-292).
func TestDecideSilenceBoundary(t *testing.T) {
	const threshold = 10 * time.Minute
	last := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)

	cases := []struct {
		name     string
		now      time.Time
		existing bool
		want     bool
	}{
		{"equal to threshold does not alert", last.Add(threshold), false, false},
		{"one second over threshold alerts", last.Add(threshold + time.Second), false, true},
		{"well over threshold alerts", last.Add(time.Hour), false, true},
		{"existing episode key never alerts again", last.Add(time.Hour), true, false},
		{"fresh report does not alert", last.Add(time.Minute), false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := decideSilence(last, c.now, threshold, c.existing); got != c.want {
				t.Fatalf("decideSilence = %v, want %v", got, c.want)
			}
		})
	}
}
