package jobs

import (
	"context"
	"errors"
	"testing"

	"fleetops/internal/testinfra"
)

// newTestJob is a minimal valid NewJob with fixed Istanbul coordinates.
func newTestJob() NewJob {
	return NewJob{Pickup: Point{Lat: 41.0082, Lng: 28.9784}, Dropoff: Point{Lat: 41.0422, Lng: 29.0083}}
}

// TestJobStoreTransitionD24 covers D-24 on the store: applied writes one event,
// replay writes none, an illegal edge is ErrConflict with nothing changed, and
// a job of another fleet is ErrNotFound.
func TestJobStoreTransitionD24(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	s := &Store{DB: db}
	ctx := context.Background()
	fleet, admin := fleetWithAdmin(t, db, "d24")
	otherFleet, otherAdmin := fleetWithAdmin(t, db, "d24-other")
	vehicle := fleetVehicle(t, db, fleet, "D24-1")
	driver := fleetDriver(t, db, fleet, "d24-driver")

	job, err := s.Create(ctx, admin, fleet, newTestJob())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if job.State != StateCreated || job.VehicleID != nil || job.DriverID != nil {
		t.Fatalf("created job = %+v, want state created and unassigned", job)
	}
	events := func() int {
		return rowCount(t, db, `SELECT count(*) FROM job_events WHERE job_id = $1`, job.ID)
	}
	if n := events(); n != 1 {
		t.Fatalf("events after create = %d, want 1 (creation)", n)
	}

	// Illegal: created -> accepted. ErrConflict, no event, state unchanged.
	if _, _, err := s.Transition(ctx, admin, fleet, job.ID, StateAccepted); !errors.Is(err, ErrConflict) {
		t.Fatalf("created -> accepted: err = %v, want ErrConflict", err)
	}
	if n := events(); n != 1 {
		t.Fatalf("events after illegal = %d, want 1", n)
	}
	if got, _ := s.Get(ctx, fleet, job.ID); got.State != StateCreated {
		t.Fatalf("state after illegal = %s, want created", got.State)
	}

	// Assign to the state the job must move through first.
	if _, _, err := s.Transition(ctx, admin, fleet, job.ID, StateAssigned); !errors.Is(err, ErrValidation) {
		t.Fatalf("transition to assigned: err = %v, want ErrValidation", err)
	}

	// Cross-fleet: the other fleet's admin cannot see or move the job.
	if _, _, err := s.Transition(ctx, otherAdmin, otherFleet, job.ID, StateCancelled); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-fleet cancel: err = %v, want ErrNotFound", err)
	}
	if _, err := s.Get(ctx, otherFleet, job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-fleet get: err = %v, want ErrNotFound", err)
	}

	// Applied: created -> assigned through Assign writes one event.
	ch, out, err := s.Assign(ctx, admin, fleet, job.ID, vehicle, driver)
	if err != nil || out != Applied {
		t.Fatalf("assign: outcome %v err %v, want Applied", out, err)
	}
	if ch.From != StateCreated || ch.To != StateAssigned || ch.Job.State != StateAssigned {
		t.Fatalf("assign change = %+v", ch)
	}
	if n := events(); n != 2 {
		t.Fatalf("events after assign = %d, want 2", n)
	}

	// Applied: assigned -> accepted through Transition.
	ch, out, err = s.Transition(ctx, admin, fleet, job.ID, StateAccepted)
	if err != nil || out != Applied {
		t.Fatalf("assigned -> accepted: outcome %v err %v, want Applied", out, err)
	}
	if ch.From != StateAssigned || ch.To != StateAccepted || ch.At.IsZero() {
		t.Fatalf("transition change = %+v", ch)
	}
	if n := events(); n != 3 {
		t.Fatalf("events after transition = %d, want 3", n)
	}

	// Replay: the same target again is Replayed with no event.
	ch, out, err = s.Transition(ctx, admin, fleet, job.ID, StateAccepted)
	if err != nil || out != Replayed {
		t.Fatalf("replay: outcome %v err %v, want Replayed", out, err)
	}
	if ch.Job.State != StateAccepted {
		t.Fatalf("replay job state = %s, want accepted", ch.Job.State)
	}
	if n := events(); n != 3 {
		t.Fatalf("events after replay = %d, want 3", n)
	}
}
