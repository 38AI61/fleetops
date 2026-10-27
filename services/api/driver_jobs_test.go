package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// driverLogin inserts a driver user linked to driverID in the fleet and returns its token.
func (h *harness) driverLogin(t *testing.T, fleet, driverID int64, email string) string {
	t.Helper()
	uid := h.user(t, fleet, email, "driver")
	if _, err := h.db.Exec(`UPDATE users SET driver_id = $1 WHERE id = $2`, driverID, uid); err != nil {
		t.Fatalf("link driver user: %v", err)
	}
	return h.login(t, email)
}

// assignedJob creates a job and assigns it to the vehicle and driver as the dispatcher.
func assignedJob(t *testing.T, h *harness, f jobsFleet, vehicle, driver int64) int64 {
	t.Helper()
	return assignedJobAt(t, h, f, vehicle, driver, "")
}

// assignedJobAt is assignedJob with extra JSON fields appended to the create body,
// such as `,"window_start":"..."`.
func assignedJobAt(t *testing.T, h *harness, f jobsFleet, vehicle, driver int64, extra string) int64 {
	t.Helper()
	job := createJob(t, h, f.dispTok, `{`+jobPoints+extra+`}`)
	r := h.do(t, http.MethodPost, fmt.Sprintf("/jobs/%d/assign", job), f.dispTok,
		fmt.Sprintf(`{"vehicle_id":%d,"driver_id":%d}`, vehicle, driver), nil)
	if r.status != http.StatusOK {
		t.Fatalf("assign = %d %s, want 200", r.status, r.body)
	}
	return job
}

