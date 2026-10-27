package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// proofField returns proof_photo_url of one job object and whether the key is present (GF-196).
func proofField(t *testing.T, obj map[string]any) (any, bool) {
	t.Helper()
	v, ok := obj["proof_photo_url"]
	return v, ok
}

// requireNullProof checks a single-job response: status, and proof_photo_url present and null.
func requireNullProof(t *testing.T, r reply, status int) {
	t.Helper()
	if r.status != status {
		t.Fatalf("status = %d %s, want %d", r.status, r.body, status)
	}
	var obj map[string]any
	if err := json.Unmarshal(r.body, &obj); err != nil {
		t.Fatalf("decode job: %v", err)
	}
	if v, ok := proofField(t, obj); !ok || v != nil {
		t.Fatalf("proof_photo_url = %v (present %v), want present and null: %s", v, ok, r.body)
	}
}

// listProofs reads a job list response and returns each job's proof_photo_url, by id.
// It fails when a job object lacks the key.
func listProofs(t *testing.T, r reply) map[int64]any {
	t.Helper()
	if r.status != http.StatusOK {
		t.Fatalf("list = %d %s, want 200", r.status, r.body)
	}
	var out struct {
		Jobs []map[string]any `json:"jobs"`
	}
	if err := json.Unmarshal(r.body, &out); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	urls := map[int64]any{}
	for _, obj := range out.Jobs {
		v, ok := proofField(t, obj)
		if !ok {
			t.Fatalf("list job %v lacks proof_photo_url", obj["id"])
		}
		urls[int64(obj["id"].(float64))] = v
	}
	return urls
}

// TestJobProofURLs: every job-emitting endpoint carries proof_photo_url (GF-196).
// Jobs without a proof are null on create, assign, cancel, the driver transitions,
// the dispatcher list and the driver today list. A delivered job's URL is non-null
// and its GET returns the exact photo bytes.
func TestJobProofURLs(t *testing.T) {
	h := startServer(t)
	f := newJobsFleet(t, h, "proofurl")
	v, d := f.vehicle(t, h, "PRL-1"), f.driver(t, h, "proofurl-driver")
	tok := h.driverLogin(t, f.id, d, "proofurl-driver@jobs.example")

	// Create: present and null.
	created := h.do(t, http.MethodPost, "/jobs", f.dispTok, `{`+jobPoints+`}`, nil)
	requireNullProof(t, created, http.StatusCreated)
	job := jobID(t, created)

	// Assign: present and null.
	requireNullProof(t, h.do(t, http.MethodPost, fmt.Sprintf("/jobs/%d/assign", job), f.dispTok,
		fmt.Sprintf(`{"vehicle_id":%d,"driver_id":%d}`, v, d), nil), http.StatusOK)

	// Driver accept: present and null.
	requireNullProof(t, driverDo(t, h, tok, job, "accept", ""), http.StatusOK)

	// Driver today and the dispatcher list both carry the key, null for this job.
	today := listProofs(t, h.do(t, http.MethodGet, "/driver/jobs/today", tok, "", nil))
	if v, ok := today[job]; !ok || v != nil {
		t.Fatalf("driver today proof_photo_url for job %d = %v (present %v), want null", job, v, ok)
	}
	all := listProofs(t, h.do(t, http.MethodGet, "/jobs", f.dispTok, "", nil))
	if v, ok := all[job]; !ok || v != nil {
		t.Fatalf("dispatcher list proof_photo_url for job %d = %v (present %v), want null", job, v, ok)
	}

	// Cancel a second job: present and null.
	other := createJob(t, h, f.dispTok, `{`+jobPoints+`}`)
	requireNullProof(t, h.do(t, http.MethodPost, fmt.Sprintf("/jobs/%d/cancel", other), f.dispTok, "", nil), http.StatusOK)

	// Deliver: the URL is non-null and serves the exact bytes.
	driverDo(t, h, tok, job, "start", "")
	driverDo(t, h, tok, job, "pickup", "")
	ctype, body := deliverForm(t, jpegPhoto, validCoords)
	r := h.send(t, http.MethodPost, deliverPathFor(job), tok, ctype, body)
	if r.status != http.StatusOK {
		t.Fatalf("deliver = %d %s, want 200", r.status, r.body)
	}
	var delivered map[string]any
	if err := json.Unmarshal(r.body, &delivered); err != nil {
		t.Fatalf("decode deliver: %v", err)
	}
	url, ok := delivered["proof_photo_url"].(string)
	if !ok || url == "" {
		t.Fatalf("delivered proof_photo_url = %v, want a non-null URL", delivered["proof_photo_url"])
	}
	status, served, err := rawCall(http.MethodGet, url, "", "")
	if err != nil || status != http.StatusOK || !bytes.Equal(served, jpegPhoto) {
		t.Fatalf("proof URL GET = %d %v (%d bytes), want 200 with the exact photo", status, err, len(served))
	}

	// The dispatcher list shows the URL for the delivered job and null for the others.
	all = listProofs(t, h.do(t, http.MethodGet, "/jobs", f.dispTok, "", nil))
	if got, ok := all[job].(string); !ok || got != url {
		t.Fatalf("dispatcher list URL for delivered job = %v, want the deliver URL", all[job])
	}
	if v, ok := all[other]; !ok || v != nil {
		t.Fatalf("dispatcher list URL for cancelled job = %v (present %v), want null", v, ok)
	}
}
