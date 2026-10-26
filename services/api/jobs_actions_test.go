package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"

	"fleetops/internal/jobs"
)

// eventCount counts job_events rows for one job.
func eventCount(t *testing.T, h *harness, job int64) int {
	t.Helper()
	var n int
	if err := h.db.QueryRow(`SELECT count(*) FROM job_events WHERE job_id = $1`, job).Scan(&n); err != nil {
		t.Fatalf("count events: %v", err)
	}
	return n
}

// jobState reads the persisted state of one job.
func jobState(t *testing.T, h *harness, job int64) string {
	t.Helper()
	var s string
	if err := h.db.QueryRow(`SELECT state FROM jobs WHERE id = $1`, job).Scan(&s); err != nil {
		t.Fatalf("read state: %v", err)
	}
	return s
}

// setVehicleStatus changes a vehicle's registry status for a fixture.
func setVehicleStatus(t *testing.T, h *harness, vehicle int64, status string) {
	t.Helper()
	if _, err := h.db.Exec(`UPDATE vehicles SET status = $2 WHERE id = $1`, vehicle, status); err != nil {
		t.Fatalf("set vehicle status: %v", err)
	}
}

// advance moves a job through the store as the fleet's dispatcher, for fixtures
// that need a driver-side state (accepted, en_route) before the HTTP routes exist.
func advance(t *testing.T, h *harness, f jobsFleet, job int64, to jobs.State) {
	t.Helper()
	s := &jobs.Store{DB: h.db}
	if _, _, err := s.Transition(context.Background(), f.dispID, f.id, job, to); err != nil {
		t.Fatalf("advance job %d to %s: %v", job, to, err)
	}
}

// TestJobsAssignReplayAndReassign: assign, replay, reassign, and the refusals (GF-175).
func TestJobsAssignReplayAndReassign(t *testing.T) {
	h := startServer(t)
	f := newJobsFleet(t, h, "assign")
	v1, v2 := f.vehicle(t, h, "AS-1"), f.vehicle(t, h, "AS-2")
	d1, d2 := f.driver(t, h, "as-driver-1"), f.driver(t, h, "as-driver-2")
	job := createJob(t, h, f.dispTok, `{`+jobPoints+`}`)
	path := fmt.Sprintf("/jobs/%d/assign", job)
	assign := func(v, d int64) reply {
		return h.do(t, http.MethodPost, path, f.dispTok, fmt.Sprintf(`{"vehicle_id":%d,"driver_id":%d}`, v, d), nil)
	}

	// created -> assigned: 200, one event.
	if r := assign(v1, d1); r.status != http.StatusOK {
		t.Fatalf("assign = %d %s, want 200", r.status, r.body)
	}
	if n := eventCount(t, h, job); n != 2 {
		t.Fatalf("events after assign = %d, want 2 (create + assign)", n)
	}

	// Same vehicle and driver again: 200, no event.
	if r := assign(v1, d1); r.status != http.StatusOK {
		t.Fatalf("replay = %d %s, want 200", r.status, r.body)
	}
	if n := eventCount(t, h, job); n != 2 {
		t.Fatalf("events after replay = %d, want 2", n)
	}

	// Different driver: one assigned -> assigned event.
	if r := assign(v1, d2); r.status != http.StatusOK {
		t.Fatalf("reassign driver = %d %s, want 200", r.status, r.body)
	}
	if n := eventCount(t, h, job); n != 3 {
		t.Fatalf("events after driver reassign = %d, want 3", n)
	}
	var from, to string
	if err := h.db.QueryRow(`SELECT from_state, to_state FROM job_events WHERE job_id = $1 ORDER BY id DESC LIMIT 1`, job).Scan(&from, &to); err != nil {
		t.Fatalf("last event: %v", err)
	}
	if from != "assigned" || to != "assigned" {
		t.Fatalf("last event = %s -> %s, want assigned -> assigned", from, to)
	}

	// Different vehicle: another reassign event.
	if r := assign(v2, d2); r.status != http.StatusOK {
		t.Fatalf("reassign vehicle = %d %s, want 200", r.status, r.body)
	}
	if n := eventCount(t, h, job); n != 4 {
		t.Fatalf("events after vehicle reassign = %d, want 4", n)
	}

	// A vehicle in maintenance is refused for a new assignment; nothing is written.
	setVehicleStatus(t, h, v1, "maintenance")
	if r := assign(v1, d1); r.status != http.StatusConflict || r.errCode(t) != "vehicle_not_active" {
		t.Fatalf("assign to maintenance vehicle = %d %s, want 409 vehicle_not_active", r.status, r.body)
	}
	if n := eventCount(t, h, job); n != 4 {
		t.Fatalf("events after refused assign = %d, want 4", n)
	}
	// The replay rule does not recheck vehicle status (GF-175 step 2).
	if r := assign(v2, d2); r.status != http.StatusOK {
		t.Fatalf("replay with active vehicle = %d %s, want 200", r.status, r.body)
	}

	// accepted: assign is refused with conflict.
	advance(t, h, f, job, jobs.StateAccepted)
	if r := assign(v2, d1); r.status != http.StatusConflict || r.errCode(t) != "conflict" {
		t.Fatalf("assign on accepted = %d %s, want 409 conflict", r.status, r.body)
	}
	if got := jobState(t, h, job); got != "accepted" {
		t.Fatalf("state after refused assign = %s, want accepted", got)
	}

	// A created job against a maintenance vehicle is refused the same way.
	fresh := createJob(t, h, f.dispTok, `{`+jobPoints+`}`)
	r := h.do(t, http.MethodPost, fmt.Sprintf("/jobs/%d/assign", fresh), f.dispTok,
		fmt.Sprintf(`{"vehicle_id":%d,"driver_id":%d}`, v1, d1), nil)
	if r.status != http.StatusConflict || r.errCode(t) != "vehicle_not_active" {
		t.Fatalf("created job, maintenance vehicle = %d %s, want 409 vehicle_not_active", r.status, r.body)
	}
	if n := eventCount(t, h, fresh); n != 1 {
		t.Fatalf("events on refused fresh job = %d, want 1", n)
	}
}

