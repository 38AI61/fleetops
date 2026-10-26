package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// jobPoints is a valid pickup and dropoff pair for every job fixture.
const jobPoints = `"pickup":{"lat":41.0082,"lng":28.9784},"dropoff":{"lat":41.0422,"lng":29.0083}`

// jobsFleet is one fleet with an admin and a dispatcher, both logged in.
type jobsFleet struct {
	id       int64
	adminTok string
	dispTok  string
}

func newJobsFleet(t *testing.T, h *harness, name string) jobsFleet {
	t.Helper()
	f := jobsFleet{id: h.fleet(t, name)}
	adminEmail := name + "-admin@jobs.example"
	h.user(t, f.id, adminEmail, "admin")
	f.adminTok = h.login(t, adminEmail)
	dispEmail := name + "-dispatcher@jobs.example"
	h.user(t, f.id, dispEmail, "dispatcher")
	f.dispTok = h.login(t, dispEmail)
	return f
}

// vehicle inserts an active vehicle in the fleet.
func (f jobsFleet) vehicle(t *testing.T, h *harness, plate string) int64 {
	t.Helper()
	var id int64
	if err := h.db.QueryRow(`INSERT INTO vehicles (fleet_id, plate) VALUES ($1, $2) RETURNING id`, f.id, plate).Scan(&id); err != nil {
		t.Fatalf("insert vehicle: %v", err)
	}
	return id
}

// driver inserts a driver in the fleet.
func (f jobsFleet) driver(t *testing.T, h *harness, name string) int64 {
	t.Helper()
	var id int64
	if err := h.db.QueryRow(`INSERT INTO drivers (fleet_id, name, license_no_hash) VALUES ($1, $2, $3) RETURNING id`,
		f.id, name, []byte(name)).Scan(&id); err != nil {
		t.Fatalf("insert driver: %v", err)
	}
	return id
}

// jobID reads the id from a job response.
func jobID(t *testing.T, r reply) int64 {
	t.Helper()
	var j struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(r.body, &j); err != nil || j.ID == 0 {
		t.Fatalf("job response %d %s has no id", r.status, r.body)
	}
	return j.ID
}

// createJob posts a job and returns its id, failing unless the answer is 201.
func createJob(t *testing.T, h *harness, tok, body string) int64 {
	t.Helper()
	r := h.do(t, http.MethodPost, "/jobs", tok, body, nil)
	if r.status != http.StatusCreated {
		t.Fatalf("create job = %d %s, want 201", r.status, r.body)
	}
	return jobID(t, r)
}

// jobCount counts the fleet's jobs directly, so refused writes are shown to persist nothing.
func jobCount(t *testing.T, h *harness, fleet int64) int {
	t.Helper()
	var n int
	if err := h.db.QueryRow(`SELECT count(*) FROM jobs WHERE fleet_id = $1`, fleet).Scan(&n); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	return n
}

