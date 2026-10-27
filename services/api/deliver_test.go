package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/nats-io/nats.go"

	"fleetops/internal/jobs"
	"fleetops/internal/proofs"
	"fleetops/internal/proofs/prooftest"
	"fleetops/internal/testinfra"
)

// Photo fixtures. Each starts with the magic bytes http.DetectContentType sniffs.
var (
	jpegPhoto = append([]byte("\xff\xd8\xff\xe0"), []byte("proof-jpeg-bytes")...)
	pngPhoto  = append([]byte("\x89PNG\r\n\x1a\n"), []byte("proof-png-bytes")...)
	webpPhoto = append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), []byte("proof-webp-bytes")...)
)

// deliverForm builds a multipart body. A nil photo sends no photo part. fields
// are text parts. It returns the Content-Type with its boundary and the body.
func deliverForm(t *testing.T, photo []byte, fields map[string]string) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if photo != nil {
		part, err := w.CreateFormFile("photo", "proof.bin")
		if err != nil {
			t.Fatalf("form file: %v", err)
		}
		if _, err := part.Write(photo); err != nil {
			t.Fatalf("write photo: %v", err)
		}
	}
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatalf("write field %s: %v", k, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close form: %v", err)
	}
	return w.FormDataContentType(), buf.Bytes()
}

// validCoords are the lat/lng fields of a valid deliver form.
var validCoords = map[string]string{"lat": "41.0422", "lng": "29.0083"}