// TestJobsCancelReplay: cancel from each allowed state, replay, and refusals (GF-176).
func TestJobsCancelReplay(t *testing.T) {
	h := startServer(t)
	f := newJobsFleet(t, h, "cancel")
	v, d := f.vehicle(t, h, "CN-1"), f.driver(t, h, "cn-driver")

	cancel := func(job int64, body string) reply {
		return h.do(t, http.MethodPost, fmt.Sprintf("/jobs/%d/cancel", job), f.dispTok, body, nil)
	}

	// created: cancel leaves driver_id NULL and writes one event.
	created := createJob(t, h, f.dispTok, `{`+jobPoints+`}`)
	if r := cancel(created, ""); r.status != http.StatusOK {
		t.Fatalf("cancel created = %d %s, want 200", r.status, r.body)
	}
	if n := eventCount(t, h, created); n != 2 {
		t.Fatalf("events after cancel from created = %d, want 2", n)
	}

	// assigned.
	assigned := createJob(t, h, f.dispTok, `{`+jobPoints+`}`)
	assignJob(t, h, f.dispTok, assigned, v, d)
	if r := cancel(assigned, ""); r.status != http.StatusOK {
		t.Fatalf("cancel assigned = %d %s, want 200", r.status, r.body)
	}
	if n := eventCount(t, h, assigned); n != 3 {
		t.Fatalf("events after cancel from assigned = %d, want 3", n)
	}

	// accepted.
	accepted := createJob(t, h, f.dispTok, `{`+jobPoints+`}`)
	assignJob(t, h, f.dispTok, accepted, v, d)
	advance(t, h, f, accepted, jobs.StateAccepted)
	if r := cancel(accepted, ""); r.status != http.StatusOK {
		t.Fatalf("cancel accepted = %d %s, want 200", r.status, r.body)
	}
	if n := eventCount(t, h, accepted); n != 4 {
		t.Fatalf("events after cancel from accepted = %d, want 4", n)
	}

	// Replay: 200, no event. An empty object body is accepted.
	for _, job := range []int64{created, assigned, accepted} {
		before := eventCount(t, h, job)
		if r := cancel(job, `{}`); r.status != http.StatusOK {
			t.Fatalf("replay cancel job %d = %d %s, want 200", job, r.status, r.body)
		}
		if after := eventCount(t, h, job); after != before {
			t.Fatalf("replay cancel job %d wrote events: %d -> %d", job, before, after)
		}
	}

	// en_route: conflict, nothing written.
	moving := createJob(t, h, f.dispTok, `{`+jobPoints+`}`)
	assignJob(t, h, f.dispTok, moving, v, d)
	advance(t, h, f, moving, jobs.StateAccepted)
	advance(t, h, f, moving, jobs.StateEnRoute)
	if r := cancel(moving, ""); r.status != http.StatusConflict || r.errCode(t) != "conflict" {
		t.Fatalf("cancel en_route = %d %s, want 409 conflict", r.status, r.body)
	}
	if n := eventCount(t, h, moving); n != 4 {
		t.Fatalf("events on refused cancel = %d, want 4", n)
	}

	// Any field in the body is a 400.
	if r := cancel(assigned, `{"x":1}`); r.status != http.StatusBadRequest || r.errCode(t) != "validation_failed" {
		t.Fatalf("cancel with field = %d %s, want 400 validation_failed", r.status, r.body)
	}
}

