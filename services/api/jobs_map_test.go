package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"fleetops/internal/jobs"
)

// TestMapShowsJobState: the map status follows the vehicle's active job (GF-181).
// en_route shows en_route; picked_up shows en_route; accepted does not.
func TestMapShowsJobState(t *testing.T) {
	h := startServer(t)
	f := newJobsFleet(t, h, "mapjobs")
	d := f.driver(t, h, "map-driver")
	fresh := h.clock.t
	store := &jobs.Store{DB: h.db}
	ctx := context.Background()

	// vehicleWithJob puts a fresh position on a vehicle, then assigns a job and walks it to the state.
	vehicleWithJob := func(plate string, lng, lat float64, steps ...jobs.State) int64 {
		t.Helper()
		v := mapFixtureVehicle(t, h.db, f.id, plate, lng, lat, fresh, nil)
		r := h.do(t, http.MethodPost, "/jobs", f.dispTok, `{`+jobPoints+`}`, nil)
		if r.status != http.StatusCreated {
			t.Fatalf("create job = %d %s", r.status, r.body)
		}
		job := jobID(t, r)
		assignJob(t, h, f.dispTok, job, v, d)
		for _, to := range steps {
			if _, _, err := store.Transition(ctx, f.dispID, f.id, job, to); err != nil {
				t.Fatalf("transition %s: %v", to, err)
			}
		}
		return v
	}

	enRoute := vehicleWithJob("MAP-EN", 13.40, 52.50, jobs.StateAccepted, jobs.StateEnRoute)
	pickedUp := vehicleWithJob("MAP-PU", 13.41, 52.50, jobs.StateAccepted, jobs.StateEnRoute, jobs.StatePickedUp)
	accepted := vehicleWithJob("MAP-AC", 13.42, 52.50, jobs.StateAccepted)

	r := h.do(t, http.MethodGet, mapBox, f.dispTok, "", nil)
	if r.status != http.StatusOK {
		t.Fatalf("GET /map/vehicles = %d %s", r.status, r.body)
	}
	var out struct {
		Vehicles []struct {
			VehicleID int64  `json:"vehicle_id"`
			Status    string `json:"status"`
		} `json:"vehicles"`
	}
	if err := json.Unmarshal(r.body, &out); err != nil {
		t.Fatalf("decode map: %v", err)
	}
	status := map[int64]string{}
	for _, v := range out.Vehicles {
		status[v.VehicleID] = v.Status
	}
	for _, c := range []struct {
		name    string
		vehicle int64
		want    string
	}{
		{"en_route job", enRoute, "en_route"},
		{"picked_up job", pickedUp, "en_route"},
		{"accepted job", accepted, "idle"},
	} {
		if got, ok := status[c.vehicle]; !ok || got != c.want {
			t.Errorf("%s: vehicle %d status %q (present %v), want %q", c.name, c.vehicle, got, ok, c.want)
		}
	}
}
