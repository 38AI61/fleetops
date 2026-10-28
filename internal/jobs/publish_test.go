package jobs

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"fleetops/internal/testinfra"
)

// envelopeFields is the part of a jobs.events body the hub filter reads.
type envelopeFields struct {
	DriverID     *int64 `json:"driver_id"`
	DriverUserID *int64 `json:"driver_user_id"`
}

// TestPublishEnvelopeCarriesLinkedUserID: the jobs.events envelope names the
// users.id linked to the driver, and null for an unlinked driver (GF-192, GF-341).
// It runs against real Postgres and real NATS.
func TestPublishEnvelopeCarriesLinkedUserID(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	ctx := context.Background()
	fleetID, _ := fleetWithAdmin(t, db, "publish-map")

	linked := fleetDriver(t, db, fleetID, "linked")
	var linkedUser int64
	if err := db.QueryRow(
		`INSERT INTO users (fleet_id, email, password_hash, role, driver_id) VALUES ($1, $2, 'x', 'driver', $3) RETURNING id`,
		fleetID, "linked-driver@example.test", linked).Scan(&linkedUser); err != nil {
		t.Fatalf("insert linked driver user: %v", err)
	}
	unlinked := fleetDriver(t, db, fleetID, "unlinked")

	nc, err := nats.Connect(testinfra.NATSURL(t))
	if err != nil {
		t.Fatalf("connect nats: %v", err)
	}
	t.Cleanup(nc.Close)
	subj := subject(fleetID)
	msgs := make(chan []byte, 2)
	sub, err := nc.Subscribe(subj, func(m *nats.Msg) { msgs <- m.Data })
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	if err := nc.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	s := &Store{DB: db}
	for _, driverID := range []int64{linked, unlinked} {
		d := driverID
		publishChange(ctx, s, nc, Change{
			Job:  Job{ID: 1, FleetID: fleetID, State: StateAssigned, DriverID: &d},
			From: StateCreated, To: StateAssigned, At: time.Now(),
		})
	}

	got := make([]envelopeFields, 0, 2)
	for range 2 {
		select {
		case body := <-msgs:
			var env envelopeFields
			if err := json.Unmarshal(body, &env); err != nil {
				t.Fatalf("decode envelope %s: %v", body, err)
			}
			got = append(got, env)
		case <-time.After(5 * time.Second):
			t.Fatal("envelope not published within 5s")
		}
	}

	if got[0].DriverID == nil || *got[0].DriverID != linked {
		t.Fatalf("linked envelope driver_id = %v, want %d", got[0].DriverID, linked)
	}
	if got[0].DriverUserID == nil || *got[0].DriverUserID != linkedUser {
		t.Fatalf("linked envelope driver_user_id = %v, want %d", got[0].DriverUserID, linkedUser)
	}
	if got[1].DriverID == nil || *got[1].DriverID != unlinked {
		t.Fatalf("unlinked envelope driver_id = %v, want %d", got[1].DriverID, unlinked)
	}
	if got[1].DriverUserID != nil {
		t.Fatalf("unlinked envelope driver_user_id = %d, want null", *got[1].DriverUserID)
	}
}
