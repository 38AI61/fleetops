package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"pgregory.net/rapid"

	"fleetops/internal/testinfra"
)

// TestPairingInvariantProperty drives random pair, unpair, rotate, revoke, and
// issue sequences over one fleet (GF-40, GF-46). Each check gets a fresh
// migrated database and an injected clock that advances one second per op.
func TestPairingInvariantProperty(t *testing.T) {
	newDB := testinfra.NewMigratedDBFrom(t)
	start := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()

	rapid.Check(t, func(rt *rapid.T) {
		db := newDB(rt)
		now := start
		svc := &Service{DB: db, Salt: storeSalt, Clock: func() time.Time { return now }}

		fleet := must(scalar(db, `INSERT INTO fleets (name) VALUES ('P') RETURNING id`))
		actor := Actor{
			UserID:  must(scalar(db, `INSERT INTO users (fleet_id, email, password_hash, role) VALUES ($1, 'p@example.test', 'x', 'admin') RETURNING id`, fleet)),
			FleetID: fleet,
		}
		var vehicles, devices, drivers []int64
		for i := range 3 {
			vehicles = append(vehicles, must(svc.CreateVehicle(ctx, actor, VehicleInput{Plate: fmt.Sprintf("P%d", i)})).ID)
			devices = append(devices, must(svc.IssueDevice(ctx, actor, KindPWA)).ID)
		}
		for i := range 2 {
			drivers = append(drivers, must(svc.CreateDriver(ctx, actor, DriverInput{Name: fmt.Sprintf("D%d", i), LicenseNo: fmt.Sprintf("PLIC%04d", i)})).ID)
		}

		n := rapid.IntRange(1, 40).Draw(rt, "length")
		for range n {
			now = now.Add(time.Second)
			kind := rapid.SampledFrom([]string{"pair", "rotate", "revoke", "issue"}).Draw(rt, "op")
			before := pairingSnapshot(rt, db)

			switch kind {
			case "pair":
				in := PairingInput{}
				vehicle := vehicles[rapid.IntRange(0, len(vehicles)-1).Draw(rt, "vehicle")]
				if rapid.Bool().Draw(rt, "pairing") {
					d := devices[rapid.IntRange(0, len(devices)-1).Draw(rt, "device")]
					in.DeviceID = &d
					if rapid.Bool().Draw(rt, "withDriver") {
						dr := drivers[rapid.IntRange(0, len(drivers)-1).Draw(rt, "driver")]
						in.DriverID = &dr
					}
				}
				_, _, err := svc.SetPairing(ctx, actor, vehicle, in)
				if rejected(err) {
					if after := pairingSnapshot(rt, db); after != before {
						rt.Fatalf("rejected pair (%v) changed state:\n before %s\n after  %s", err, before, after)
					}
				} else if err != nil {
					rt.Fatalf("pair: %v", err)
				}
			case "rotate":
				d := devices[rapid.IntRange(0, len(devices)-1).Draw(rt, "device")]
				_, err := svc.RotateDevice(ctx, actor, d)
				if err != nil && !errors.Is(err, ErrDeviceRevoked) {
					rt.Fatalf("rotate: %v", err)
				}
				if after := pairingSnapshot(rt, db); after != before {
					rt.Fatalf("rotate changed pairing state")
				}
			case "revoke":
				d := devices[rapid.IntRange(0, len(devices)-1).Draw(rt, "device")]
				if _, err := svc.RevokeDevice(ctx, actor, d); err != nil {
					rt.Fatalf("revoke: %v", err)
				}
			case "issue":
				devices = append(devices, must(svc.IssueDevice(ctx, actor, KindTracker)).ID)
			}
			checkPairingInvariants(rt, db)
		}
	})
}

// rejected reports whether err is one of the refusals that must leave state unchanged.
func rejected(err error) bool {
	return errors.Is(err, ErrDeviceInUse) || errors.Is(err, ErrDeviceRevoked) || errors.Is(err, ErrVehicleNotActive)
}

// checkPairingInvariants asserts the P3 invariants by SQL after every op (GF-40).
func checkPairingInvariants(rt *rapid.T, db *sql.DB) {
	rt.Helper()
	count := func(query string) int {
		var n int
		if err := db.QueryRow(query).Scan(&n); err != nil {
			rt.Fatalf("invariant query: %v", err)
		}
		return n
	}
	if n := count(`SELECT count(*) FROM (SELECT vehicle_id FROM vehicle_pairings WHERE ended_at IS NULL GROUP BY vehicle_id HAVING count(*) > 1) x`); n != 0 {
		rt.Fatalf("%d vehicles with more than one open pairing", n)
	}
	if n := count(`SELECT count(*) FROM (SELECT device_id FROM vehicle_pairings WHERE ended_at IS NULL GROUP BY device_id HAVING count(*) > 1) x`); n != 0 {
		rt.Fatalf("%d devices with more than one open pairing", n)
	}
	if n := count(`SELECT count(*) FROM vehicles v LEFT JOIN vehicle_pairings p ON p.vehicle_id = v.id AND p.ended_at IS NULL
	                WHERE v.paired_device_id IS DISTINCT FROM p.device_id`); n != 0 {
		rt.Fatalf("%d vehicles whose paired_device_id does not mirror the open pairing", n)
	}
	if n := count(`SELECT count(*) FROM vehicle_pairings WHERE ended_at < started_at`); n != 0 {
		rt.Fatalf("%d ended rows end before they start", n)
	}
}

// pairingSnapshot captures row counts and every mirror, so rejected ops can be
// shown to leave both unchanged.
func pairingSnapshot(rt *rapid.T, db *sql.DB) string {
	rt.Helper()
	var rows int
	var mirrors sql.NullString
	if err := db.QueryRow(`SELECT count(*) FROM vehicle_pairings`).Scan(&rows); err != nil {
		rt.Fatalf("snapshot rows: %v", err)
	}
	if err := db.QueryRow(`SELECT string_agg(id || ':' || coalesce(paired_device_id::text, '-'), ',' ORDER BY id) FROM vehicles`).Scan(&mirrors); err != nil {
		rt.Fatalf("snapshot mirrors: %v", err)
	}
	return fmt.Sprintf("rows=%d mirrors=%s", rows, mirrors.String)
}

// must unwraps a value or panics, which rapid reports as a failing check.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// scalar runs a query that returns one bigint.
func scalar(db *sql.DB, query string, args ...any) (int64, error) {
	var v int64
	err := db.QueryRow(query, args...).Scan(&v)
	return v, err
}
