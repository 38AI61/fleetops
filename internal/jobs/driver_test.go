package jobs

import (
	"context"
	"database/sql"
	"errors"
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

// TestDriverStoreTransition covers the driver transition path (GF-187..GF-191, D-24):
// each driver edge, ownership, fail persistence, replay, illegal edges, target
// validation, and event counts per job.
func TestDriverStoreTransition(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	s := &Store{DB: db}
	ctx := context.Background()
	fleet, admin := fleetWithAdmin(t, db, "drvtx")
	otherFleet, otherAdmin := fleetWithAdmin(t, db, "drvtx-other")
	vehicle := fleetVehicle(t, db, fleet, "DT-1")
	me := fleetDriver(t, db, fleet, "drvtx-me")
	other := fleetDriver(t, db, fleet, "drvtx-other-driver")
	meUser := linkedDriverUser(t, db, fleet, me, "drvtx-me@example.test")

	// fresh creates a job and assigns it to driver, returning its id.
	fresh := func(driver int64) int64 {
		t.Helper()
		job, err := s.Create(ctx, admin, fleet, newTestJob())
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, _, err := s.Assign(ctx, admin, fleet, job.ID, vehicle, driver); err != nil {
			t.Fatalf("assign: %v", err)
		}
		return job.ID
	}
	// advance moves a job as the dispatcher, for fixtures that need a driver-side state.
	advance := func(job int64, to ...State) {
		t.Helper()
		for _, st := range to {
			if _, _, err := s.Transition(ctx, admin, fleet, job, st); err != nil {
				t.Fatalf("advance %d to %s: %v", job, st, err)
			}
		}
	}
	events := func(job int64) int {
		return rowCount(t, db, `SELECT count(*) FROM job_events WHERE job_id = $1`, job)
	}
	stateOf := func(job int64) State {
		t.Helper()
		j, err := s.Get(ctx, fleet, job)
		if err != nil {
			t.Fatalf("get %d: %v", job, err)
		}
		return j.State
	}
	reasonOf := func(job int64) *string {
		t.Helper()
		j, err := s.Get(ctx, fleet, job)
		if err != nil {
			t.Fatalf("get %d: %v", job, err)
		}
		return j.FailureReason
	}
	lastActor := func(job int64) int64 {
		t.Helper()
		var a int64
		if err := db.QueryRow(`SELECT actor_id FROM job_events WHERE job_id = $1 ORDER BY id DESC LIMIT 1`, job).Scan(&a); err != nil {
			t.Fatalf("last actor: %v", err)
		}
		return a
	}
	reason := func(v string) *string { return &v }

	// Happy path: assigned -> accepted -> en_route -> picked_up -> failed, one event each.
	job := fresh(me)
	if n := events(job); n != 2 {
		t.Fatalf("events after assign = %d, want 2 (create + assign)", n)
	}
	for _, to := range []State{StateAccepted, StateEnRoute, StatePickedUp} {
		before := events(job)
		ch, out, err := s.DriverTransition(ctx, meUser, fleet, job, me, to, nil)
		if err != nil || out != Applied {
			t.Fatalf("driver -> %s: outcome %v err %v, want Applied", to, out, err)
		}
		if ch.To != to || ch.Job.State != to {
			t.Fatalf("driver -> %s: change = %+v", to, ch)
		}
		if events(job) != before+1 {
			t.Fatalf("driver -> %s: events did not grow by one", to)
		}
		if a := lastActor(job); a != meUser {
			t.Fatalf("driver -> %s: actor = %d, want user %d (GF-180)", to, a, meUser)
		}
	}
	ch, out, err := s.DriverTransition(ctx, meUser, fleet, job, me, StateFailed, reason("gate locked"))
	if err != nil || out != Applied {
		t.Fatalf("picked_up -> failed: outcome %v err %v, want Applied", out, err)
	}
	if ch.Job.FailureReason == nil || *ch.Job.FailureReason != "gate locked" {
		t.Fatalf("failure_reason = %v, want gate locked", ch.Job.FailureReason)
	}
	if n := events(job); n != 6 {
		t.Fatalf("events after 4 applied moves = %d, want 6", n)
	}

	// fail from accepted and from en_route are also edges.
	for _, from := range []State{StateAccepted, StateEnRoute} {
		j := fresh(me)
		advance(j, StateAccepted)
		if from == StateEnRoute {
			advance(j, StateEnRoute)
		}
		if _, out, err := s.DriverTransition(ctx, meUser, fleet, j, me, StateFailed, reason("no access")); err != nil || out != Applied {
			t.Fatalf("%s -> failed: outcome %v err %v, want Applied", from, out, err)
		}
		if stateOf(j) != StateFailed {
			t.Fatalf("%s -> failed: state = %s", from, stateOf(j))
		}
	}

	// Replay: accepted again is Replayed with no event.
	rep := fresh(me)
	advance(rep, StateAccepted)
	before := events(rep)
	ch, out, err = s.DriverTransition(ctx, meUser, fleet, rep, me, StateAccepted, nil)
	if err != nil || out != Replayed || ch.Job.State != StateAccepted {
		t.Fatalf("accept replay: outcome %v err %v state %v, want Replayed accepted", out, err, ch.Job.State)
	}
	if events(rep) != before {
		t.Fatalf("accept replay wrote an event")
	}

	// Replay of fail: the second reason is ignored and the first stays durable.
	f := fresh(me)
	advance(f, StateAccepted)
	if _, out, err := s.DriverTransition(ctx, meUser, fleet, f, me, StateFailed, reason("first")); err != nil || out != Applied {
		t.Fatalf("first fail: outcome %v err %v, want Applied", out, err)
	}
	before = events(f)
	if _, out, err := s.DriverTransition(ctx, meUser, fleet, f, me, StateFailed, reason("second")); err != nil || out != Replayed {
		t.Fatalf("second fail: outcome %v err %v, want Replayed", out, err)
	}
	if events(f) != before {
		t.Fatalf("fail replay wrote an event")
	}
	if r := reasonOf(f); r == nil || *r != "first" {
		t.Fatalf("failure_reason after replay = %v, want first", r)
	}

	// Ownership (GF-187): another driver, an unlinked caller (driver id 0), and a
	// cross-fleet caller all get ErrNotFound and leave the job unchanged.
	owned := fresh(me)
	before = events(owned)
	if _, _, err := s.DriverTransition(ctx, meUser, fleet, owned, other, StateAccepted, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other driver accept: err = %v, want ErrNotFound", err)
	}
	if _, _, err := s.DriverTransition(ctx, meUser, fleet, owned, 0, StateAccepted, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unlinked accept: err = %v, want ErrNotFound", err)
	}
	if _, _, err := s.DriverTransition(ctx, otherAdmin, otherFleet, owned, me, StateAccepted, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-fleet accept: err = %v, want ErrNotFound", err)
	}
	if stateOf(owned) != StateAssigned || events(owned) != before {
		t.Fatalf("refused driver moves changed the job")
	}

	// The ownership guard runs before the replay check: a non-owner calling a
	// target the job already has gets ErrNotFound, not a replayed 200.
	advance(owned, StateAccepted)
	before = events(owned)
	if _, _, err := s.DriverTransition(ctx, meUser, fleet, owned, other, StateAccepted, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-owner replay: err = %v, want ErrNotFound", err)
	}
	if _, _, err := s.DriverTransition(ctx, otherAdmin, otherFleet, owned, me, StateFailed, reason("x")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-fleet fail: err = %v, want ErrNotFound", err)
	}
	if stateOf(owned) != StateAccepted || events(owned) != before || reasonOf(owned) != nil {
		t.Fatalf("non-owner calls changed the job")
	}

	// Illegal edges from assigned are ErrConflict with nothing changed.
	ill := fresh(me)
	before = events(ill)
	for _, to := range []State{StateEnRoute, StatePickedUp, StateFailed} {
		if _, _, err := s.DriverTransition(ctx, meUser, fleet, ill, me, to, reason("x")); !errors.Is(err, ErrConflict) {
			t.Fatalf("assigned -> %s: err = %v, want ErrConflict", to, err)
		}
	}
	if stateOf(ill) != StateAssigned || events(ill) != before || reasonOf(ill) != nil {
		t.Fatalf("illegal driver moves changed the job")
	}
	// failed is terminal: accepted from failed is ErrConflict.
	if _, _, err := s.DriverTransition(ctx, meUser, fleet, f, me, StateAccepted, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("failed -> accepted: err = %v, want ErrConflict", err)
	}

	// Targets outside the driver set are ErrValidation, and fail needs a reason.
	val := fresh(me)
	for _, to := range []State{StateDelivered, StateAssigned, StateCancelled, State("bogus")} {
		if _, _, err := s.DriverTransition(ctx, meUser, fleet, val, me, to, reason("x")); !errors.Is(err, ErrValidation) {
			t.Fatalf("target %s: err = %v, want ErrValidation", to, err)
		}
	}
	if _, _, err := s.DriverTransition(ctx, meUser, fleet, val, me, StateFailed, nil); !errors.Is(err, ErrValidation) {
		t.Fatalf("fail without reason: err = %v, want ErrValidation", err)
	}
	if stateOf(val) != StateAssigned {
		t.Fatalf("validation refusals changed the job")
	}
}
