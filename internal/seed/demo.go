package seed

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand/v2"
	"sort"
	"time"

	"fleetops/internal/devicesfile"
	"fleetops/internal/registry"
)

// DemoVehicleCount is the number of demo vehicles, a constant and not a flag (GF-136).
const DemoVehicleCount = 50

// Waypoint box (GF-142). It is a placeholder area with no DB read and no OSRM.
const (
	demoLatMin, demoLatMax = 52.45, 52.60
	demoLngMin, demoLngMax = 13.30, 13.50
)

// waypointStream is the second PCG seed. The device id is the first, so each
// device gets its own loop and reruns of the same device id reproduce it.
const waypointStream = 0x8f1a_2b3c_4d5e_6f70

// GenerateWaypoints returns the closed loop for one device (GF-142). The same
// device id always yields the same four points. A loop with two equal
// consecutive points, including the closing pair, is redrawn in full.
func GenerateWaypoints(deviceID int64) []devicesfile.Point {
	// Simulation geometry, not a secret, so a seeded PRNG is the intended tool (GF-142).
	rng := rand.New(rand.NewPCG(uint64(deviceID), waypointStream)) //nolint:gosec // seeded route data, not a credential
	for {
		pts := make([]devicesfile.Point, devicesfile.WaypointsPerDevice)
		for i := range pts {
			pts[i] = devicesfile.Point{
				Lat: demoLatMin + rng.Float64()*(demoLatMax-demoLatMin),
				Lng: demoLngMin + rng.Float64()*(demoLngMax-demoLngMin),
			}
		}
		if loopDistinct(pts) {
			return pts
		}
	}
}

// loopDistinct reports whether no two consecutive points of the closed loop are equal.
func loopDistinct(pts []devicesfile.Point) bool {
	for i := range pts {
		if pts[i] == pts[(i+1)%len(pts)] {
			return false
		}
	}
	return true
}

// RunDemo creates DemoVehicleCount vehicles, each with one tracker device
// paired 1:1, through the registry path so audit and key hashing are shared
// (GF-136). It then writes devices.json (GF-137, GF-139). Seed calls it only
// after it creates the first fleet, so a rerun never reaches this code.
func RunDemo(ctx context.Context, db *sql.DB, res Result, salt []byte, path string, clock func() time.Time) error {
	svc := &registry.Service{DB: db, Salt: salt, Clock: clock}
	actor := registry.Actor{UserID: res.UserID, FleetID: res.FleetID}

	devs := make([]devicesfile.Device, 0, DemoVehicleCount)
	for i := 1; i <= DemoVehicleCount; i++ {
		v, err := svc.CreateVehicle(ctx, actor, registry.VehicleInput{Plate: fmt.Sprintf("DEMO-%03d", i)})
		if err != nil {
			return fmt.Errorf("demo vehicle %d: %w; reset the database before rerunning", i, err)
		}
		d, err := svc.IssueDevice(ctx, actor, registry.KindTracker)
		if err != nil {
			return fmt.Errorf("demo device for vehicle %d: %w; reset the database before rerunning", i, err)
		}
		deviceID := d.ID
		if _, _, err := svc.SetPairing(ctx, actor, v.ID, registry.PairingInput{DeviceID: &deviceID}); err != nil {
			return fmt.Errorf("pair demo vehicle %d: %w; reset the database before rerunning", i, err)
		}
		devs = append(devs, devicesfile.Device{
			DeviceID:  d.ID,
			VehicleID: v.ID,
			Kind:      registry.KindTracker,
			Key:       d.APIKey,
			Waypoints: GenerateWaypoints(d.ID),
		})
	}
	sort.Slice(devs, func(a, b int) bool { return devs[a].DeviceID < devs[b].DeviceID })

	file := devicesfile.File{
		Version:     devicesfile.Version,
		FleetID:     res.FleetID,
		GeneratedAt: clock().UTC().Truncate(time.Second),
		Devices:     devs,
	}
	if err := devicesfile.Write(path, file); err != nil {
		return fmt.Errorf("demo devices exist in the database but the keys file was not written; reset the database: %w", err)
	}
	return nil
}