// TestDriverTransitionValidation covers the driver route refusals (GF-186..GF-188):
// fail body rules, empty-body rules for accept, malformed ids, unlinked callers,
// non-driver roles refused before the body is read, and the 409 and 404 cases.
func TestDriverTransitionValidation(t *testing.T) {
	h := startServer(t)
	f := newJobsFleet(t, h, "drvval")
	v := f.vehicle(t, h, "DV-1")
	d := f.driver(t, h, "drvval-driver")
	tok := h.driverLogin(t, f.id, d, "drvval-driver@jobs.example")
	h.user(t, f.id, "drvval-unlinked@jobs.example", "driver")
	unlinkedTok := h.login(t, "drvval-unlinked@jobs.example")

	job := assignedJob(t, h, f, v, d)
	fail := func(id int64, body string) reply {
		return h.do(t, http.MethodPost, fmt.Sprintf("/driver/jobs/%d/fail", id), tok, body, nil)
	}

	// Fail bodies that are refused: 400 validation_failed, job unchanged.
	long := strings.Repeat("a", 1001)
	for _, body := range []string{
		``, `{}`, `{"reason":null}`, `{"reason":""}`, `{"reason":" \t "}`,
		`{"reason":"` + long + `"}`, `{"reason":"gate","extra":1}`, `{"reason":"gate"`, `[]`,
	} {
		if r := fail(job, body); r.status != http.StatusBadRequest || r.errCode(t) != "validation_failed" {
			t.Fatalf("fail body %q = %d %s, want 400 validation_failed", body, r.status, r.body)
		}
	}
	if got := jobState(t, h, job); got != "assigned" {
		t.Fatalf("state after refused fail bodies = %s, want assigned", got)
	}
	if n := eventCount(t, h, job); n != 2 {
		t.Fatalf("events after refused fail bodies = %d, want 2", n)
	}

	// The reason cap counts runes: 1000 multi-byte runes pass validation and then
	// hit the state rule (assigned cannot fail), which is 409.
	if r := fail(job, `{"reason":"`+strings.Repeat("é", 1000)+`"}`); r.status != http.StatusConflict {
		t.Fatalf("1000-rune reason from assigned = %d %s, want 409", r.status, r.body)
	}

	// accept takes an empty body or {}; any field is 400 before the store is reached.
	acc := func(id int64, body string) reply {
		return h.do(t, http.MethodPost, fmt.Sprintf("/driver/jobs/%d/accept", id), tok, body, nil)
	}
	if r := acc(job, `{"x":1}`); r.status != http.StatusBadRequest || r.errCode(t) != "validation_failed" {
		t.Fatalf("accept with field = %d %s, want 400", r.status, r.body)
	}
	if r := acc(job, `{}`); r.status != http.StatusOK {
		t.Fatalf("accept {} = %d %s, want 200", r.status, r.body)
	}
	if got := jobState(t, h, job); got != "accepted" {
		t.Fatalf("state after accept = %s, want accepted", got)
	}

	// fail is valid from accepted; the reason is trimmed and stored.
	if r := fail(job, `{"reason":"  locked gate  "}`); r.status != http.StatusOK {
		t.Fatalf("fail from accepted = %d %s, want 200", r.status, r.body)
	}
	if got := jobState(t, h, job); got != "failed" {
		t.Fatalf("state after fail = %s, want failed", got)
	}
	var reason string
	if err := h.db.QueryRow(`SELECT failure_reason FROM jobs WHERE id = $1`, job).Scan(&reason); err != nil || reason != "locked gate" {
		t.Fatalf("failure_reason = %q (%v), want trimmed locked gate", reason, err)
	}

	// Malformed ids are 404; a valid body does not change that.
	for _, path := range []string{"/driver/jobs/abc/accept", "/driver/jobs/0/accept", "/driver/jobs/abc/fail"} {
		r := h.do(t, http.MethodPost, path, tok, `{"reason":"x"}`, nil)
		if r.status != http.StatusNotFound {
			t.Fatalf("%s = %d %s, want 404", path, r.status, r.body)
		}
	}

	// A cancelled job still tied to the driver: accept is 409 (cancelled has no edge to accepted).
	cancelled := assignedJob(t, h, f, v, d)
	if r := h.do(t, http.MethodPost, fmt.Sprintf("/jobs/%d/cancel", cancelled), f.dispTok, `{}`, nil); r.status != http.StatusOK {
		t.Fatalf("cancel = %d %s, want 200", r.status, r.body)
	}
	if r := acc(cancelled, ``); r.status != http.StatusConflict || r.errCode(t) != "conflict" {
		t.Fatalf("accept on cancelled = %d %s, want 409 conflict", r.status, r.body)
	}

	// A created job has no driver, so it is not this driver's: 404, not 409 (GF-187).
	unowned := createJob(t, h, f.dispTok, `{`+jobPoints+`}`)
	if r := acc(unowned, ``); r.status != http.StatusNotFound {
		t.Fatalf("accept on unowned created job = %d %s, want 404", r.status, r.body)
	}

	// An unlinked driver user gets 404 on every transition (GF-184).
	if r := h.do(t, http.MethodPost, fmt.Sprintf("/driver/jobs/%d/accept", cancelled), unlinkedTok, ``, nil); r.status != http.StatusNotFound {
		t.Fatalf("unlinked accept = %d %s, want 404", r.status, r.body)
	}

	// Non-driver roles are refused by the role check before the body is read (GF-186, GF-28).
	for _, c := range []struct{ name, tok string }{{"admin", f.adminTok}, {"dispatcher", f.dispTok}} {
		if r := h.do(t, http.MethodPost, fmt.Sprintf("/driver/jobs/%d/fail", job), c.tok, `{`, nil); r.status != http.StatusForbidden {
			t.Fatalf("%s malformed fail = %d %s, want 403", c.name, r.status, r.body)
		}
	}
}

// driverDo sends one driver route with the driver's token.
func driverDo(t *testing.T, h *harness, tok string, job int64, action, body string) reply {
	t.Helper()
	return h.do(t, http.MethodPost, fmt.Sprintf("/driver/jobs/%d/%s", job, action), tok, body, nil)
}

