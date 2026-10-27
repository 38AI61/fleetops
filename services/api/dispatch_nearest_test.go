package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// nearestBody is the GF-214 response, decoded for assertions.
type nearestBody struct {
	JobID      int64 `json:"job_id"`
	Candidates []struct {
		VehicleID  int64   `json:"vehicle_id"`
		Plate      string  `json:"plate"`
		DriverID   int64   `json:"driver_id"`
		DriverName string  `json:"driver_name"`
		DistanceM  float64 `json:"distance_m"`
	} `json:"candidates"`
}

// nearestPair gives an active, reported vehicle an open pairing to a new driver of fleet (GF-210).
func nearestPair(t *testing.T, h *harness, fleet, vehicle int64, name string) {
	t.Helper()
	var driver, device int64
	if err := h.db.QueryRow(
		`INSERT INTO drivers (fleet_id, name, license_no_hash) VALUES ($1, $2, $3) RETURNING id`,
		fleet, name, []byte(name),
	).Scan(&driver); err != nil {
		t.Fatalf("insert driver %s: %v", name, err)
	}
	if err := h.db.QueryRow(
		`INSERT INTO devices (fleet_id, kind, api_key_hash) VALUES ($1, 'tracker', $2) RETURNING id`,
		fleet, []byte("dev-"+name),
	).Scan(&device); err != nil {
		t.Fatalf("insert device %s: %v", name, err)
	}
	if _, err := h.db.Exec(
		`INSERT INTO vehicle_pairings (fleet_id, vehicle_id, driver_id, device_id) VALUES ($1, $2, $3, $4)`,
		fleet, vehicle, driver, device,
	); err != nil {
		t.Fatalf("insert pairing %s: %v", name, err)
	}
}

// TestNearestEndpoint covers GF-213 to GF-215 over real HTTP. The pickup is
// jobPoints. Vehicles sit due north of it at 0.001 degree steps, so the
// expected order is creation order.
func TestNearestEndpoint(t *testing.T) {
	h := startServer(t)
	f := newJobsFleet(t, h, "nearest")
	fresh := h.clock.t
	const eligible = 12

	var vehicles []int64
	for i := 0; i < eligible; i++ {
		v := mapFixtureVehicle(t, h.db, f.id, fmt.Sprintf("NRS-%02d", i), 28.9784, 41.0082+0.001*float64(i+1), fresh, nil)
		nearestPair(t, h, f.id, v, fmt.Sprintf("nrs-driver-%02d", i))
		vehicles = append(vehicles, v)
	}
	// Unpaired and nearest of all: must never appear (GF-210).
	mapFixtureVehicle(t, h.db, f.id, "NRS-UNPAIRED", 28.9784, 41.0082+0.0001, fresh, nil)

	job := createJob(t, h, f.dispTok, `{`+jobPoints+`}`)
	path := fmt.Sprintf("/jobs/%d/nearest", job)

	get := func(query string) reply {
		t.Helper()
		return h.do(t, http.MethodGet, path+query, f.dispTok, "", nil)
	}
	decode := func(r reply) nearestBody {
		t.Helper()
		var out nearestBody
		if err := json.Unmarshal(r.body, &out); err != nil {
			t.Fatalf("decode nearest: %v (%s)", err, r.body)
		}
		return out
	}
	ids := func(out nearestBody) []int64 {
		got := make([]int64, 0, len(out.Candidates))
		for _, c := range out.Candidates {
			got = append(got, c.VehicleID)
		}
		return got
	}

	// Ordered, with the limit applied.
	r := get("?limit=3")
	if r.status != http.StatusOK {
		t.Fatalf("nearest limit=3 = %d %s", r.status, r.body)
	}
	out := decode(r)
	if out.JobID != job || len(out.Candidates) != 3 ||
		ids(out)[0] != vehicles[0] || ids(out)[1] != vehicles[1] || ids(out)[2] != vehicles[2] {
		t.Fatalf("limit=3 = job %d ids %v, want job %d ids %v", out.JobID, ids(out), job, vehicles[:3])
	}
	if out.Candidates[0].DriverName != "nrs-driver-00" || out.Candidates[0].DistanceM <= 0 {
		t.Fatalf("first candidate = %+v", out.Candidates[0])
	}

	// Default limit is 10 (GF-211), even with 12 eligible rows.
	def := decode(get(""))
	if len(def.Candidates) != 10 || ids(def)[9] != vehicles[9] {
		t.Fatalf("default limit gave %d rows, want 10", len(def.Candidates))
	}

	// The cap is 50; 12 eligible rows all come back under it.
	all := decode(get("?limit=50"))
	if len(all.Candidates) != eligible {
		t.Fatalf("limit=50 gave %d rows, want %d", len(all.Candidates), eligible)
	}

	for _, q := range []string{"?limit=0", "?limit=51", "?limit=abc", "?limit=-1", "?limit=1&limit=2"} {
		r := get(q)
		if r.status != http.StatusBadRequest || r.errCode(t) != "validation_failed" {
			t.Fatalf("nearest %s = %d %s, want 400 validation_failed", q, r.status, r.body)
		}
	}

	// Malformed and missing job ids are 404.
	for _, p := range []string{"/jobs/abc/nearest", "/jobs/0/nearest", "/jobs/999999/nearest"} {
		r := h.do(t, http.MethodGet, p, f.dispTok, "", nil)
		if r.status != http.StatusNotFound || r.errCode(t) != "not_found" {
			t.Fatalf("GET %s = %d %s, want 404 not_found", p, r.status, r.body)
		}
	}

	// A job of another fleet is 404 to this dispatcher (GF-213).
	other := newJobsFleet(t, h, "nearest-other")
	foreign := createJob(t, h, other.dispTok, `{`+jobPoints+`}`)
	r = h.do(t, http.MethodGet, fmt.Sprintf("/jobs/%d/nearest", foreign), f.dispTok, "", nil)
	if r.status != http.StatusNotFound || r.errCode(t) != "not_found" {
		t.Fatalf("cross-fleet nearest = %d %s, want 404", r.status, r.body)
	}

	// A driver is refused by role before the lookup, so a missing job is still 403 (GF-215).
	driver := f.driver(t, h, "nearest-driver")
	drvTok := h.driverLogin(t, f.id, driver, "nearest-driver@nearest.example")
	r = h.do(t, http.MethodGet, "/jobs/999999/nearest", drvTok, "", nil)
	if r.status != http.StatusForbidden {
		t.Fatalf("driver nearest = %d %s, want 403", r.status, r.body)
	}
}
