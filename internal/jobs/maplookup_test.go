package jobs

import (
	"context"
	"testing"

	"fleetops/internal/mapview"
	"fleetops/internal/testinfra"
)

// TestJobLookupPrecedence: per vehicle, en_route and picked_up show en_route;
// accepted and assigned show other; delivered, failed, cancelled and other
// fleets' jobs are ignored (GF-181).
func TestJobLookupPrecedence(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	s := &Store{DB: db}
	ctx := context.Background()
	fleet, admin := fleetWithAdmin(t, db, "lookup")
	otherFleet, otherAdmin := fleetWithAdmin(t, db, "lookup-other")
	driver := fleetDriver(t, db, fleet, "lookup-driver")
	otherDriver := fleetDriver(t, db, otherFleet, "lookup-other-driver")

	// jobOn creates a job on vehicle and walks it through the steps.
	jobOn := func(actor, f, vehicle, drv int64, steps ...State) {
		t.Helper()
		job, err := s.Create(ctx, actor, f, newTestJob())
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, _, err := s.Assign(ctx, actor, f, job.ID, vehicle, drv); err != nil {
			t.Fatalf("assign: %v", err)
		}
		for _, to := range steps {
			if _, _, err := s.Transition(ctx, actor, f, job.ID, to); err != nil {
				t.Fatalf("transition %s: %v", to, err)
			}
		}
	}

	enRoute := fleetVehicle(t, db, fleet, "LK-EN")
	jobOn(admin, fleet, enRoute, driver, StateAccepted, StateEnRoute)
	jobOn(admin, fleet, enRoute, driver, StateAccepted) // accepted does not outrank en_route

	pickedUp := fleetVehicle(t, db, fleet, "LK-PU")
	jobOn(admin, fleet, pickedUp, driver, StateAccepted, StateEnRoute, StatePickedUp)
	jobOn(admin, fleet, pickedUp, driver, StateAccepted) // picked_up outranks accepted

	accepted := fleetVehicle(t, db, fleet, "LK-AC")
	jobOn(admin, fleet, accepted, driver, StateAccepted)

	assigned := fleetVehicle(t, db, fleet, "LK-AS")
	jobOn(admin, fleet, assigned, driver)

	delivered := fleetVehicle(t, db, fleet, "LK-DL")
	jobOn(admin, fleet, delivered, driver, StateAccepted, StateEnRoute, StatePickedUp, StateDelivered)

	failed := fleetVehicle(t, db, fleet, "LK-FA")
	jobOn(admin, fleet, failed, driver, StateAccepted, StateFailed)

	cancelled := fleetVehicle(t, db, fleet, "LK-CA")
	jobOn(admin, fleet, cancelled, driver, StateCancelled)

	idle := fleetVehicle(t, db, fleet, "LK-ID")

	foreign := fleetVehicle(t, db, otherFleet, "LK-OT")
	jobOn(otherAdmin, otherFleet, foreign, otherDriver, StateAccepted, StateEnRoute)

	all := []int64{enRoute, pickedUp, accepted, assigned, delivered, failed, cancelled, idle, foreign}
	lookup := &MapLookup{DB: db}
	got, err := lookup.ActiveJobStates(ctx, fleet, all)
	if err != nil {
		t.Fatalf("ActiveJobStates: %v", err)
	}
	want := map[int64]mapview.JobState{
		enRoute:  mapview.JobEnRoute,
		pickedUp: mapview.JobEnRoute,
		accepted: mapview.JobOther,
		assigned: mapview.JobOther,
	}
	if len(got) != len(want) {
		t.Fatalf("states = %v, want %v", got, want)
	}
	for vehicle, state := range want {
		if got[vehicle] != state {
			t.Errorf("vehicle %d: state %q, want %q", vehicle, got[vehicle], state)
		}
	}

	// Asked by the other fleet, only its own vehicle answers.
	other, err := lookup.ActiveJobStates(ctx, otherFleet, all)
	if err != nil {
		t.Fatalf("ActiveJobStates other fleet: %v", err)
	}
	if len(other) != 1 || other[foreign] != mapview.JobEnRoute {
		t.Fatalf("other fleet states = %v, want only foreign en_route", other)
	}

	// An empty vehicle list is a no-op.
	none, err := lookup.ActiveJobStates(ctx, fleet, nil)
	if err != nil || len(none) != 0 {
		t.Fatalf("empty list = %v, %v", none, err)
	}
}