// todayIDs returns the ids GET /driver/jobs/today lists, in order, for the caller.
func todayIDs(t *testing.T, h *harness, tok string) []int64 {
	t.Helper()
	r := h.do(t, http.MethodGet, "/driver/jobs/today", tok, "", nil)
	if r.status != http.StatusOK {
		t.Fatalf("GET /driver/jobs/today = %d %s, want 200", r.status, r.body)
	}
	var out struct {
		Jobs []struct {
			ID int64 `json:"id"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(r.body, &out); err != nil {
		t.Fatalf("decode today: %v", err)
	}
	ids := []int64{}
	for _, j := range out.Jobs {
		ids = append(ids, j.ID)
	}
	return ids
}

// eventCountTo counts a job's events whose to_state is state.
func eventCountTo(t *testing.T, h *harness, job int64, state string) int {
	t.Helper()
	var n int
	if err := h.db.QueryRow(`SELECT count(*) FROM job_events WHERE job_id = $1 AND to_state = $2`, job, state).Scan(&n); err != nil {
		t.Fatalf("count events to %s: %v", state, err)
	}
	return n
}

// TestDriverToday: a driver's today list in a non-UTC fleet (GF-185). The fake
// clock is set before any login so tokens stay valid. Istanbul is UTC+3, so the
// 22:30 UTC window on the 27th is the 28th locally and is excluded.
func TestDriverToday(t *testing.T) {
	h := startServer(t)
	h.clock.t = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	f := newJobsFleet(t, h, "drvtoday")
	if _, err := h.db.Exec(`UPDATE fleets SET timezone = 'Europe/Istanbul' WHERE id = $1`, f.id); err != nil {
		t.Fatalf("set timezone: %v", err)
	}
	v := f.vehicle(t, h, "DTD-1")
	d1, d2 := f.driver(t, h, "drvtoday-1"), f.driver(t, h, "drvtoday-2")
	tok1 := h.driverLogin(t, f.id, d1, "drvtoday-1@jobs.example")
	tok2 := h.driverLogin(t, f.id, d2, "drvtoday-2@jobs.example")

	carried := assignedJobAt(t, h, f, v, d1, `,"window_start":"2026-10-26T20:00:00Z"`)
	if r := driverDo(t, h, tok1, carried, "accept", ``); r.status != http.StatusOK {
		t.Fatalf("accept carried = %d %s", r.status, r.body)
	}
	todayJob := assignedJobAt(t, h, f, v, d1, `,"window_start":"2026-10-27T09:00:00Z"`)
	undated := assignedJobAt(t, h, f, v, d1, ``)
	assignedJobAt(t, h, f, v, d1, `,"window_start":"2026-10-28T09:00:00Z"`) // future
	assignedJobAt(t, h, f, v, d1, `,"window_start":"2026-10-27T22:30:00Z"`) // the 28th in Istanbul: future
	failed := assignedJobAt(t, h, f, v, d1, `,"window_start":"2026-10-27T10:00:00Z"`)
	driverDo(t, h, tok1, failed, "accept", ``)
	if r := driverDo(t, h, tok1, failed, "fail", `{"reason":"no access"}`); r.status != http.StatusOK {
		t.Fatalf("fail = %d %s", r.status, r.body)
	}
	cancelled := assignedJobAt(t, h, f, v, d1, `,"window_start":"2026-10-27T11:00:00Z"`)
	if r := h.do(t, http.MethodPost, fmt.Sprintf("/jobs/%d/cancel", cancelled), f.dispTok, `{}`, nil); r.status != http.StatusOK {
		t.Fatalf("cancel = %d %s", r.status, r.body)
	}
	other := assignedJobAt(t, h, f, v, d2, `,"window_start":"2026-10-27T09:00:00Z"`)

	if got, want := todayIDs(t, h, tok1), []int64{carried, todayJob, undated}; !slices.Equal(got, want) {
		t.Fatalf("driver 1 today = %v, want %v", got, want)
	}
	if got, want := todayIDs(t, h, tok2), []int64{other}; !slices.Equal(got, want) {
		t.Fatalf("driver 2 today = %v, want %v", got, want)
	}

	// The envelope is exactly {"jobs": [...]}.
	r := h.do(t, http.MethodGet, "/driver/jobs/today", tok1, "", nil)
	var env map[string]json.RawMessage
	if err := json.Unmarshal(r.body, &env); err != nil || len(env) != 1 || env["jobs"] == nil {
		t.Fatalf("today envelope = %s, want only jobs", r.body)
	}

	// A user with no driver row has no jobs: 200 with an empty list.
	h.user(t, f.id, "drvtoday-unlinked@jobs.example", "driver")
	unlinked := h.login(t, "drvtoday-unlinked@jobs.example")
	if r := h.do(t, http.MethodGet, "/driver/jobs/today", unlinked, "", nil); r.status != http.StatusOK || !strings.Contains(string(r.body), `"jobs":[]`) {
		t.Fatalf("unlinked today = %d %s, want 200 with empty jobs", r.status, r.body)
	}
}

// TestDriverCrossDriverIsolation: a driver cannot move or see another driver's
// job, and a driver in another fleet cannot touch this fleet's jobs (GF-187).
func TestDriverCrossDriverIsolation(t *testing.T) {
	h := startServer(t)
	f := newJobsFleet(t, h, "drviso")
	v := f.vehicle(t, h, "DIS-1")
	a, b := f.driver(t, h, "drviso-a"), f.driver(t, h, "drviso-b")
	tokA := h.driverLogin(t, f.id, a, "drviso-a@jobs.example")
	tokB := h.driverLogin(t, f.id, b, "drviso-b@jobs.example")

	bJob := assignedJob(t, h, f, v, b)
	aJob := assignedJob(t, h, f, v, a)
	before := eventCount(t, h, bJob)

	for _, c := range []struct{ action, body string }{
		{"accept", ``}, {"start", ``}, {"pickup", ``}, {"fail", `{"reason":"x"}`},
	} {
		if r := driverDo(t, h, tokA, bJob, c.action, c.body); r.status != http.StatusNotFound {
			t.Fatalf("A %s on B's job = %d %s, want 404", c.action, r.status, r.body)
		}
	}
	if got := jobState(t, h, bJob); got != "assigned" {
		t.Fatalf("B's job state after A's calls = %s, want assigned", got)
	}
	if n := eventCount(t, h, bJob); n != before {
		t.Fatalf("B's job events after A's calls = %d, want %d", n, before)
	}

	if got, want := todayIDs(t, h, tokA), []int64{aJob}; !slices.Equal(got, want) {
		t.Fatalf("A today = %v, want only A's job %v", got, want)
	}
	if got, want := todayIDs(t, h, tokB), []int64{bJob}; !slices.Equal(got, want) {
		t.Fatalf("B today = %v, want only B's job %v", got, want)
	}

	// Cross-fleet: a driver in another fleet gets 404 on this fleet's job.
	g := newJobsFleet(t, h, "drvisox")
	gd := g.driver(t, h, "drvisox-driver")
	tokG := h.driverLogin(t, g.id, gd, "drvisox-driver@jobs.example")
	if r := driverDo(t, h, tokG, aJob, "accept", ``); r.status != http.StatusNotFound {
		t.Fatalf("cross-fleet accept = %d %s, want 404", r.status, r.body)
	}
	if got := jobState(t, h, aJob); got != "assigned" {
		t.Fatalf("A's job after cross-fleet call = %s, want assigned", got)
	}
	if got := todayIDs(t, h, tokG); len(got) != 0 {
		t.Fatalf("cross-fleet today = %v, want empty", got)
	}
}

// TestDriverReplay: repeated driver moves are D-24 replays: 200 each time, one
// event per applied move, and the first fail reason stays (GF-189).
func TestDriverReplay(t *testing.T) {
	h := startServer(t)
	f := newJobsFleet(t, h, "drvrep")
	v := f.vehicle(t, h, "DRP-1")
	d := f.driver(t, h, "drvrep-driver")
	tok := h.driverLogin(t, f.id, d, "drvrep-driver@jobs.example")
	job := assignedJob(t, h, f, v, d)

	for i := 0; i < 2; i++ {
		if r := driverDo(t, h, tok, job, "accept", ``); r.status != http.StatusOK {
			t.Fatalf("accept #%d = %d %s, want 200", i+1, r.status, r.body)
		}
	}
	if n := eventCountTo(t, h, job, "accepted"); n != 1 {
		t.Fatalf("accepted events = %d, want 1", n)
	}

	for _, move := range []struct{ action, to string }{{"start", "en_route"}, {"pickup", "picked_up"}} {
		for i := 0; i < 2; i++ {
			if r := driverDo(t, h, tok, job, move.action, ``); r.status != http.StatusOK {
				t.Fatalf("%s #%d = %d %s, want 200", move.action, i+1, r.status, r.body)
			}
		}
		if n := eventCountTo(t, h, job, move.to); n != 1 {
			t.Fatalf("%s events = %d, want 1", move.to, n)
		}
	}

	if r := driverDo(t, h, tok, job, "fail", `{"reason":"first"}`); r.status != http.StatusOK {
		t.Fatalf("fail #1 = %d %s, want 200", r.status, r.body)
	}
	if r := driverDo(t, h, tok, job, "fail", `{"reason":"second"}`); r.status != http.StatusOK {
		t.Fatalf("fail #2 = %d %s, want 200", r.status, r.body)
	}
	if n := eventCountTo(t, h, job, "failed"); n != 1 {
		t.Fatalf("failed events = %d, want 1", n)
	}
	var reason string
	if err := h.db.QueryRow(`SELECT failure_reason FROM jobs WHERE id = $1`, job).Scan(&reason); err != nil || reason != "first" {
		t.Fatalf("failure_reason = %q (%v), want first", reason, err)
	}
	// Create, assign, and the four applied moves: one event each.
	if n := eventCount(t, h, job); n != 6 {
		t.Fatalf("total events = %d, want 6 (create, assign, accept, start, pickup, fail)", n)
	}
}
