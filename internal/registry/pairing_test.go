package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"fleetops/internal/auth"
)

// pairFixture adds one active vehicle, two devices, and one driver to fleet A.
type pairFixture struct {
	*fixture
	vehicle, vehicleB int64
	devX, devY        int64
	driver            int64
}

func newPairFixture(t *testing.T) *pairFixture {
	t.Helper()
	f := newFixture(t)
	ctx := context.Background()
	v, err := f.svc.CreateVehicle(ctx, f.actorA, VehicleInput{Plate: "VAN1"})
	mustOK(t, err, "vehicle")
	vb, err := f.svc.CreateVehicle(ctx, f.actorA, VehicleInput{Plate: "VAN2"})
	mustOK(t, err, "vehicle B")
	x, err := f.svc.IssueDevice(ctx, f.actorA, KindPWA)
	mustOK(t, err, "device X")
	y, err := f.svc.IssueDevice(ctx, f.actorA, KindTracker)
	mustOK(t, err, "device Y")
	d, err := f.svc.CreateDriver(ctx, f.actorA, DriverInput{Name: "Ana", LicenseNo: "LIC00001"})
	mustOK(t, err, "driver")
	return &pairFixture{fixture: f, vehicle: v.ID, vehicleB: vb.ID, devX: x.ID, devY: y.ID, driver: d.ID}
}

func i64(v int64) *int64 { return &v }

// auditCount returns the number of admin_audit rows for an action.
func (f *fixture) auditCount(t *testing.T, action string) int {
	t.Helper()
	var n int
	mustOK(t, f.db.QueryRow(`SELECT count(*) FROM admin_audit WHERE action = $1`, action).Scan(&n), "count audit")
	return n
}

func (f *pairFixture) mirror(t *testing.T, vehicleID int64) *int64 {
	t.Helper()
	var d sql.NullInt64
	mustOK(t, f.db.QueryRow(`SELECT paired_device_id FROM vehicles WHERE id = $1`, vehicleID).Scan(&d), "mirror")
	if !d.Valid {
		return nil
	}
	return &d.Int64
}

func TestPairingSwapKeepsHistory(t *testing.T) {
	f := newPairFixture(t)
	ctx := context.Background()

	// Open A -> X.
	first, out, err := f.svc.SetPairing(ctx, f.actorA, f.vehicle, PairingInput{DeviceID: i64(f.devX), DriverID: i64(f.driver)})
	mustOK(t, err, "pair X")
	if out != Created || first == nil || first.EndedAt != nil {
		t.Fatalf("pair X = %+v, %v", first, out)
	}
	if m := f.mirror(t, f.vehicle); m == nil || *m != f.devX {
		t.Fatalf("mirror after pair = %v, want %d", m, f.devX)
	}

	// Swap A -> Y.
	f.tick()
	_, out, err = f.svc.SetPairing(ctx, f.actorA, f.vehicle, PairingInput{DeviceID: i64(f.devY)})
	mustOK(t, err, "swap to Y")
	if out != Created {
		t.Fatalf("swap outcome = %v", out)
	}
	// Re-pair A -> X.
	f.tick()
	_, out, err = f.svc.SetPairing(ctx, f.actorA, f.vehicle, PairingInput{DeviceID: i64(f.devX)})
	mustOK(t, err, "re-pair X")
	if out != Created {
		t.Fatalf("re-pair outcome = %v", out)
	}

	history, err := f.svc.ListPairings(ctx, f.fleetA, f.vehicle)
	mustOK(t, err, "list pairings")
	if len(history) != 3 {
		t.Fatalf("history rows = %d, want 3", len(history))
	}
	// Newest first: row 3 (open, X), row 2 (ended, Y), row 1 (ended, X).
	if history[0].EndedAt != nil || history[0].DeviceID != f.devX {
		t.Fatalf("row 3 = %+v, want open X", history[0])
	}
	if history[1].EndedAt == nil || history[1].DeviceID != f.devY {
		t.Fatalf("row 2 = %+v, want ended Y", history[1])
	}
	if history[2].EndedAt == nil || history[2].DeviceID != f.devX {
		t.Fatalf("row 1 = %+v, want ended X", history[2])
	}
	if !history[2].StartedAt.Equal(first.StartedAt) {
		t.Fatalf("row 1 started_at changed: %v vs %v", history[2].StartedAt, first.StartedAt)
	}
	if m := f.mirror(t, f.vehicle); m == nil || *m != f.devX {
		t.Fatalf("mirror after re-pair = %v, want %d", m, f.devX)
	}

	// The same history comes back over GET /vehicles/:id/pairings, newest first.
	clock := func() time.Time { return *f.clock }
	iss, err := auth.NewIssuer(httpSecret, clock)
	mustOK(t, err, "issuer")
	tok, err := iss.Issue(f.actorA.UserID, f.fleetA, roleAdmin)
	mustOK(t, err, "issue token")
	e := echo.New()
	(&Handler{Svc: f.svc}).Register(e, auth.RequireAuth(iss))
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/vehicles/%d/pairings", f.vehicle), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	var rows []Pairing
	decode(t, rec.Body.Bytes(), &rows)
	if rec.Code != http.StatusOK || len(rows) != 3 {
		t.Fatalf("GET pairings = %d %s, want 200 with 3 rows", rec.Code, rec.Body)
	}
	for i := range rows {
		if rows[i].ID != history[i].ID {
			t.Fatalf("GET row %d id = %d, want %d (newest first)", i, rows[i].ID, history[i].ID)
		}
	}
}

