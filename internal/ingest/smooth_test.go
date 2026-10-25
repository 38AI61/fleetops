package ingest

import (
	"math"
	"testing"
	"time"
)

func TestIsStaleBoundary(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	if isStale(now.Add(-60*time.Second), now) {
		t.Error("age exactly 60 s must be kept")
	}
	if !isStale(now.Add(-60*time.Second-time.Microsecond), now) {
		t.Error("age 60 s + 1 µs must be stale")
	}
	if isStale(now.Add(5*time.Second), now) {
		t.Error("a future server_ts must be kept")
	}
}

func TestHeadingEMAWrapsAcrossNorth(t *testing.T) {
	cases := []struct{ prev, raw, want float64 }{
		{359, 1, 0},
		{1, 359, 0},
	}
	for _, tc := range cases {
		got := headingStep(tc.prev, tc.raw)
		if math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("headingStep(%v, %v) = %v, want %v", tc.prev, tc.raw, got, tc.want)
		}
		if got < 0 || got >= 360 {
			t.Errorf("headingStep(%v, %v) = %v outside [0, 360)", tc.prev, tc.raw, got)
		}
	}
}

func TestHeadingEMAHalfStep(t *testing.T) {
	if got := headingStep(90, 110); math.Abs(got-100) > 1e-9 {
		t.Fatalf("headingStep(90, 110) = %v, want 100", got)
	}
}

func TestHeadingEMASeedsFromFirstMessage(t *testing.T) {
	ts := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	s := smoothState{}.next(120, ts)
	if s.heading != 120 || !s.at.Equal(ts) {
		t.Fatalf("seed: got heading %v at %v, want 120 at %v", s.heading, s.at, ts)
	}
	replay := s.next(300, ts)
	if replay != s {
		t.Fatalf("a sample at the same server_ts must not move the state: %+v", replay)
	}
}