// TestJobsCreateValidation is the POST /jobs table (GF-173). Refused bodies persist nothing.
func TestJobsCreateValidation(t *testing.T) {
	h := startServer(t)
	f := newJobsFleet(t, h, "validate")
	long := strings.Repeat("n", 1001)

	cases := []struct {
		name string
		body string
		want int
	}{
		{"valid minimal", `{` + jobPoints + `}`, http.StatusCreated},
		{"valid full", `{` + jobPoints + `,"window_start":"2026-10-26T09:00:00Z","window_end":"2026-10-26T12:00:00Z","eta_minutes":30,"notes":"ring twice","size_class":"medium","weight_kg":12.5}`, http.StatusCreated},
		{"size_class null", `{` + jobPoints + `,"size_class":null}`, http.StatusCreated},
		{"missing pickup", `{"dropoff":{"lat":41,"lng":29}}`, http.StatusBadRequest},
		{"missing dropoff", `{"pickup":{"lat":41,"lng":29}}`, http.StatusBadRequest},
		{"lat 91", `{"pickup":{"lat":91,"lng":29},"dropoff":{"lat":41,"lng":29}}`, http.StatusBadRequest},
		{"lng -181", `{"pickup":{"lat":41,"lng":-181},"dropoff":{"lat":41,"lng":29}}`, http.StatusBadRequest},
		{"window_end equal to start", `{` + jobPoints + `,"window_start":"2026-10-26T09:00:00Z","window_end":"2026-10-26T09:00:00Z"}`, http.StatusBadRequest},
		{"window_end before start", `{` + jobPoints + `,"window_start":"2026-10-26T09:00:00Z","window_end":"2026-10-26T08:00:00Z"}`, http.StatusBadRequest},
		{"eta -1", `{` + jobPoints + `,"eta_minutes":-1}`, http.StatusBadRequest},
		{"eta 1441", `{` + jobPoints + `,"eta_minutes":1441}`, http.StatusBadRequest},
		{"notes 1001 chars", `{` + jobPoints + `,"notes":"` + long + `"}`, http.StatusBadRequest},
		{"size_class huge", `{` + jobPoints + `,"size_class":"huge"}`, http.StatusBadRequest},
		{"weight_kg 0", `{` + jobPoints + `,"weight_kg":0}`, http.StatusBadRequest},
		{"weight_kg negative", `{` + jobPoints + `,"weight_kg":-3}`, http.StatusBadRequest},
		{"unknown field", `{` + jobPoints + `,"state":"assigned"}`, http.StatusBadRequest},
		{"vehicle_id on create", `{` + jobPoints + `,"vehicle_id":1}`, http.StatusBadRequest},
		{"unknown field in point", `{"pickup":{"lat":41,"lng":29,"alt":1},"dropoff":{"lat":41,"lng":29}}`, http.StatusBadRequest},
		{"malformed JSON", `{"pickup":`, http.StatusBadRequest},
		{"empty body", ``, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := jobCount(t, h, f.id)
			r := h.do(t, http.MethodPost, "/jobs", f.dispTok, tc.body, nil)
			if r.status != tc.want {
				t.Fatalf("status = %d (%s), want %d", r.status, r.body, tc.want)
			}
			if tc.want == http.StatusBadRequest {
				if r.errCode(t) != "validation_failed" {
					t.Fatalf("error = %q, want validation_failed", r.errCode(t))
				}
				if after := jobCount(t, h, f.id); after != before {
					t.Fatalf("refused create changed job count %d -> %d", before, after)
				}
				return
			}
			var j struct {
				State     string  `json:"state"`
				VehicleID *int64  `json:"vehicle_id"`
				DriverID  *int64  `json:"driver_id"`
				SizeClass *string `json:"size_class"`
			}
			if err := json.Unmarshal(r.body, &j); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if j.State != "created" || j.VehicleID != nil || j.DriverID != nil {
				t.Fatalf("created job = %s, want state created and unassigned", r.body)
			}
		})
	}
}