func TestPairingPairCreatesOneRowAndMirrors(t *testing.T) {
	f := newPairFixture(t)
	ctx := context.Background()
	p, out, err := f.svc.SetPairing(ctx, f.actorA, f.vehicle, PairingInput{DeviceID: i64(f.devX), DriverID: i64(f.driver)})
	mustOK(t, err, "pair")
	if out != Created || p.DeviceID != f.devX || p.DriverID == nil || *p.DriverID != f.driver {
		t.Fatalf("pair = %+v, %v", p, out)
	}
	if n := countRows(t, f, f.vehicle); n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
	if m := f.mirror(t, f.vehicle); m == nil || *m != f.devX {
		t.Fatalf("mirror = %v", m)
	}
	if n := f.auditCount(t, "vehicle.pair"); n != 1 {
		t.Fatalf("vehicle.pair audit rows = %d, want 1", n)
	}
}

func countRows(t *testing.T, f *pairFixture, vehicleID int64) int {
	t.Helper()
	var n int
	mustOK(t, f.db.QueryRow(`SELECT count(*) FROM vehicle_pairings WHERE vehicle_id = $1`, vehicleID).Scan(&n), "count rows")
	return n
}

func TestPairingSameDeviceAndDriverIsNoOp(t *testing.T) {
	f := newPairFixture(t)
	ctx := context.Background()
	in := PairingInput{DeviceID: i64(f.devX), DriverID: i64(f.driver)}
	_, _, err := f.svc.SetPairing(ctx, f.actorA, f.vehicle, in)
	mustOK(t, err, "pair")
	before := f.auditCount(t, "vehicle.pair")

	f.tick()
	p, out, err := f.svc.SetPairing(ctx, f.actorA, f.vehicle, in)
	mustOK(t, err, "pair again")
	if out != NoOp || p == nil || p.EndedAt != nil {
		t.Fatalf("repeat pair = %+v, %v; want NoOp with the open row", p, out)
	}
	if n := countRows(t, f, f.vehicle); n != 1 {
		t.Fatalf("rows after no-op = %d, want 1", n)
	}
	if n := f.auditCount(t, "vehicle.pair"); n != before {
		t.Fatalf("no-op wrote an audit row")
	}
}

func TestPairingUnpair(t *testing.T) {
	f := newPairFixture(t)
	ctx := context.Background()
	_, _, err := f.svc.SetPairing(ctx, f.actorA, f.vehicle, PairingInput{DeviceID: i64(f.devX)})
	mustOK(t, err, "pair")

	f.tick()
	p, out, err := f.svc.SetPairing(ctx, f.actorA, f.vehicle, PairingInput{})
	mustOK(t, err, "unpair")
	if out != Created || p == nil || p.EndedAt == nil {
		t.Fatalf("unpair = %+v, %v; want the ended row", p, out)
	}
	if m := f.mirror(t, f.vehicle); m != nil {
		t.Fatalf("mirror after unpair = %d, want NULL", *m)
	}
	var open int
	mustOK(t, f.db.QueryRow(`SELECT count(*) FROM vehicle_pairings WHERE vehicle_id = $1 AND ended_at IS NULL`, f.vehicle).Scan(&open), "open rows")
	if open != 0 {
		t.Fatalf("open rows after unpair = %d", open)
	}
	if n := f.auditCount(t, "vehicle.unpair"); n != 1 {
		t.Fatalf("vehicle.unpair audit rows = %d, want 1", n)
	}

	f.tick()
	p, out, err = f.svc.SetPairing(ctx, f.actorA, f.vehicle, PairingInput{})
	mustOK(t, err, "unpair with nothing open")
	if out != NoOp || p != nil {
		t.Fatalf("unpair with nothing open = %+v, %v; want NoOp, nil", p, out)
	}
}