// send sends one request with an explicit Content-Type, for the multipart route.
func (h *harness) send(t *testing.T, method, path, bearer, ctype string, body []byte) reply {
	t.Helper()
	req, err := http.NewRequest(method, h.url+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var b bytes.Buffer
	_, _ = b.ReadFrom(resp.Body)
	return reply{status: resp.StatusCode, body: b.Bytes()}
}

// storedObjects counts every object in the proofs bucket. Tests share one MinIO
// container and run one at a time, so a before/after delta is exact.
func storedObjects(t *testing.T) int {
	t.Helper()
	n := 0
	for obj := range prooftest.Client(t).ListObjects(context.Background(), proofs.Bucket, minio.ListObjectsOptions{Recursive: true}) {
		if obj.Err != nil {
			t.Fatalf("list proofs: %v", obj.Err)
		}
		n++
	}
	return n
}

// jobProof reads proof_photo for a job, or "" when the column is null.
func jobProof(t *testing.T, h *harness, job int64) string {
	t.Helper()
	var key *string
	if err := h.db.QueryRow(`SELECT proof_photo FROM jobs WHERE id = $1`, job).Scan(&key); err != nil {
		t.Fatalf("read proof_photo: %v", err)
	}
	if key == nil {
		return ""
	}
	return *key
}

// pickedUpJob assigns a job to the driver, advances it through steps, and returns its id.
func pickedUpJob(t *testing.T, h *harness, f jobsFleet, vehicle, driver int64, steps ...jobs.State) int64 {
	t.Helper()
	job := assignedJob(t, h, f, vehicle, driver)
	for _, to := range steps {
		advance(t, h, f, job, to)
	}
	return job
}

// deliverPathFor is the deliver route for one job.
func deliverPathFor(job int64) string {
	return fmt.Sprintf("/driver/jobs/%d/deliver", job)
}

// jobsEvents subscribes to jobs.events for the fleet and returns a next-message
// helper and a silent helper, as the publish tests do.
func jobsEvents(t *testing.T, h *harness, fleet int64) (next func(string) map[string]any, silent func(string)) {
	t.Helper()
	sc, err := nats.Connect(testinfra.NATSURL(t))
	if err != nil {
		t.Fatalf("connect subscriber: %v", err)
	}
	t.Cleanup(sc.Close)
	sub, err := sc.SubscribeSync(fmt.Sprintf("jobs.events.%d", fleet))
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := sc.Flush(); err != nil {
		t.Fatalf("flush subscriber: %v", err)
	}
	next = func(what string) map[string]any {
		t.Helper()
		if err := h.nc.Flush(); err != nil {
			t.Fatalf("flush publisher: %v", err)
		}
		msg, err := sub.NextMsg(2 * time.Second)
		if err != nil {
			t.Fatalf("%s: no jobs.events message: %v", what, err)
		}
		var body map[string]any
		if err := json.Unmarshal(msg.Data, &body); err != nil {
			t.Fatalf("%s: decode body: %v", what, err)
		}
		return body
	}
	silent = func(what string) {
		t.Helper()
		if err := h.nc.Flush(); err != nil {
			t.Fatalf("flush publisher: %v", err)
		}
		if msg, err := sub.NextMsg(quiet); err == nil {
			t.Fatalf("%s: unexpected jobs.events message %s", what, msg.Data)
		}
	}
	return next, silent
}

// TestDriverDeliver: a picked_up job delivered with a JPEG (GF-194, GF-200). The
// response carries the delivered job with a proof URL that serves the exact bytes.
// One picked_up->delivered event and one job.updated with driver_id are written.
// A replay with another photo is 200, writes nothing, stores nothing, and keeps the first key.
func TestDriverDeliver(t *testing.T) {
	h := startServer(t)
	f := newJobsFleet(t, h, "deliver")
	v, d := f.vehicle(t, h, "DLV-1"), f.driver(t, h, "deliver-driver")
	tok := h.driverLogin(t, f.id, d, "deliver-driver@jobs.example")
	job := pickedUpJob(t, h, f, v, d, jobs.StateAccepted, jobs.StateEnRoute, jobs.StatePickedUp)
	next, silent := jobsEvents(t, h, f.id)

	ctype, body := deliverForm(t, jpegPhoto, validCoords)
	before := storedObjects(t)
	r := h.send(t, http.MethodPost, deliverPathFor(job), tok, ctype, body)
	if r.status != http.StatusOK {
		t.Fatalf("deliver = %d %s, want 200", r.status, r.body)
	}
	var got struct {
		State         string  `json:"state"`
		ProofPhotoURL *string `json:"proof_photo_url"`
	}
	if err := json.Unmarshal(r.body, &got); err != nil {
		t.Fatalf("decode deliver: %v", err)
	}
	if got.State != "delivered" || got.ProofPhotoURL == nil {
		t.Fatalf("deliver body state=%q url=%v, want delivered with a proof URL", got.State, got.ProofPhotoURL)
	}
	if storedObjects(t) != before+1 {
		t.Fatalf("objects after first deliver = %d, want %d", storedObjects(t), before+1)
	}

	key := jobProof(t, h, job)
	prefix := fmt.Sprintf("proofs/%d/%d/", f.id, job)
	if !strings.HasPrefix(key, prefix) || !strings.HasSuffix(key, ".jpg") {
		t.Fatalf("proof key %q, want %s<uuid>.jpg", key, prefix)
	}
	if _, err := prooftest.Client(t).StatObject(context.Background(), proofs.Bucket, key, minio.StatObjectOptions{}); err != nil {
		t.Fatalf("stat stored proof: %v", err)
	}
	status, served, err := rawCall(http.MethodGet, *got.ProofPhotoURL, "", "")
	if err != nil || status != http.StatusOK || !bytes.Equal(served, jpegPhoto) {
		t.Fatalf("proof URL = %d %v (%d bytes), want 200 with the exact photo", status, err, len(served))
	}
	if n := eventCountTo(t, h, job, "delivered"); n != 1 {
		t.Fatalf("delivered events = %d, want 1", n)
	}

	msg := next("deliver")
	if msg["type"] != "job.updated" || msg["to_state"] != "delivered" || msg["driver_id"] == nil {
		t.Fatalf("job.updated = %v, want delivered with driver_id", msg)
	}

	// Replay: a different valid photo is validated, then not stored, and nothing is written.
	events := eventCount(t, h, job)
	ctype2, body2 := deliverForm(t, pngPhoto, validCoords)
	before = storedObjects(t)
	r2 := h.send(t, http.MethodPost, deliverPathFor(job), tok, ctype2, body2)
	if r2.status != http.StatusOK {
		t.Fatalf("replay = %d %s, want 200", r2.status, r2.body)
	}
	if storedObjects(t) != before {
		t.Fatalf("replay uploaded: objects %d -> %d", before, storedObjects(t))
	}
	if got := jobProof(t, h, job); got != key {
		t.Fatalf("replay changed proof key %q -> %q", key, got)
	}
	if n := eventCount(t, h, job); n != events {
		t.Fatalf("replay wrote events: %d -> %d", events, n)
	}
	silent("replay")

	// WebP is accepted too, with its own extension (GF-199). It goes on a fresh picked_up job.
	webJob := pickedUpJob(t, h, f, v, d, jobs.StateAccepted, jobs.StateEnRoute, jobs.StatePickedUp)
	ctype3, body3 := deliverForm(t, webpPhoto, validCoords)
	if r := h.send(t, http.MethodPost, deliverPathFor(webJob), tok, ctype3, body3); r.status != http.StatusOK {
		t.Fatalf("webp deliver = %d %s, want 200", r.status, r.body)
	}
	if got := jobProof(t, h, webJob); !strings.HasSuffix(got, ".webp") {
		t.Fatalf("webp proof key %q, want a .webp suffix", got)
	}
}

// TestDriverDeliverValidation covers the GF-197 gate and the GF-199/GF-200 body
// rules. Every refusal stores nothing (object count unchanged) and writes no event.
func TestDriverDeliverValidation(t *testing.T) {
	h := startServer(t)
	f := newJobsFleet(t, h, "dlvval")
	v, d := f.vehicle(t, h, "DLV-V"), f.driver(t, h, "dlvval-driver")
	tok := h.driverLogin(t, f.id, d, "dlvval-driver@jobs.example")
	job := pickedUpJob(t, h, f, v, d, jobs.StateAccepted, jobs.StateEnRoute, jobs.StatePickedUp)
	enRoute := pickedUpJob(t, h, f, v, d, jobs.StateAccepted, jobs.StateEnRoute)
	path := deliverPathFor(job)

	oversize := append([]byte("\xff\xd8\xff\xe0"), make([]byte, maxPhotoBytesForTest)...)
	cases := []struct {
		name   string
		method string
		path   string
		token  string
		photo  []byte
		fields map[string]string
		want   int
		code   string
	}{
		{"oversize 5 MiB + 1", http.MethodPost, path, tok, oversize, validCoords, http.StatusRequestEntityTooLarge, "payload_too_large"},
		{"wrong MIME", http.MethodPost, path, tok, []byte("plain text, not an image"), validCoords, http.StatusBadRequest, "validation_failed"},
		{"missing photo", http.MethodPost, path, tok, nil, validCoords, http.StatusBadRequest, "validation_failed"},
		{"missing lat", http.MethodPost, path, tok, jpegPhoto, map[string]string{"lng": "29"}, http.StatusBadRequest, "validation_failed"},
		{"missing lng", http.MethodPost, path, tok, jpegPhoto, map[string]string{"lat": "41"}, http.StatusBadRequest, "validation_failed"},
		{"lat 91", http.MethodPost, path, tok, jpegPhoto, map[string]string{"lat": "91", "lng": "29"}, http.StatusBadRequest, "validation_failed"},
		{"lng 181", http.MethodPost, path, tok, jpegPhoto, map[string]string{"lat": "41", "lng": "181"}, http.StatusBadRequest, "validation_failed"},
		{"lat NaN", http.MethodPost, path, tok, jpegPhoto, map[string]string{"lat": "NaN", "lng": "29"}, http.StatusBadRequest, "validation_failed"},
		{"lng +Inf", http.MethodPost, path, tok, jpegPhoto, map[string]string{"lat": "41", "lng": "+Inf"}, http.StatusBadRequest, "validation_failed"},
		{"en_route job", http.MethodPost, deliverPathFor(enRoute), tok, jpegPhoto, validCoords, http.StatusConflict, "conflict"},
		{"malformed id", http.MethodPost, "/driver/jobs/abc/deliver", tok, jpegPhoto, validCoords, http.StatusNotFound, "not_found"},
		{"non-driver with a valid body", http.MethodPost, path, f.dispTok, jpegPhoto, validCoords, http.StatusForbidden, "forbidden"},
		{"no token", http.MethodPost, path, "", jpegPhoto, validCoords, http.StatusUnauthorized, "unauthorized"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctype, body := deliverForm(t, tc.photo, tc.fields)
			events := eventCount(t, h, job) + eventCount(t, h, enRoute)
			before := storedObjects(t)
			r := h.send(t, tc.method, tc.path, tc.token, ctype, body)
			if r.status != tc.want || r.errCode(t) != tc.code {
				t.Fatalf("status = %d %s, want %d %s", r.status, r.body, tc.want, tc.code)
			}
			if storedObjects(t) != before {
				t.Fatalf("refusal stored an object: %d -> %d", before, storedObjects(t))
			}
			if got := eventCount(t, h, job) + eventCount(t, h, enRoute); got != events {
				t.Fatalf("refusal wrote events: %d -> %d", events, got)
			}
		})
	}
	if jobProof(t, h, job) != "" || jobState(t, h, job) != "picked_up" {
		t.Fatalf("refusals changed the job: proof=%q state=%s", jobProof(t, h, job), jobState(t, h, job))
	}
}