// listIDs runs GET /jobs with query and returns the ids in response order.
func listIDs(t *testing.T, h *harness, tok, query string) []int64 {
	t.Helper()
	r := h.do(t, http.MethodGet, "/jobs"+query, tok, "", nil)
	if r.status != http.StatusOK {
		t.Fatalf("GET /jobs%s = %d %s, want 200", query, r.status, r.body)
	}
	var out struct {
		Jobs []struct {
			ID int64 `json:"id"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(r.body, &out); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	ids := []int64{}
	for _, j := range out.Jobs {
		ids = append(ids, j.ID)
	}
	return ids
}

// TestJobsListFilters covers state, driver and day filters, the fleet timezone
// for day, NULL windows, and cross-fleet isolation (GF-174).
func TestJobsListFilters(t *testing.T) {
	h := startServer(t)
	f := newJobsFleet(t, h, "filters")
	other := newJobsFleet(t, h, "filters-other")
	if _, err := h.db.Exec(`UPDATE fleets SET timezone = 'Europe/Istanbul' WHERE id = $1`, f.id); err != nil {
		t.Fatalf("set timezone: %v", err)
	}
	vehicle := f.vehicle(t, h, "F-1")
	d1 := f.driver(t, h, "driver-one")
	d2 := f.driver(t, h, "driver-two")

	// j1: 23:30Z is 02:30 on the 27th in Istanbul, so its day is 2026-10-27.
	j1 := createJob(t, h, f.dispTok, `{`+jobPoints+`,"window_start":"2026-10-26T23:30:00Z","window_end":"2026-10-27T01:00:00Z"}`)
	assignJob(t, h, f.dispTok, j1, vehicle, d1)
	// j2: 10:00Z is 13:00 on the 26th in Istanbul.
	j2 := createJob(t, h, f.dispTok, `{`+jobPoints+`,"window_start":"2026-10-26T10:00:00Z","window_end":"2026-10-26T11:00:00Z"}`)
	// j3: no window, so it is excluded only when day is given.
	j3 := createJob(t, h, f.dispTok, `{`+jobPoints+`}`)
	assignJob(t, h, f.dispTok, j3, vehicle, d2)
	// Another fleet's job in the same window must never be listed.
	createJob(t, h, other.dispTok, `{`+jobPoints+`,"window_start":"2026-10-26T23:30:00Z","window_end":"2026-10-27T01:00:00Z"}`)

	cases := []struct {
		query string
		want  []int64
	}{
		{"", []int64{j1, j2, j3}},
		{"?state=assigned", []int64{j1, j3}},
		{"?state=created", []int64{j2}},
		{"?state=accepted", []int64{}},
		{"?driver=" + fmt.Sprint(d1), []int64{j1}},
		{"?driver=99999", []int64{}},
		{"?day=2026-10-27", []int64{j1}},
		{"?day=2026-10-26", []int64{j2}},
		{"?state=assigned&day=2026-10-27", []int64{j1}},
		{"?driver=" + fmt.Sprint(d2) + "&day=2026-10-26", []int64{}},
	}
	for _, tc := range cases {
		t.Run("query "+tc.query, func(t *testing.T) {
			if got := listIDs(t, h, f.dispTok, tc.query); !slices.Equal(got, tc.want) {
				t.Fatalf("GET /jobs%s = %v, want %v", tc.query, got, tc.want)
			}
		})
	}

	for _, bad := range []string{
		"?state=bogus", "?state=", "?driver=abc", "?driver=0", "?driver=-1",
		"?day=2026-02-30", "?day=26-10-2026", "?day=2026-1-6",
	} {
		t.Run("refused "+bad, func(t *testing.T) {
			r := h.do(t, http.MethodGet, "/jobs"+bad, f.dispTok, "", nil)
			if r.status != http.StatusBadRequest || r.errCode(t) != "validation_failed" {
				t.Fatalf("GET /jobs%s = %d %s, want 400 validation_failed", bad, r.status, r.body)
			}
		})
	}
}

// assignJob assigns over HTTP and fails unless the answer is 200.
func assignJob(t *testing.T, h *harness, tok string, job, vehicle, driver int64) {
	t.Helper()
	r := h.do(t, http.MethodPost, fmt.Sprintf("/jobs/%d/assign", job), tok,
		fmt.Sprintf(`{"vehicle_id":%d,"driver_id":%d}`, vehicle, driver), nil)
	if r.status != http.StatusOK {
		t.Fatalf("assign job %d = %d %s, want 200", job, r.status, r.body)
	}
}

// TestJobsCrossFleet404: a job, vehicle or driver of another fleet is not_found
// to the caller, and nothing changes (GF-178).
func TestJobsCrossFleet404(t *testing.T) {
	h := startServer(t)
	a := newJobsFleet(t, h, "cross-a")
	b := newJobsFleet(t, h, "cross-b")
	vA, dA := a.vehicle(t, h, "A-1"), a.driver(t, h, "a-driver")
	vB, dB := b.vehicle(t, h, "B-1"), b.driver(t, h, "b-driver")

	jobA := createJob(t, h, a.dispTok, `{`+jobPoints+`}`)
	jobB := createJob(t, h, b.dispTok, `{`+jobPoints+`}`)

	assignPath := func(id int64) string { return fmt.Sprintf("/jobs/%d/assign", id) }
	cancelPath := func(id int64) string { return fmt.Sprintf("/jobs/%d/cancel", id) }

	// Another fleet's job: assign and cancel are 404.
	if r := h.do(t, http.MethodPost, assignPath(jobA), b.adminTok,
		fmt.Sprintf(`{"vehicle_id":%d,"driver_id":%d}`, vB, dB), nil); r.status != http.StatusNotFound || r.errCode(t) != "not_found" {
		t.Fatalf("assign foreign job = %d %s, want 404", r.status, r.body)
	}
	if r := h.do(t, http.MethodPost, cancelPath(jobA), b.adminTok, "", nil); r.status != http.StatusNotFound || r.errCode(t) != "not_found" {
		t.Fatalf("cancel foreign job = %d %s, want 404", r.status, r.body)
	}

	// Own job, foreign vehicle or driver: 404 for each.
	if r := h.do(t, http.MethodPost, assignPath(jobB), b.dispTok,
		fmt.Sprintf(`{"vehicle_id":%d,"driver_id":%d}`, vA, dB), nil); r.status != http.StatusNotFound {
		t.Fatalf("assign with foreign vehicle = %d %s, want 404", r.status, r.body)
	}
	if r := h.do(t, http.MethodPost, assignPath(jobB), b.dispTok,
		fmt.Sprintf(`{"vehicle_id":%d,"driver_id":%d}`, vB, dA), nil); r.status != http.StatusNotFound {
		t.Fatalf("assign with foreign driver = %d %s, want 404", r.status, r.body)
	}

	// Nothing moved, and the owner still can.
	if got := listIDs(t, h, a.dispTok, "?state=created"); !slices.Equal(got, []int64{jobA}) {
		t.Fatalf("fleet A created jobs = %v, want [%d]", got, jobA)
	}
	if got := listIDs(t, h, b.dispTok, "?state=created"); !slices.Equal(got, []int64{jobB}) {
		t.Fatalf("fleet B created jobs = %v, want [%d]", got, jobB)
	}
	assignJob(t, h, b.dispTok, jobB, vB, dB)
	if r := h.do(t, http.MethodPost, cancelPath(jobA), a.dispTok, "", nil); r.status != http.StatusOK {
		t.Fatalf("own cancel = %d %s, want 200", r.status, r.body)
	}
}
