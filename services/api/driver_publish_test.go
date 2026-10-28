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

// TestDriverJobsPublish: each applied driver move publishes exactly one
// jobs.events message with a non-null driver_id; replays, 409 and 404 publish
// nothing (GF-191). The fail reason is not on the wire (GF-189).
func TestDriverJobsPublish(t *testing.T) {
	h := startServer(t)
	f := newJobsFleet(t, h, "drvpub")
	v, d := f.vehicle(t, h, "DPB-1"), f.driver(t, h, "drvpub-driver")
	d2 := f.driver(t, h, "drvpub-other")
	tok := h.driverLogin(t, f.id, d, "drvpub-driver@jobs.example")
	tok2 := h.driverLogin(t, f.id, d2, "drvpub-other@jobs.example")

	sc, err := nats.Connect(testinfra.NATSURL(t))
	if err != nil {
		t.Fatalf("connect subscriber: %v", err)
	}
	t.Cleanup(sc.Close)
	sub, err := sc.SubscribeSync(fmt.Sprintf("jobs.events.%d", f.id))
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := sc.Flush(); err != nil {
		t.Fatalf("flush subscriber: %v", err)
	}

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
	// move sends one driver route and fails the test unless the answer is 200.
	move := func(tok string, job int64, action, body string) {
		t.Helper()
		if r := driverDo(t, h, tok, job, action, body); r.status != http.StatusOK {
			t.Fatalf("%s = %d %s, want 200", action, r.status, r.body)
		}
	}

	job := assignedJob(t, h, f, v, d)
	next("assign")

	move(tok, job, "accept", ``)
	ev := next("accept")
	if len(ev) != 8 {
		t.Fatalf("accept body has %d fields, want exactly 8 (driver_user_id, GF-341): %v", len(ev), ev)
	}
	if ev["type"] != "job.updated" || ev["from_state"] != "assigned" || ev["to_state"] != "accepted" {
		t.Fatalf("accept body = %v", ev)
	}
	if ev["driver_id"] != float64(d) || ev["fleet_id"] != float64(f.id) || ev["job_id"] != float64(job) {
		t.Fatalf("accept ids = %v", ev)
	}
	if at, _ := ev["at"].(string); at == "" {
		t.Fatalf("accept has no at: %v", ev)
	} else if _, err := time.Parse(eventTimeLayout, at); err != nil {
		t.Fatalf("at = %q, want RFC3339 ms Z: %v", at, err)
	}

	move(tok, job, "accept", ``)
	silent("replay accept")

	move(tok, job, "start", ``)
	if ev := next("start"); ev["from_state"] != "accepted" || ev["to_state"] != "en_route" {
		t.Fatalf("start body = %v", ev)
	}
	move(tok, job, "pickup", ``)
	if ev := next("pickup"); ev["from_state"] != "en_route" || ev["to_state"] != "picked_up" {
		t.Fatalf("pickup body = %v", ev)
	}

	move(tok, job, "fail", `{"reason":"gate locked"}`)
	ev = next("fail")
	if ev["from_state"] != "picked_up" || ev["to_state"] != "failed" || ev["driver_id"] != float64(d) {
		t.Fatalf("fail body = %v", ev)
	}
	if len(ev) != 8 {
		t.Fatalf("fail body has %d fields, want exactly 8 (no reason on the wire): %v", len(ev), ev)
	}
	move(tok, job, "fail", `{"reason":"second"}`)
	silent("replay fail")

	// Another driver's call is 404 and publishes nothing.
	open := assignedJob(t, h, f, v, d)
	next("assign open")
	if r := driverDo(t, h, tok2, open, "accept", ``); r.status != http.StatusNotFound {
		t.Fatalf("other driver accept = %d %s, want 404", r.status, r.body)
	}
	silent("other driver 404")

	// A refused move (409) publishes nothing: cancel first, then accept.
	cancelled := assignedJob(t, h, f, v, d)
	next("assign cancelled")
	if r := h.do(t, http.MethodPost, fmt.Sprintf("/jobs/%d/cancel", cancelled), f.dispTok, `{}`, nil); r.status != http.StatusOK {
		t.Fatalf("cancel = %d %s", r.status, r.body)
	}
	next("cancel")
	if r := driverDo(t, h, tok, cancelled, "accept", ``); r.status != http.StatusConflict {
		t.Fatalf("accept cancelled = %d %s, want 409", r.status, r.body)
	}
	silent("refused accept 409")
}
