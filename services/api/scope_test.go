package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"fleetops/internal/auth"
	"fleetops/internal/registry"
)

// TestFleetScoping is the fleet-isolation check: a fleet A admin cannot see or
// change fleet B users, and a token with a forged fleet_id is refused.
func TestFleetScoping(t *testing.T) {
	h := startServer(t)
	fleetA := h.fleet(t, "A")
	fleetB := h.fleet(t, "B")
	adminA := h.user(t, fleetA, "admin@a.example", "admin")
	userB := h.user(t, fleetB, "user@b.example", "dispatcher")
	tokA := h.login(t, "admin@a.example")
	userBPath := fmt.Sprintf("/users/%d", userB)

	t.Run("GET /users excludes fleet B", func(t *testing.T) {
		r := h.do(t, http.MethodGet, "/users", tokA, "", nil)
		if r.status != http.StatusOK {
			t.Fatalf("status = %d, want 200", r.status)
		}
		if strings.Contains(string(r.body), "user@b.example") {
			t.Fatalf("fleet A listing leaks fleet B user: %s", r.body)
		}
		if !strings.Contains(string(r.body), "admin@a.example") {
			t.Fatalf("fleet A listing missing its own admin: %s", r.body)
		}
	})

	t.Run("PATCH fleet B user is 404 and writes nothing", func(t *testing.T) {
		var before int
		if err := h.db.QueryRow(`SELECT count(*) FROM admin_audit`).Scan(&before); err != nil {
			t.Fatalf("count audit: %v", err)
		}
		r := h.do(t, http.MethodPatch, userBPath, tokA, `{"role":"driver"}`, nil)
		if r.status != http.StatusNotFound || r.errCode(t) != "not_found" {
			t.Fatalf("status = %d %s, want 404 not_found", r.status, r.body)
		}
		var role string
		if err := h.db.QueryRow(`SELECT role FROM users WHERE id = $1`, userB).Scan(&role); err != nil {
			t.Fatalf("read role: %v", err)
		}
		if role != "dispatcher" {
			t.Fatalf("fleet B role changed to %q through fleet A", role)
		}
		var after int
		if err := h.db.QueryRow(`SELECT count(*) FROM admin_audit`).Scan(&after); err != nil {
			t.Fatalf("count audit: %v", err)
		}
		if after != before {
			t.Fatalf("cross-fleet attempt wrote %d audit rows", after-before)
		}
	})

	t.Run("token signed with another key is 401", func(t *testing.T) {
		other, err := auth.NewIssuer([]byte(strings.Repeat("x", 32)), h.clock.now)
		if err != nil {
			t.Fatalf("NewIssuer: %v", err)
		}
		forged, err := other.Issue(adminA, fleetB, "admin")
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}
		r := h.do(t, http.MethodPatch, userBPath, forged, `{"role":"driver"}`, nil)
		if r.status != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", r.status)
		}
	})

	t.Run("edited fleet_id on a real token is 401", func(t *testing.T) {
		forged := retargetFleet(t, tokA, fleetB)
		r := h.do(t, http.MethodPatch, userBPath, forged, `{"role":"driver"}`, nil)
		if r.status != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", r.status)
		}
		if r := h.do(t, http.MethodGet, "/users", forged, "", nil); r.status != http.StatusUnauthorized {
			t.Fatalf("GET with forged fleet_id = %d, want 401", r.status)
		}
	})
}

