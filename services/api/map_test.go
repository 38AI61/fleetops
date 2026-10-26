package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"testing"
	"time"

	"fleetops/internal/registry"
)

// mapBox covers the whole fixture area: every fixture point sits inside it.
const mapBox = "/map/vehicles?bbox=13.30,52.45,13.50,52.60"

// mapFixtureVehicle inserts a vehicle with a latest position. serverTS is
// the vehicles_latest.server_ts; speed is nullable.
func mapFixtureVehicle(t *testing.T, db *sql.DB, fleet int64, plate string, lng, lat float64, serverTS time.Time, speed *float64) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(
		`INSERT INTO vehicles (fleet_id, plate) VALUES ($1, $2) RETURNING id`, fleet, plate,
	).Scan(&id); err != nil {
		t.Fatalf("insert vehicle %s: %v", plate, err)
	}
	if _, err := db.Exec(`
		INSERT INTO vehicles_latest (vehicle_id, fleet_id, geom, speed_kmh, heading_deg, device_ts, server_ts)
		VALUES ($1, $2, ST_SetSRID(ST_MakePoint($3, $4), 4326)::geography, $5, 271.5, $6, $6)`,
		id, fleet, lng, lat, speed, serverTS); err != nil {
		t.Fatalf("insert vehicles_latest %s: %v", plate, err)
	}
	return id
}

// mapCallerToken logs in a user of fleet with the given role and returns the token.
func mapCallerToken(t *testing.T, h *harness, fleet int64, role string) string {
	t.Helper()
	email := fmt.Sprintf("%s-%d@map.example", role, fleet)
	h.user(t, fleet, email, role)
	return h.login(t, email)
}

// TestMapVehiclesAuth: no credential and a device key without a JWT both get 401 (GF-126, GF-47, GF-52).
// Each JWT role gets 200.
func TestMapVehiclesAuth(t *testing.T) {
	h := startServer(t)
	fleet := h.fleet(t, "auth")

	if r := h.do(t, http.MethodGet, mapBox, "", "", nil); r.status != http.StatusUnauthorized {
		t.Fatalf("no token = %d, want 401", r.status)
	}

	svc := &registry.Service{DB: h.db, Salt: testSalt, Clock: h.clock.now}
	adminID := h.user(t, fleet, "authadmin@map.example", "admin")
	admin := registry.Actor{UserID: adminID, FleetID: fleet}
	dev, err := svc.IssueDevice(context.Background(), admin, registry.KindTracker)
	if err != nil {
		t.Fatalf("issue device: %v", err)
	}
	r := h.doWithHeaders(t, http.MethodGet, mapBox, map[string]string{"X-Device-Key": dev.APIKey})
	if r.status != http.StatusUnauthorized {
		t.Fatalf("device key without JWT = %d, want 401", r.status)
	}

	for _, role := range []string{"admin", "manager", "dispatcher", "driver"} {
		tok := mapCallerToken(t, h, fleet, role)
		if r := h.do(t, http.MethodGet, mapBox, tok, "", nil); r.status != http.StatusOK {
			t.Fatalf("role %s = %d (%s), want 200", role, r.status, r.body)
		}
	}
}

// TestMapVehiclesResponseShape: field names, types, RFC3339 UTC ms, null speed, and an empty array (GF-130).
func TestMapVehiclesResponseShape(t *testing.T) {
	h := startServer(t)
	fleet := h.fleet(t, "shape")
	tok := mapCallerToken(t, h, fleet, "dispatcher")
	now := h.clock.now()
	speed := 42.0
	mapFixtureVehicle(t, h.db, fleet, "SHP-1", 13.40, 52.52, now.Add(-time.Minute), &speed)
	mapFixtureVehicle(t, h.db, fleet, "SHP-2", 13.41, 52.52, now.Add(-time.Minute), nil)

	r := h.do(t, http.MethodGet, mapBox, tok, "", nil)
	if r.status != http.StatusOK {
		t.Fatalf("status = %d (%s)", r.status, r.body)
	}
	var body struct {
		Vehicles  []map[string]any `json:"vehicles"`
		Truncated *bool            `json:"truncated"`
	}
	if err := json.Unmarshal(r.body, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Truncated == nil || *body.Truncated {
		t.Fatalf("truncated = %v, want false", body.Truncated)
	}
	if len(body.Vehicles) != 2 {
		t.Fatalf("vehicles = %d, want 2", len(body.Vehicles))
	}
	stamp := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)
	wantKeys := []string{"vehicle_id", "lat", "lng", "speed_kmh", "heading_deg", "status", "device_ts", "server_ts"}
	for _, v := range body.Vehicles {
		if len(v) != len(wantKeys) {
			t.Fatalf("vehicle keys = %v, want exactly %v", keysOf(v), wantKeys)
		}
		for _, k := range wantKeys {
			if _, ok := v[k]; !ok {
				t.Fatalf("missing key %s in %v", k, v)
			}
		}
		if _, ok := v["fleet_id"]; ok {
			t.Fatalf("fleet_id leaked into body: %v", v)
		}
		if s, _ := v["device_ts"].(string); !stamp.MatchString(s) {
			t.Fatalf("device_ts %q is not RFC3339 UTC ms", s)
		}
		if s, _ := v["status"].(string); s != "idle" {
			t.Fatalf("status = %v, want idle", v["status"])
		}
	}
	if body.Vehicles[0]["speed_kmh"] != 42.0 || body.Vehicles[1]["speed_kmh"] != nil {
		t.Fatalf("speed_kmh = %v / %v, want 42 and null", body.Vehicles[0]["speed_kmh"], body.Vehicles[1]["speed_kmh"])
	}

	// No vehicles: the array is present and empty, never null (GF-130).
	r = h.do(t, http.MethodGet, "/map/vehicles?bbox=0,0,1,1", tok, "", nil)
	if r.status != http.StatusOK || !jsonHasEmptyArray(r.body, "vehicles") {
		t.Fatalf("empty result = %d %s", r.status, r.body)
	}
}

