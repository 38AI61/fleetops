package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"fleetops/internal/testinfra"
)

// eventTimeLayout is the GF-179 at encoding: RFC3339, milliseconds, Z suffix.
const eventTimeLayout = "2006-01-02T15:04:05.000Z"

// quiet is how long a test waits to prove that no message arrives.
const quiet = 300 * time.Millisecond

// TestJobsPublishOnePerTransition: every applied transition publishes exactly
// one jobs.events message with the GF-179 body. Creation, replays and refusals
// publish nothing (GF-179).
func TestJobsPublishOnePerTransition(t *testing.T) {
	h := startServer(t)
	f := newJobsFleet(t, h, "publish")
	v, d := f.vehicle(t, h, "PB-1"), f.driver(t, h, "pb-driver")

	// A separate connection subscribes, so the harness's own publisher is what is tested.
	sc, err := nats.Connect(testinfra.NATSURL(t))
	if err != nil {
		t.Fatalf("connect subscriber: %v", err)
	}
	t.Cleanup(sc.Close)
	sub, err := sc.SubscribeSync("jobs.events.*")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := sc.Flush(); err != nil {
		t.Fatalf("flush subscriber: %v", err)
	}

	// next flushes the server's publisher, then returns the one message due, or fails if none came.
	next := func(what string) map[string]any {
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
		if msg.Subject != fmt.Sprintf("jobs.events.%d", f.id) {
			t.Fatalf("%s: subject = %s, want jobs.events.%d", what, msg.Subject, f.id)
		}
		return body
	}
	silent := func(what string) {
		t.Helper()
		if err := h.nc.Flush(); err != nil {
			t.Fatalf("flush publisher: %v", err)
		}
		if msg, err := sub.NextMsg(quiet); err == nil {
			t.Fatalf("%s: unexpected message %s %s", what, msg.Subject, msg.Data)
		} else if !errors.Is(err, nats.ErrTimeout) {
			t.Fatalf("%s: next message: %v", what, err)
		}
	}

	job := createJob(t, h, f.dispTok, `{`+jobPoints+`}`)
	silent("create")

	path := fmt.Sprintf("/jobs/%d/assign", job)
	assignBody := fmt.Sprintf(`{"vehicle_id":%d,"driver_id":%d}`, v, d)
	if r := h.do(t, http.MethodPost, path, f.dispTok, assignBody, nil); r.status != http.StatusOK {
		t.Fatalf("assign = %d %s", r.status, r.body)
	}
	ev := next("assign")
	if len(ev) != 7 {
		t.Fatalf("assign body has %d fields, want exactly 7: %v", len(ev), ev)
	}
	if ev["type"] != "job.updated" || ev["from_state"] != "created" || ev["to_state"] != "assigned" {
		t.Fatalf("assign body = %v", ev)
	}
	if ev["fleet_id"] != float64(f.id) || ev["job_id"] != float64(job) || ev["driver_id"] != float64(d) {
		t.Fatalf("assign ids = %v", ev)
	}
	at, _ := ev["at"].(string)
	if _, err := time.Parse(eventTimeLayout, at); err != nil {
		t.Fatalf("at = %q, want RFC3339 ms Z: %v", at, err)
	}

	// Replay and a refused assign on the cancelled job publish nothing.
	if r := h.do(t, http.MethodPost, path, f.dispTok, assignBody, nil); r.status != http.StatusOK {
		t.Fatalf("replay assign = %d %s", r.status, r.body)
	}
	silent("replay assign")

	cancel := fmt.Sprintf("/jobs/%d/cancel", job)
	if r := h.do(t, http.MethodPost, cancel, f.dispTok, "", nil); r.status != http.StatusOK {
		t.Fatalf("cancel = %d %s", r.status, r.body)
	}
	ev = next("cancel")
	if ev["from_state"] != "assigned" || ev["to_state"] != "cancelled" || ev["driver_id"] != float64(d) {
		t.Fatalf("cancel body = %v", ev)
	}
	if r := h.do(t, http.MethodPost, cancel, f.dispTok, "", nil); r.status != http.StatusOK {
		t.Fatalf("replay cancel = %d %s", r.status, r.body)
	}
	silent("replay cancel")
	if r := h.do(t, http.MethodPost, path, f.dispTok, assignBody, nil); r.status != http.StatusConflict {
		t.Fatalf("assign cancelled = %d %s, want 409", r.status, r.body)
	}
	silent("refused assign")

	// Cancel from created has no driver: published with driver_id null (GF-176).
	fresh := createJob(t, h, f.dispTok, `{`+jobPoints+`}`)
	if r := h.do(t, http.MethodPost, fmt.Sprintf("/jobs/%d/cancel", fresh), f.dispTok, "", nil); r.status != http.StatusOK {
		t.Fatalf("cancel fresh = %d %s", r.status, r.body)
	}
	ev = next("cancel from created")
	if ev["driver_id"] != nil || ev["from_state"] != "created" {
		t.Fatalf("cancel from created body = %v", ev)
	}
}

// failingPublisher stands in for a NATS connection that is down.
type failingPublisher struct{}

func (failingPublisher) Publish(string, []byte) error { return errors.New("nats unavailable") }

// TestJobsPublishFailureKeepsResponse: a publish error is logged and the HTTP
// answer and the committed state are the same as with a healthy publisher (GF-179).
func TestJobsPublishFailureKeepsResponse(t *testing.T) {
	h := startServerEvents(t, failingPublisher{})
	f := newJobsFleet(t, h, "publish-down")
	v, d := f.vehicle(t, h, "PD-1"), f.driver(t, h, "pd-driver")
	job := createJob(t, h, f.dispTok, `{`+jobPoints+`}`)

	r := h.do(t, http.MethodPost, fmt.Sprintf("/jobs/%d/assign", job), f.dispTok,
		fmt.Sprintf(`{"vehicle_id":%d,"driver_id":%d}`, v, d), nil)
	if r.status != http.StatusOK {
		t.Fatalf("assign with failing publisher = %d %s, want 200", r.status, r.body)
	}
	var j struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(r.body, &j); err != nil || j.State != "assigned" {
		t.Fatalf("assign body = %s, want state assigned", r.body)
	}
	if got := jobState(t, h, job); got != "assigned" {
		t.Fatalf("persisted state = %s, want assigned", got)
	}
	if r := h.do(t, http.MethodPost, fmt.Sprintf("/jobs/%d/cancel", job), f.dispTok, "", nil); r.status != http.StatusOK {
		t.Fatalf("cancel with failing publisher = %d %s, want 200", r.status, r.body)
	}
}