func TestPairingDeviceInUseElsewhere(t *testing.T) {
	f := newPairFixture(t)
	ctx := context.Background()
	_, _, err := f.svc.SetPairing(ctx, f.actorA, f.vehicleB, PairingInput{DeviceID: i64(f.devX)})
	mustOK(t, err, "pair X to B")
	_, _, err = f.svc.SetPairing(ctx, f.actorA, f.vehicle, PairingInput{DeviceID: i64(f.devX)})
	if !errors.Is(err, ErrDeviceInUse) {
		t.Fatalf("pair X to A while on B = %v, want ErrDeviceInUse", err)
	}
	if n := countRows(t, f, f.vehicle); n != 0 {
		t.Fatalf("rows written on refused pair = %d", n)
	}
	if m := f.mirror(t, f.vehicle); m != nil {
		t.Fatal("mirror changed on refused pair")
	}
}

func TestPairingRefusedForInactiveVehicle(t *testing.T) {
	f := newPairFixture(t)
	ctx := context.Background()
	for _, status := range []string{VehicleMaintenance, VehicleRetired} {
		_, err := f.svc.UpdateVehicle(ctx, f.actorA, f.vehicle, VehiclePatch{Status: &status})
		mustOK(t, err, "set status")
		_, _, err = f.svc.SetPairing(ctx, f.actorA, f.vehicle, PairingInput{DeviceID: i64(f.devX)})
		if !errors.Is(err, ErrVehicleNotActive) {
			t.Fatalf("pair on %s vehicle = %v, want ErrVehicleNotActive", status, err)
		}
	}
	if n := countRows(t, f, f.vehicle); n != 0 {
		t.Fatalf("rows written for inactive vehicle = %d", n)
	}
}

func TestPairingRefusedForRevokedDevice(t *testing.T) {
	f := newPairFixture(t)
	ctx := context.Background()
	_, err := f.svc.RevokeDevice(ctx, f.actorA, f.devX)
	mustOK(t, err, "revoke")
	_, _, err = f.svc.SetPairing(ctx, f.actorA, f.vehicle, PairingInput{DeviceID: i64(f.devX)})
	if !errors.Is(err, ErrDeviceRevoked) {
		t.Fatalf("pair revoked device = %v, want ErrDeviceRevoked", err)
	}
}

func TestPairingCrossFleetIsRefused(t *testing.T) {
	f := newPairFixture(t)
	ctx := context.Background()
	foreignDevice, err := f.svc.IssueDevice(ctx, f.actorB, KindPWA)
	mustOK(t, err, "fleet B device")
	foreignDriver, err := f.svc.CreateDriver(ctx, f.actorB, DriverInput{Name: "Bo", LicenseNo: "LIC00002"})
	mustOK(t, err, "fleet B driver")

	if _, _, err := f.svc.SetPairing(ctx, f.actorB, f.vehicle, PairingInput{DeviceID: i64(f.devX)}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("fleet B pairs fleet A vehicle = %v, want ErrNotFound", err)
	}
	if _, _, err := f.svc.SetPairing(ctx, f.actorA, f.vehicle, PairingInput{DeviceID: i64(foreignDevice.ID)}); !errors.Is(err, ErrValidation) {
		t.Fatalf("foreign device = %v, want ErrValidation", err)
	}
	if _, _, err := f.svc.SetPairing(ctx, f.actorA, f.vehicle, PairingInput{DeviceID: i64(f.devX), DriverID: i64(foreignDriver.ID)}); !errors.Is(err, ErrValidation) {
		t.Fatalf("foreign driver = %v, want ErrValidation", err)
	}
	if n := countRows(t, f, f.vehicle); n != 0 {
		t.Fatalf("rows written across fleets = %d", n)
	}
}

func TestPairingDriverWithoutDeviceIsValidation(t *testing.T) {
	f := newPairFixture(t)
	_, _, err := f.svc.SetPairing(context.Background(), f.actorA, f.vehicle, PairingInput{DriverID: i64(f.driver)})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("driver without device = %v, want ErrValidation", err)
	}
}

func TestPairingConcurrentSameDeviceOneWins(t *testing.T) {
	f := newPairFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, veh := range []int64{f.vehicle, f.vehicleB} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, errs[i] = f.svc.SetPairing(ctx, f.actorA, veh, PairingInput{DeviceID: i64(f.devX)})
		}()
	}
	wg.Wait()
	wins, inUse := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ErrDeviceInUse):
			inUse++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if wins != 1 || inUse != 1 {
		t.Fatalf("wins=%d inUse=%d, want 1 and 1", wins, inUse)
	}
}