// TestFleetScopeRegistry is the P3 fleet-isolation check (spec 03 group 12).
// Fleet B's rows must be invisible, unchanged, and unreachable from fleet A's
// admin, and a driver sees only its own row.
func TestFleetScopeRegistry(t *testing.T) {
	h := startServer(t)
	fleetA := h.fleet(t, "A")
	fleetB := h.fleet(t, "B")
	adminAID := h.user(t, fleetA, "admin@a.example", "admin")
	adminBID := h.user(t, fleetB, "admin@b.example", "admin")
	driverAUser := h.user(t, fleetA, "driver@a.example", "driver")
	tokA := h.login(t, "admin@a.example")
	tokDriverA := h.login(t, "driver@a.example")

	svc := &registry.Service{DB: h.db, Salt: testSalt, Clock: h.clock.now}
	ctx := context.Background()
	actorA := registry.Actor{UserID: adminAID, FleetID: fleetA}
	actorB := registry.Actor{UserID: adminBID, FleetID: fleetB}
	vehicleA, err := svc.CreateVehicle(ctx, actorA, registry.VehicleInput{Plate: "FSA0001"})
	if err != nil {
		t.Fatalf("fleet A vehicle: %v", err)
	}
	vehicleB, err := svc.CreateVehicle(ctx, actorB, registry.VehicleInput{Plate: "FSB0001"})
	if err != nil {
		t.Fatalf("fleet B vehicle: %v", err)
	}
	driverA, err := svc.CreateDriver(ctx, actorA, registry.DriverInput{Name: "Ann", LicenseNo: "ANN00001"})
	if err != nil {
		t.Fatalf("fleet A driver: %v", err)
	}
	driverB, err := svc.CreateDriver(ctx, actorB, registry.DriverInput{Name: "Bea", LicenseNo: "BEA00001"})
	if err != nil {
		t.Fatalf("fleet B driver: %v", err)
	}
	deviceA, err := svc.IssueDevice(ctx, actorA, registry.KindPWA)
	if err != nil {
		t.Fatalf("fleet A device: %v", err)
	}
	deviceB, err := svc.IssueDevice(ctx, actorB, registry.KindPWA)
	if err != nil {
		t.Fatalf("fleet B device: %v", err)
	}
	if _, err := h.db.Exec(`UPDATE users SET driver_id = $1 WHERE id = $2`, driverA.ID, driverAUser); err != nil {
		t.Fatalf("link driver A: %v", err)
	}

	t.Run("GET /vehicles excludes fleet B", func(t *testing.T) {
		r := h.do(t, http.MethodGet, "/vehicles", tokA, "", nil)
		if r.status != http.StatusOK || strings.Contains(string(r.body), "FSB0001") || !strings.Contains(string(r.body), "FSA0001") {
			t.Fatalf("GET /vehicles = %d %s", r.status, r.body)
		}
	})
	t.Run("cross-fleet reads and writes are 404 and change nothing", func(t *testing.T) {
		before := deviceHash(t, h, deviceB.ID)
		vehiclePath := fmt.Sprintf("/vehicles/%d", vehicleB.ID)
		driverPath := fmt.Sprintf("/drivers/%d", driverB.ID)
		if r := h.do(t, http.MethodGet, vehiclePath, tokA, "", nil); r.status != http.StatusNotFound || r.errCode(t) != "not_found" {
			t.Fatalf("GET vehicle B = %d %s", r.status, r.body)
		}
		if r := h.do(t, http.MethodPatch, driverPath, tokA, `{"phone":"+90 000"}`, nil); r.status != http.StatusNotFound {
			t.Fatalf("PATCH driver B = %d %s", r.status, r.body)
		}
		if r := h.do(t, http.MethodPost, fmt.Sprintf("/devices/%d/rotate", deviceB.ID), tokA, "", nil); r.status != http.StatusNotFound {
			t.Fatalf("rotate device B = %d %s", r.status, r.body)
		}
		if after := deviceHash(t, h, deviceB.ID); after != before {
			t.Fatalf("cross-fleet rotate changed fleet B's key hash")
		}
	})
	t.Run("pairing with another fleet's device or driver is 400", func(t *testing.T) {
		path := fmt.Sprintf("/vehicles/%d/pairings", vehicleA.ID)
		if r := h.do(t, http.MethodPost, path, tokA, fmt.Sprintf(`{"device_id":%d,"driver_id":null}`, deviceB.ID), nil); r.status != http.StatusBadRequest || r.errCode(t) != "validation_failed" {
			t.Fatalf("device B = %d %s, want 400", r.status, r.body)
		}
		body := fmt.Sprintf(`{"device_id":%d,"driver_id":%d}`, deviceA.ID, driverB.ID)
		if r := h.do(t, http.MethodPost, path, tokA, body, nil); r.status != http.StatusBadRequest {
			t.Fatalf("driver B = %d %s, want 400", r.status, r.body)
		}
		var open int
		if err := h.db.QueryRow(`SELECT count(*) FROM vehicle_pairings`).Scan(&open); err != nil || open != 0 {
			t.Fatalf("refused pairings wrote %d rows (%v)", open, err)
		}
	})
	t.Run("forged fleet_id on a real token is 401", func(t *testing.T) {
		forged := retargetFleet(t, tokA, fleetB)
		if r := h.do(t, http.MethodGet, "/vehicles", forged, "", nil); r.status != http.StatusUnauthorized {
			t.Fatalf("GET /vehicles with forged fleet_id = %d, want 401", r.status)
		}
		if r := h.do(t, http.MethodGet, fmt.Sprintf("/drivers/%d", driverB.ID), forged, "", nil); r.status != http.StatusUnauthorized {
			t.Fatalf("GET /drivers with forged fleet_id = %d, want 401", r.status)
		}
	})
	t.Run("driver sees only its own row", func(t *testing.T) {
		r := h.do(t, http.MethodGet, "/drivers", tokDriverA, "", nil)
		if r.status != http.StatusOK || strings.Contains(string(r.body), "Bea") || !strings.Contains(string(r.body), "Ann") {
			t.Fatalf("driver A GET /drivers = %d %s", r.status, r.body)
		}
		if r := h.do(t, http.MethodGet, fmt.Sprintf("/drivers/%d", driverB.ID), tokDriverA, "", nil); r.status != http.StatusNotFound {
			t.Fatalf("driver A GET driver B = %d, want 404", r.status)
		}
	})
}

// deviceHash reads one device's stored key hash, for before-and-after checks.
func deviceHash(t *testing.T, h *harness, id int64) string {
	t.Helper()
	var hash []byte
	if err := h.db.QueryRow(`SELECT api_key_hash FROM devices WHERE id = $1`, id).Scan(&hash); err != nil {
		t.Fatalf("read device hash: %v", err)
	}
	return string(hash)
}

// retargetFleet rewrites fleet_id in a token's payload and keeps the old
// signature, which must then fail verification.
func retargetFleet(t *testing.T, tok string, fleetID int64) string {
	t.Helper()
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d parts", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	claims["fleet_id"] = fleetID
	edited, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	parts[1] = base64.RawURLEncoding.EncodeToString(edited)
	return strings.Join(parts, ".")
}
