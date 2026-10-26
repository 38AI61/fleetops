package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"fleetops/internal/devicesfile"
	"fleetops/internal/registry"
	"fleetops/internal/simulator"
)

// e2eWaypoints is a closed loop inside the demo box (GF-142).
var e2eWaypoints = []devicesfile.Point{
	{Lat: 52.51, Lng: 13.34}, {Lat: 52.55, Lng: 13.42}, {Lat: 52.48, Lng: 13.47}, {Lat: 52.58, Lng: 13.31},
}

// e2eVehicles issues n tracker devices through the real registry and builds the
// simulator vehicles for them. The API harness and ingest are real.
func e2eVehicles(t *testing.T, h *harness, svc *registry.Service, actor registry.Actor, n int) ([]simulator.Vehicle, []int64) {
	t.Helper()
	vehicles := make([]simulator.Vehicle, 0, n)
	ids := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		dev, err := svc.IssueDevice(context.Background(), actor, registry.KindTracker)
		if err != nil {
			t.Fatalf("issue device %d: %v", i, err)
		}
		route, err := simulator.NewRoute(dev.ID, e2eWaypoints)
		if err != nil {
			t.Fatalf("route %d: %v", i, err)
		}
		vehicles = append(vehicles, simulator.Vehicle{Index: i, DeviceID: dev.ID, Key: dev.APIKey, Route: route})
		ids = append(ids, dev.ID)
	}
	return vehicles, ids
}

// e2eSetup returns the harness, the registry service, and the seeded admin actor for a fresh fleet.
// The simulator stamps real device time, so the harness clock moves to now.
// Ingest checks device_ts against that clock (GF-56).
func e2eSetup(t *testing.T) (*harness, *registry.Service, registry.Actor) {
	t.Helper()
	h := startServer(t)
	h.clock.t = time.Now().UTC()
	fleet := h.fleet(t, "sim")
	adminID := h.user(t, fleet, "sim-admin@sim.example", "admin")
	svc := &registry.Service{DB: h.db, Salt: testSalt, Clock: h.clock.now}
	return h, svc, registry.Actor{UserID: adminID, FleetID: fleet}
}

// TestSimulatorSustainsConfiguredRate is the roadmap Validation item: 4
// vehicles at 250 ms over a 3 s window. Each vehicle accepts floor(3s/250ms) = 12
// within ±2, the aggregate is at least 95%, and no slot is skipped (GF-144, GF-146).
func TestSimulatorSustainsConfiguredRate(t *testing.T) {
	h, svc, actor := e2eSetup(t)
	vehicles, _ := e2eVehicles(t, h, svc, actor, 4)

	const rate = 250 * time.Millisecond
	const window = 3 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), window)
	defer cancel()

	var log bytes.Buffer
	sum, err := simulator.Run(ctx, simulator.Config{
		Vehicles: vehicles,
		Rate:     rate,
		Sender:   simulator.NewHTTPSender(h.url, len(vehicles)),
		Log:      &log,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := int64(window / rate) // 12
	t.Logf("per-vehicle accepted: %v; total %+v", acceptedCounts(sum), sum.Total)
	for i, c := range sum.PerVehicle {
		if c.Accepted < want-2 || c.Accepted > want+2 {
			t.Errorf("vehicle %d accepted %d, want %d±2 (tally %+v)", i, c.Accepted, want, c)
		}
	}
	expected := int64(len(vehicles)) * want
	if sum.Total.Accepted*100 < expected*95 {
		t.Errorf("aggregate accepted %d below 95%% of %d (tally %+v)", sum.Total.Accepted, expected, sum.Total)
	}
	if sum.Total.Skipped != 0 {
		t.Errorf("skipped %d slots, want 0", sum.Total.Skipped)
	}
	if sum.Total.Rejected != 0 || sum.Total.Failed != 0 {
		t.Errorf("rejected %d failed %d, want 0 (log %q)", sum.Total.Rejected, sum.Total.Failed, log.String())
	}
}

// acceptedCounts lists the accepted count of each vehicle, for test output.
func acceptedCounts(sum simulator.Summary) []int64 {
	out := make([]int64, 0, len(sum.PerVehicle))
	for _, c := range sum.PerVehicle {
		out = append(out, c.Accepted)
	}
	return out
}

// TestSimulatorSurvivesRejectedKey: a device revoked mid-run gets 401s, which
// are counted as rejected. The other vehicles keep being accepted, and Run returns
// no error (GF-146).
func TestSimulatorSurvivesRejectedKey(t *testing.T) {
	h, svc, actor := e2eSetup(t)
	vehicles, ids := e2eVehicles(t, h, svc, actor, 4)

	// Revoke vehicle 0's device after about a second of traffic.
	revoked := make(chan error, 1)
	timer := time.AfterFunc(time.Second, func() {
		_, err := svc.RevokeDevice(context.Background(), actor, ids[0])
		revoked <- err
	})
	defer timer.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	sum, err := simulator.Run(ctx, simulator.Config{
		Vehicles: vehicles,
		Rate:     250 * time.Millisecond,
		Sender:   simulator.NewHTTPSender(h.url, len(vehicles)),
	})
	if err != nil {
		t.Fatalf("Run exited with error: %v", err)
	}
	if err := <-revoked; err != nil {
		t.Fatalf("revoke: %v", err)
	}

	if sum.PerVehicle[0].Rejected == 0 {
		t.Fatalf("revoked vehicle had no rejections: %+v", sum.PerVehicle[0])
	}
	if sum.PerVehicle[0].Accepted == 0 {
		t.Fatalf("revoked vehicle had no accepts before revocation: %+v", sum.PerVehicle[0])
	}
	for i := 1; i < len(sum.PerVehicle); i++ {
		c := sum.PerVehicle[i]
		if c.Rejected != 0 || c.Accepted < 8 {
			t.Errorf("vehicle %d: %+v, want all accepted", i, c)
		}
	}
}