// TestJobsIllegalTransitionPersistsNothing: each refused request is 409 and
// leaves the job row and the event count unchanged (roadmap validation item).
func TestJobsIllegalTransitionPersistsNothing(t *testing.T) {
	h := startServer(t)
	f := newJobsFleet(t, h, "illegal")
	v, d := f.vehicle(t, h, "IL-1"), f.driver(t, h, "il-driver")

	enRoute := createJob(t, h, f.dispTok, `{`+jobPoints+`}`)
	assignJob(t, h, f.dispTok, enRoute, v, d)
	advance(t, h, f, enRoute, jobs.StateAccepted)
	advance(t, h, f, enRoute, jobs.StateEnRoute)

	accepted := createJob(t, h, f.dispTok, `{`+jobPoints+`}`)
	assignJob(t, h, f.dispTok, accepted, v, d)
	advance(t, h, f, accepted, jobs.StateAccepted)

	cancelled := createJob(t, h, f.dispTok, `{`+jobPoints+`}`)
	if r := h.do(t, http.MethodPost, fmt.Sprintf("/jobs/%d/cancel", cancelled), f.dispTok, "", nil); r.status != http.StatusOK {
		t.Fatalf("setup cancel = %d %s", r.status, r.body)
	}

	snapshot := func(job int64) string {
		var state, vehicle, driver string
		if err := h.db.QueryRow(
			`SELECT state, coalesce(vehicle_id::text, '-'), coalesce(driver_id::text, '-') FROM jobs WHERE id = $1`, job,
		).Scan(&state, &vehicle, &driver); err != nil {
			t.Fatalf("snapshot job: %v", err)
		}
		return fmt.Sprintf("%s/%s/%s/events=%d", state, vehicle, driver, eventCount(t, h, job))
	}

	cases := []struct {
		name   string
		job    int64
		method string
		path   string
		body   string
	}{
		{"cancel en_route", enRoute, http.MethodPost, "/cancel", ""},
		{"assign accepted", accepted, http.MethodPost, "/assign", fmt.Sprintf(`{"vehicle_id":%d,"driver_id":%d}`, v, d)},
		{"assign cancelled", cancelled, http.MethodPost, "/assign", fmt.Sprintf(`{"vehicle_id":%d,"driver_id":%d}`, v, d)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := snapshot(tc.job)
			r := h.do(t, tc.method, fmt.Sprintf("/jobs/%d%s", tc.job, tc.path), f.dispTok, tc.body, nil)
			if r.status != http.StatusConflict {
				t.Fatalf("status = %d %s, want 409", r.status, r.body)
			}
			if after := snapshot(tc.job); after != before {
				t.Fatalf("refused request changed the job: %s -> %s", before, after)
			}
		})
	}
}

// rawCall posts without t, so it is safe from goroutines.
func rawCall(method, url, bearer, body string) (int, []byte, error) {
	req, err := http.NewRequest(method, url, bytes.NewBufferString(body))
	if err != nil {
		return 0, nil, err
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, b, err
}

// TestJobsConcurrentCancel: parallel cancels of one assigned job write exactly
// one event; the rest are replays (D-24 under the row lock).
func TestJobsConcurrentCancel(t *testing.T) {
	h := startServer(t)
	f := newJobsFleet(t, h, "race")
	v, d := f.vehicle(t, h, "RC-1"), f.driver(t, h, "rc-driver")
	job := createJob(t, h, f.dispTok, `{`+jobPoints+`}`)
	assignJob(t, h, f.dispTok, job, v, d)

	const callers = 4
	type result struct {
		status int
		body   []byte
		err    error
	}
	results := make(chan result, callers)
	var start sync.WaitGroup
	start.Add(1)
	url := fmt.Sprintf("%s/jobs/%d/cancel", h.url, job)
	for range callers {
		go func() {
			start.Wait()
			status, body, err := rawCall(http.MethodPost, url, f.dispTok, "")
			results <- result{status, body, err}
		}()
	}
	start.Done()

	for range callers {
		res := <-results
		if res.err != nil {
			t.Fatalf("cancel call: %v", res.err)
		}
		if res.status != http.StatusOK {
			t.Fatalf("concurrent cancel = %d %s, want 200 for every caller", res.status, res.body)
		}
		var j struct {
			State string `json:"state"`
		}
		if err := json.Unmarshal(res.body, &j); err != nil || j.State != "cancelled" {
			t.Fatalf("concurrent cancel body = %s, want state cancelled", res.body)
		}
	}
	if n := eventCount(t, h, job); n != 3 {
		t.Fatalf("events = %d, want 3 (create, assign, one cancel)", n)
	}
	if got := jobState(t, h, job); got != "cancelled" {
		t.Fatalf("state = %s, want cancelled", got)
	}
}
