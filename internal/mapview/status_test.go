package mapview

import (
	"testing"
	"time"
)

// TestDeriveStatusOfflineEdge is the 120 s edge with a fake clock (GF-131).
// The threshold is "older than ttl": exactly ttl is fresh, ttl plus 1 ns is offline.
func TestDeriveStatusOfflineEdge(t *testing.T) {
	const ttl = 120 * time.Second
	now := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		age  time.Duration
		want DisplayStatus
	}{
		{"fresh", 5 * time.Second, StatusIdle},
		{"exactly_120s_not_offline", ttl, StatusIdle},
		{"120s_plus_1ns_offline", ttl + time.Nanosecond, StatusOffline},
		{"negative_skew_not_offline", -3 * time.Second, StatusIdle},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			serverTS := now.Add(-tc.age)
			if got := DeriveStatus(serverTS, now, ttl, JobNone); got != tc.want {
				t.Fatalf("age %s: got %s, want %s", tc.age, got, tc.want)
			}
		})
	}
}

// TestDeriveStatusPrecedence covers D-27 order and the TTL bounds of GF-132.
func TestDeriveStatusPrecedence(t *testing.T) {
	now := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	fresh := now.Add(-time.Second)
	stale := now.Add(-10 * time.Minute)

	cases := []struct {
		name     string
		serverTS time.Time
		ttl      time.Duration
		job      JobState
		want     DisplayStatus
	}{
		{"offline beats en_route", stale, 120 * time.Second, JobEnRoute, StatusOffline},
		{"offline beats picked_up", stale, 120 * time.Second, JobPickedUp, StatusOffline},
		{"en_route", fresh, 120 * time.Second, JobEnRoute, StatusEnRoute},
		{"picked_up is en_route", fresh, 120 * time.Second, JobPickedUp, StatusEnRoute},
		{"other is idle", fresh, 120 * time.Second, JobOther, StatusIdle},
		{"no job is idle", fresh, 120 * time.Second, JobNone, StatusIdle},
		{"ttl 30s min: 30s fresh", now.Add(-30 * time.Second), 30 * time.Second, JobNone, StatusIdle},
		{"ttl 30s min: 30s+1ns offline", now.Add(-30*time.Second - time.Nanosecond), 30 * time.Second, JobNone, StatusOffline},
		{"ttl 600s max: 600s fresh", now.Add(-600 * time.Second), 600 * time.Second, JobEnRoute, StatusEnRoute},
		{"ttl 600s max: 600s+1ns offline", now.Add(-600*time.Second - time.Nanosecond), 600 * time.Second, JobEnRoute, StatusOffline},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeriveStatus(tc.serverTS, now, tc.ttl, tc.job); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}
