package jobs

import (
	"context"
	"database/sql"
	"slices"
	"testing"
	"time"

	"fleetops/internal/auth"
	"fleetops/internal/testinfra"
)

// linkedDriverUser inserts a driver user linked to driverID in the fleet.
func linkedDriverUser(t testing.TB, db *sql.DB, fleetID, driverID int64, email string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(
		`INSERT INTO users (fleet_id, email, password_hash, role, driver_id) VALUES ($1, $2, 'x', 'driver', $3) RETURNING id`,
		fleetID, email, driverID).Scan(&id); err != nil {
		t.Fatalf("insert driver user: %v", err)
	}
	return id
}

// TestDriverResolveAndList covers GF-184 identity and GF-185 today semantics on
// the store. The fleet timezone is Istanbul (UTC+3), so a window at 22:30 UTC on
// the 27th is already the 28th there and must be excluded.
func TestDriverResolveAndList(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	s := &Store{DB: db}
	ctx := context.Background()
	fleet, admin := fleetWithAdmin(t, db, "resolve")
	if _, err := db.Exec(`UPDATE fleets SET timezone = 'Europe/Istanbul' WHERE id = $1`, fleet); err != nil {
		t.Fatalf("set timezone: %v", err)
	}
	otherFleet, _ := fleetWithAdmin(t, db, "resolve-other")
	vehicle := fleetVehicle(t, db, fleet, "RSV-1")
	me := fleetDriver(t, db, fleet, "resolve-me")
	other := fleetDriver(t, db, fleet, "resolve-other-driver")
	meUser := linkedDriverUser(t, db, fleet, me, "resolve-me@example.test")

	// Identity: a linked driver user resolves; an admin with no driver row does not;
	// a driver row in another fleet does not resolve under this fleet's principal.
	if id, ok, err := resolveDriver(ctx, db, auth.Principal{UserID: meUser, FleetID: fleet}); err != nil || !ok || id != me {
		t.Fatalf("resolve linked driver = %d %v %v, want %d true nil", id, ok, err, me)
	}
	if _, ok, err := resolveDriver(ctx, db, auth.Principal{UserID: admin, FleetID: fleet}); err != nil || ok {
		t.Fatalf("resolve admin = %v %v, want not linked", ok, err)
	}
	if _, ok, err := resolveDriver(ctx, db, auth.Principal{UserID: meUser, FleetID: otherFleet}); err != nil || ok {
		t.Fatalf("resolve across fleets = %v %v, want not linked", ok, err)
	}

	// mk creates a job with an optional window and assigns it to the driver.
	mk := func(window *time.Time, driver int64) int64 {
		t.Helper()
		job, err := s.Create(ctx, admin, fleet, NewJob{
			Pickup: Point{Lat: 41.0082, Lng: 28.9784}, Dropoff: Point{Lat: 41.0422, Lng: 29.0083}, WindowStart: window,
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, _, err := s.Assign(ctx, admin, fleet, job.ID, vehicle, driver); err != nil {
			t.Fatalf("assign: %v", err)
		}
		return job.ID
	}
	at := func(v string) *time.Time {
		t.Helper()
		ts, err := time.Parse(time.RFC3339, v)
		if err != nil {
			t.Fatalf("parse %s: %v", v, err)
		}
		return &ts
	}
	advance := func(job int64, to ...State) {
		t.Helper()
		for _, st := range to {
			if _, _, err := s.Transition(ctx, admin, fleet, job, st); err != nil {
				t.Fatalf("advance %d to %s: %v", job, st, err)
			}
		}
	}

	carried := mk(at("2026-10-26T20:00:00Z"), me) // 23:00 on the 26th in Istanbul: carried over, included
	advance(carried, StateAccepted)
	todayJob := mk(at("2026-10-27T09:00:00Z"), me) // 12:00 on the 27th: included
	undated := mk(nil, me)                         // NULL window: included, ordered last
	mk(at("2026-10-28T09:00:00Z"), me)             // the 28th: future, excluded
	mk(at("2026-10-27T22:30:00Z"), me)             // 01:30 on the 28th in Istanbul: future, excluded
	failed := mk(at("2026-10-27T10:00:00Z"), me)   // terminal, excluded
	advance(failed, StateAccepted, StateFailed)
	cancelled := mk(at("2026-10-27T11:00:00Z"), me) // terminal, excluded
	advance(cancelled, StateCancelled)
	theirs := mk(at("2026-10-27T09:00:00Z"), other) // another driver's job: excluded

	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	got, err := s.DriverJobs(ctx, fleet, me, now)
	if err != nil {
		t.Fatalf("driver jobs: %v", err)
	}
	var ids []int64
	for _, j := range got {
		ids = append(ids, j.ID)
	}
	want := []int64{carried, todayJob, undated}
	if !slices.Equal(ids, want) {
		t.Fatalf("driver jobs = %v, want %v", ids, want)
	}

	gotOther, err := s.DriverJobs(ctx, fleet, other, now)
	if err != nil {
		t.Fatalf("driver jobs other: %v", err)
	}
	if len(gotOther) != 1 || gotOther[0].ID != theirs {
		t.Fatalf("other driver jobs = %+v, want only job %d", gotOther, theirs)
	}

	empty, err := s.DriverJobs(ctx, otherFleet, me, now)
	if err != nil {
		t.Fatalf("driver jobs cross-fleet: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("cross-fleet driver jobs = %#v, want empty non-nil slice", empty)
	}
}