// maxPhotoBytesForTest is 5 MiB, the proof cap (D-25). The +1 byte makes the part oversize.
const maxPhotoBytesForTest = 5 * 1024 * 1024

// TestDriverDeliverIsolation: a driver on another driver's job, and a driver from
// another fleet, both get 404. Nothing is stored and no event is written.
func TestDriverDeliverIsolation(t *testing.T) {
	h := startServer(t)
	f := newJobsFleet(t, h, "dlviso")
	v := f.vehicle(t, h, "DLV-I")
	mine, theirs := f.driver(t, h, "dlviso-mine"), f.driver(t, h, "dlviso-theirs")
	tokMine := h.driverLogin(t, f.id, mine, "dlviso-mine@jobs.example")
	job := pickedUpJob(t, h, f, v, theirs, jobs.StateAccepted, jobs.StateEnRoute, jobs.StatePickedUp)

	other := newJobsFleet(t, h, "dlviso-other")
	otherDriver := other.driver(t, h, "dlviso-outsider")
	tokOther := h.driverLogin(t, other.id, otherDriver, "dlviso-outsider@jobs.example")

	ctype, body := deliverForm(t, jpegPhoto, validCoords)
	cases := []struct {
		name  string
		token string
	}{
		{"same fleet, another driver", tokMine},
		{"cross-fleet driver", tokOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := eventCount(t, h, job)
			before := storedObjects(t)
			r := h.send(t, http.MethodPost, deliverPathFor(job), tc.token, ctype, body)
			if r.status != http.StatusNotFound || r.errCode(t) != "not_found" {
				t.Fatalf("status = %d %s, want 404 not_found", r.status, r.body)
			}
			if storedObjects(t) != before {
				t.Fatalf("isolation refusal stored an object")
			}
			if got := eventCount(t, h, job); got != events {
				t.Fatalf("isolation refusal wrote events: %d -> %d", events, got)
			}
		})
	}
	if jobProof(t, h, job) != "" {
		t.Fatalf("isolation refusal set a proof on the job")
	}
}
