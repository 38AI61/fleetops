package main

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
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
	job := createJob(t, h, f.dispTok, `{`+jobPoints+`}`)
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
