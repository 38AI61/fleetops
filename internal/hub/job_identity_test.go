package hub

import (
	"testing"
)

// The job filter admits a driver only when driver_user_id equals the connection's
// users.id (GF-192, GF-341). The envelope carries the mapping the publisher
// resolved, so the hub never reads Postgres.

// TestDriverReceivesOwnJobUpdate: the GF-192 positive case. Driver user 42 is
// linked to driver 7, and receives the update for that job.
func TestDriverReceivesOwnJobUpdate(t *testing.T) {
	const fleet = 3020
	r := newRig(t, Options{})
	b := newBus(t, r)
	driver := r.dialAuth(fleet, r.token(42, fleet, "driver"))
	readAuthOK(t, driver, fleet, "driver")
	joined(t, r, 1)

	own := `{"type":"job.updated","fleet_id":3020,"job_id":1,"driver_id":7,"driver_user_id":42}`
	b.publishNATS("jobs.events.3020", own)
	if got := string(readMsg(t, driver)); got != own {
		t.Fatalf("driver got %s, want its own job update", got)
	}
}

// TestDriverDoesNotReceiveOtherDriverJobUpdate: the cross-id-space case. Driver
// user 7 has the same number as driver row 7, which belongs to user 42. The old
// filter compared the subject with driver_id and leaked this update to user 7.
func TestDriverDoesNotReceiveOtherDriverJobUpdate(t *testing.T) {
	const fleet = 3021
	r := newRig(t, Options{})
	b := newBus(t, r)
	userSeven := r.dialAuth(fleet, r.token(7, fleet, "driver"))
	readAuthOK(t, userSeven, fleet, "driver")
	joined(t, r, 1)

	otherDriver := `{"type":"job.updated","fleet_id":3021,"job_id":1,"driver_id":7,"driver_user_id":42}`
	end := `{"type":"job.updated","fleet_id":3021,"job_id":2,"driver_id":8,"driver_user_id":7}`
	b.publishNATS("jobs.events.3021", otherDriver)
	b.publishNATS("jobs.events.3021", end)
	// NATS keeps order, so the first frame is the end marker only if the other update was dropped.
	if got := string(readMsg(t, userSeven)); got != end {
		t.Fatalf("user 7 got %s, want only its own marker (other driver's job leaked)", got)
	}
}

// TestUnlinkedDriverReceivesNoJobUpdate: a null driver_user_id reaches no driver.
// A dispatcher receives the same event first, so the relay has run before the marker.
func TestUnlinkedDriverReceivesNoJobUpdate(t *testing.T) {
	const fleet = 3022
	r := newRig(t, Options{})
	b := newBus(t, r)
	disp := r.dialAuth(fleet, r.token(9, fleet, "dispatcher"))
	readAuthOK(t, disp, fleet, "dispatcher")
	driver := r.dialAuth(fleet, r.token(42, fleet, "driver"))
	readAuthOK(t, driver, fleet, "driver")
	joined(t, r, 2)

	unlinked := `{"type":"job.updated","fleet_id":3022,"job_id":1,"driver_id":7,"driver_user_id":null}`
	end := `{"type":"job.updated","fleet_id":3022,"job_id":2,"driver_id":42,"driver_user_id":42}`
	b.publishNATS("jobs.events.3022", unlinked)
	b.publishNATS("jobs.events.3022", end)
	if got := string(readMsg(t, disp)); got != unlinked {
		t.Fatalf("dispatcher got %s, want the unlinked update", got)
	}
	if got := string(readMsg(t, disp)); got != end {
		t.Fatalf("dispatcher got %s, want the end marker", got)
	}
	if got := string(readMsg(t, driver)); got != end {
		t.Fatalf("driver's first frame %s, want its own marker (unlinked update leaked)", got)
	}
}

// TestDispatcherStillReceivesAllJobUpdates: non-driver roles see every job
// update, linked or unlinked (GF-109, GF-116).
func TestDispatcherStillReceivesAllJobUpdates(t *testing.T) {
	const fleet = 3023
	r := newRig(t, Options{})
	b := newBus(t, r)
	disp := r.dialAuth(fleet, r.token(9, fleet, "dispatcher"))
	readAuthOK(t, disp, fleet, "dispatcher")
	joined(t, r, 1)

	linked := `{"type":"job.updated","fleet_id":3023,"job_id":1,"driver_id":7,"driver_user_id":42}`
	unlinked := `{"type":"job.updated","fleet_id":3023,"job_id":2,"driver_id":8,"driver_user_id":null}`
	b.publishNATS("jobs.events.3023", linked)
	b.publishNATS("jobs.events.3023", unlinked)
	for _, want := range []string{linked, unlinked} {
		if got := string(readMsg(t, disp)); got != want {
			t.Fatalf("dispatcher got %s, want %s", got, want)
		}
	}
}