// TestMapVehiclesTruncation: 1001 rows answer with 1000 and truncated=true (GF-129).
func TestMapVehiclesTruncation(t *testing.T) {
	h := startServer(t)
	fleet := h.fleet(t, "trunc")
	tok := mapCallerToken(t, h, fleet, "dispatcher")
	_, err := h.db.Exec(`
		INSERT INTO vehicles (fleet_id, plate)
		SELECT $1, 'T' || lpad(g::text, 5, '0') FROM generate_series(1, 1001) g`, fleet)
	if err != nil {
		t.Fatalf("insert vehicles: %v", err)
	}
	_, err = h.db.Exec(`
		INSERT INTO vehicles_latest (vehicle_id, fleet_id, geom, device_ts, server_ts)
		SELECT v.id, v.fleet_id, ST_SetSRID(ST_MakePoint(13.40, 52.52), 4326)::geography, $2, $2
		FROM vehicles v WHERE v.fleet_id = $1`, fleet, h.clock.now())
	if err != nil {
		t.Fatalf("insert vehicles_latest: %v", err)
	}

	r := h.do(t, http.MethodGet, mapBox, tok, "", nil)
	if r.status != http.StatusOK {
		t.Fatalf("status = %d (%s)", r.status, r.body)
	}
	var body struct {
		Vehicles  []json.RawMessage `json:"vehicles"`
		Truncated bool              `json:"truncated"`
	}
	if err := json.Unmarshal(r.body, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Vehicles) != 1000 || !body.Truncated {
		t.Fatalf("got %d vehicles truncated=%v, want 1000 and true", len(body.Vehicles), body.Truncated)
	}
}

// TestMapVehiclesReturnsOnlyInBboxOfCallerFleet is the roadmap Validation item:
// in-box caller-fleet vehicles only. An out-of-box caller vehicle and an in-box
// other-fleet vehicle must not appear (GF-128).
func TestMapVehiclesReturnsOnlyInBboxOfCallerFleet(t *testing.T) {
	h := startServer(t)
	caller := h.fleet(t, "caller")
	other := h.fleet(t, "other")
	tok := mapCallerToken(t, h, caller, "dispatcher")
	now := h.clock.now()

	inA := mapFixtureVehicle(t, h.db, caller, "CAL-IN-A", 13.40, 52.52, now, nil)
	inB := mapFixtureVehicle(t, h.db, caller, "CAL-IN-B", 13.3020, 52.50, now, nil)
	mapFixtureVehicle(t, h.db, caller, "CAL-OUT", 13.60, 52.52, now, nil)
	mapFixtureVehicle(t, h.db, other, "OTH-IN", 13.41, 52.52, now, nil)

	r := h.do(t, http.MethodGet, mapBox, tok, "", nil)
	if r.status != http.StatusOK {
		t.Fatalf("status = %d (%s)", r.status, r.body)
	}
	var body struct {
		Vehicles []struct {
			VehicleID int64 `json:"vehicle_id"`
		} `json:"vehicles"`
	}
	if err := json.Unmarshal(r.body, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := map[int64]bool{}
	for _, v := range body.Vehicles {
		got[v.VehicleID] = true
	}
	if len(got) != 2 || !got[inA] || !got[inB] {
		t.Fatalf("vehicle ids = %v, want exactly {%d, %d}", got, inA, inB)
	}
}

// doWithHeaders sends a bodiless request with explicit headers and no bearer.
// It exists so the device-key cases can send X-Device-Key alone.
func (h *harness) doWithHeaders(t *testing.T, method, path string, headers map[string]string) reply {
	t.Helper()
	req, err := http.NewRequest(method, h.url+path, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return reply{status: resp.StatusCode, body: b}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// jsonHasEmptyArray reports whether body[key] is a present, empty JSON array.
func jsonHasEmptyArray(body []byte, key string) bool {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return false
	}
	return string(m[key]) == "[]"
}
