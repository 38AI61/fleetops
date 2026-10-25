package registry

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"fleetops/internal/auth"
	"fleetops/internal/devicekey"
	"fleetops/internal/testinfra"
)

// httpSecret is 32 bytes, the minimum (D-03). It is never a real secret.
var httpSecret = []byte(strings.Repeat("h", 32))

// keyPattern is the GF-31 key format. Only keys matching it count as issued.
var keyPattern = regexp.MustCompile(`fo_dev_[1-9][0-9]*_[A-Za-z0-9_-]{43}`)

// httpEnv is the registry routes mounted by Handler.Register on a loopback
// server, backed by a migrated PostGIS database. Time is fixed and advanced by
// the test (GF-45).
type httpEnv struct {
	url string
	db  *sql.DB
	svc *Service
	iss *auth.Issuer
	now time.Time
}

func newHTTPEnv(t *testing.T) *httpEnv {
	t.Helper()
	db := testinfra.NewMigratedDB(t)
	env := &httpEnv{db: db, now: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}
	iss, err := auth.NewIssuer(httpSecret, func() time.Time { return env.now })
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	env.iss = iss
	env.svc = &Service{DB: db, Salt: storeSalt, Clock: func() time.Time { return env.now }}

	e := echo.New()
	e.HideBanner = true
	(&Handler{Svc: env.svc}).Register(e, auth.RequireAuth(iss))
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	env.url = srv.URL
	return env
}

// user inserts a user with the given role in the fleet and returns its id.
func (e *httpEnv) user(t *testing.T, fleetID int64, role string) int64 {
	t.Helper()
	var id int64
	email := fmt.Sprintf("%s-%d@http.example", role, time.Now().UnixNano())
	if err := e.db.QueryRow(
		`INSERT INTO users (fleet_id, email, password_hash, role) VALUES ($1, $2, 'x', $3) RETURNING id`,
		fleetID, email, role).Scan(&id); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

// token issues an access token for the user, as the login handler would.
func (e *httpEnv) token(t *testing.T, userID, fleetID int64, role string) string {
	t.Helper()
	tok, err := e.iss.Issue(userID, fleetID, role)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return tok
}

// caller is an admin, manager, dispatcher, or driver in fleetID, with its token.
type caller struct {
	id, fleet int64
	role, tok string
}

func (e *httpEnv) as(t *testing.T, fleetID int64, role string) caller {
	t.Helper()
	id := e.user(t, fleetID, role)
	return caller{id: id, fleet: fleetID, role: role, tok: e.token(t, id, fleetID, role)}
}

type reply struct {
	status int
	body   []byte
}

func (r reply) errCode(t *testing.T) string {
	t.Helper()
	var m map[string]any
	_ = json.Unmarshal(r.body, &m)
	s, _ := m["error"].(string)
	return s
}

// do sends one request over real HTTP. An empty bearer sends no Authorization header.
func (e *httpEnv) do(t *testing.T, method, path, bearer, body string) reply {
	t.Helper()
	req, err := http.NewRequest(method, e.url+path, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return reply{status: resp.StatusCode, body: b}
}

// send is do with the caller's token.
func (e *httpEnv) send(t *testing.T, c caller, method, path, body string) reply {
	t.Helper()
	return e.do(t, method, path, c.tok, body)
}

func (e *httpEnv) vehicle(t *testing.T, fleetID int64, plate string) int64 {
	t.Helper()
	v, err := e.svc.CreateVehicle(context.Background(), Actor{UserID: e.user(t, fleetID, roleAdmin), FleetID: fleetID},
		VehicleInput{Plate: plate})
	mustOK(t, err, "create vehicle")
	return v.ID
}

func (e *httpEnv) driver(t *testing.T, fleetID int64, name, license string) int64 {
	t.Helper()
	d, err := e.svc.CreateDriver(context.Background(), Actor{UserID: e.user(t, fleetID, roleAdmin), FleetID: fleetID},
		DriverInput{Name: name, LicenseNo: license})
	mustOK(t, err, "create driver")
	return d.ID
}

// decode unmarshals a JSON body into v and fails the test on error.
func decode(t *testing.T, b []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
}

func TestVehicleRoutes(t *testing.T) {
	e := newHTTPEnv(t)
	fleet := insertFleet(t, e.db, "A")
	admin := e.as(t, fleet, roleAdmin)

	r := e.send(t, admin, http.MethodPost, "/vehicles", `{"plate":" ab 12-cd ","model":"Transit"}`)
	if r.status != http.StatusCreated {
		t.Fatalf("POST /vehicles = %d %s", r.status, r.body)
	}
	var v Vehicle
	decode(t, r.body, &v)
	if v.Plate != "AB 12-CD" || v.Status != VehicleActive || v.Model == nil || *v.Model != "Transit" || v.FleetID != fleet {
		t.Fatalf("created vehicle = %+v", v)
	}

	if r := e.send(t, admin, http.MethodGet, "/vehicles", ""); r.status != http.StatusOK || !strings.Contains(string(r.body), `"AB 12-CD"`) {
		t.Fatalf("GET /vehicles = %d %s", r.status, r.body)
	}
	if r := e.send(t, admin, http.MethodGet, fmt.Sprintf("/vehicles/%d", v.ID), ""); r.status != http.StatusOK {
		t.Fatalf("GET /vehicles/:id = %d %s", r.status, r.body)
	}
	r = e.send(t, admin, http.MethodPatch, fmt.Sprintf("/vehicles/%d", v.ID), `{"status":"maintenance"}`)
	if r.status != http.StatusOK {
		t.Fatalf("PATCH /vehicles/:id = %d %s", r.status, r.body)
	}
	decode(t, r.body, &v)
	if v.Status != VehicleMaintenance {
		t.Fatalf("status after PATCH = %q", v.Status)
	}

	bad := map[string]struct{ method, path, body string }{
		"bad plate":       {http.MethodPost, "/vehicles", `{"plate":"x"}`},
		"bad status":      {http.MethodPost, "/vehicles", `{"plate":"AB12","status":"broken"}`},
		"unknown field":   {http.MethodPost, "/vehicles", `{"plate":"AB12","fleet_id":9}`},
		"malformed json":  {http.MethodPost, "/vehicles", `{"plate":`},
		"empty patch":     {http.MethodPatch, fmt.Sprintf("/vehicles/%d", v.ID), `{}`},
		"patch bad state": {http.MethodPatch, fmt.Sprintf("/vehicles/%d", v.ID), `{"status":"broken"}`},
	}
	for name, tc := range bad {
		t.Run(name, func(t *testing.T) {
			r := e.send(t, admin, tc.method, tc.path, tc.body)
			if r.status != http.StatusBadRequest || r.errCode(t) != "validation_failed" {
				t.Fatalf("= %d %s, want 400 validation_failed", r.status, r.body)
			}
		})
	}

	other := e.as(t, insertFleet(t, e.db, "B"), roleAdmin)
	foreign := fmt.Sprintf("/vehicles/%d", v.ID)
	t.Run("cross-fleet GET and PATCH are 404", func(t *testing.T) {
		if r := e.send(t, other, http.MethodGet, foreign, ""); r.status != http.StatusNotFound || r.errCode(t) != "not_found" {
			t.Fatalf("GET = %d %s", r.status, r.body)
		}
		if r := e.send(t, other, http.MethodPatch, foreign, `{"model":"X"}`); r.status != http.StatusNotFound {
			t.Fatalf("PATCH = %d %s", r.status, r.body)
		}
	})
	t.Run("duplicate plate in one fleet is 409", func(t *testing.T) {
		if r := e.send(t, admin, http.MethodPost, "/vehicles", `{"plate":"AB12CD"}`); r.status != http.StatusCreated {
			t.Fatalf("first POST = %d %s", r.status, r.body)
		}
		if r := e.send(t, admin, http.MethodPost, "/vehicles", `{"plate":"ab12cd"}`); r.status != http.StatusConflict || r.errCode(t) != "conflict" {
			t.Fatalf("duplicate POST = %d %s", r.status, r.body)
		}
	})
}

func TestDriverRoutes(t *testing.T) {
	e := newHTTPEnv(t)
	fleet := insertFleet(t, e.db, "A")
	admin := e.as(t, fleet, roleAdmin)

	r := e.send(t, admin, http.MethodPost, "/drivers",
		`{"name":"Ada","license_no":"ab-12 cd34","phone":"+90 555","shift_start":"08:00","shift_end":"16:30"}`)
	if r.status != http.StatusCreated {
		t.Fatalf("POST /drivers = %d %s", r.status, r.body)
	}
	for _, leak := range []string{"license", "AB12CD34", "license_no_hash"} {
		if strings.Contains(string(r.body), leak) {
			t.Fatalf("driver response leaks %q: %s", leak, r.body)
		}
	}
	var d Driver
	decode(t, r.body, &d)
	if d.Name != "Ada" || d.ShiftStart == nil || *d.ShiftStart != "08:00" {
		t.Fatalf("created driver = %+v", d)
	}

	if r := e.send(t, admin, http.MethodGet, "/drivers", ""); r.status != http.StatusOK {
		t.Fatalf("GET /drivers = %d %s", r.status, r.body)
	}
	if r := e.send(t, admin, http.MethodGet, fmt.Sprintf("/drivers/%d", d.ID), ""); r.status != http.StatusOK {
		t.Fatalf("GET /drivers/:id = %d %s", r.status, r.body)
	}
	r = e.send(t, admin, http.MethodPatch, fmt.Sprintf("/drivers/%d", d.ID), `{"name":"Ada L","license_no":"ZZ990011"}`)
	if r.status != http.StatusOK {
		t.Fatalf("PATCH /drivers/:id = %d %s", r.status, r.body)
	}

	bad := map[string]string{
		"bad license":     `{"name":"X","license_no":"ab"}`,
		"bad shift":       `{"name":"X","license_no":"ABCD1234","shift_start":"25:00"}`,
		"unknown field":   `{"name":"X","license_no":"ABCD1234","role":"admin"}`,
		"missing license": `{"name":"X"}`,
	}
	for name, body := range bad {
		t.Run(name, func(t *testing.T) {
			r := e.send(t, admin, http.MethodPost, "/drivers", body)
			if r.status != http.StatusBadRequest || r.errCode(t) != "validation_failed" {
				t.Fatalf("= %d %s, want 400 validation_failed", r.status, r.body)
			}
		})
	}

	other := e.as(t, insertFleet(t, e.db, "B"), roleAdmin)
	t.Run("cross-fleet driver is 404", func(t *testing.T) {
		if r := e.send(t, other, http.MethodGet, fmt.Sprintf("/drivers/%d", d.ID), ""); r.status != http.StatusNotFound {
			t.Fatalf("GET = %d %s", r.status, r.body)
		}
		if r := e.send(t, other, http.MethodPatch, fmt.Sprintf("/drivers/%d", d.ID), `{"name":"Y"}`); r.status != http.StatusNotFound {
			t.Fatalf("PATCH = %d %s", r.status, r.body)
		}
	})
}

// TestDriverScopeAndRoleOrder covers GF-49 and GF-28: a linked driver sees only
// its own row, and a role refusal wins over a malformed body.
func TestDriverScopeAndRoleOrder(t *testing.T) {
	e := newHTTPEnv(t)
	fleet := insertFleet(t, e.db, "A")
	ownDriver := e.driver(t, fleet, "Own", "OWN12345")
	otherDriver := e.driver(t, fleet, "Other", "OTHER123")

	linked := e.user(t, fleet, roleDriver)
	if _, err := e.db.Exec(`UPDATE users SET driver_id = $1 WHERE id = $2`, ownDriver, linked); err != nil {
		t.Fatalf("link driver: %v", err)
	}
	driver := caller{id: linked, fleet: fleet, role: roleDriver, tok: e.token(t, linked, fleet, roleDriver)}

	t.Run("GET /drivers returns only the linked row", func(t *testing.T) {
		r := e.send(t, driver, http.MethodGet, "/drivers", "")
		var list []Driver
		decode(t, r.body, &list)
		if r.status != http.StatusOK || len(list) != 1 || list[0].ID != ownDriver {
			t.Fatalf("= %d %s, want one row %d", r.status, r.body, ownDriver)
		}
	})
	t.Run("GET own row is 200, another driver is 404", func(t *testing.T) {
		if r := e.send(t, driver, http.MethodGet, fmt.Sprintf("/drivers/%d", ownDriver), ""); r.status != http.StatusOK {
			t.Fatalf("own = %d %s", r.status, r.body)
		}
		if r := e.send(t, driver, http.MethodGet, fmt.Sprintf("/drivers/%d", otherDriver), ""); r.status != http.StatusNotFound {
			t.Fatalf("other = %d %s, want 404", r.status, r.body)
		}
	})
	t.Run("unlinked driver sees an empty list", func(t *testing.T) {
		unlinked := e.as(t, fleet, roleDriver)
		if r := e.send(t, unlinked, http.MethodGet, "/drivers", ""); r.status != http.StatusOK || strings.TrimSpace(string(r.body)) != "[]" {
			t.Fatalf("= %d %s, want []", r.status, r.body)
		}
		if r := e.send(t, unlinked, http.MethodGet, fmt.Sprintf("/drivers/%d", ownDriver), ""); r.status != http.StatusNotFound {
			t.Fatalf("GET by id = %d, want 404", r.status)
		}
	})
	t.Run("driver is 403 on every vehicle route", func(t *testing.T) {
		for _, route := range []struct{ method, path string }{
			{http.MethodGet, "/vehicles"},
			{http.MethodGet, "/vehicles/1"},
			{http.MethodPost, "/vehicles"},
			{http.MethodPatch, "/vehicles/1"},
			{http.MethodGet, "/vehicles/1/pairings"},
			{http.MethodPost, "/vehicles/1/pairings"},
		} {
			if r := e.send(t, driver, route.method, route.path, ""); r.status != http.StatusForbidden {
				t.Fatalf("%s %s = %d, want 403", route.method, route.path, r.status)
			}
		}
	})
	t.Run("driver POST with a malformed body is 403, not 400", func(t *testing.T) {
		if r := e.send(t, driver, http.MethodPost, "/drivers", `{"name":`); r.status != http.StatusForbidden {
			t.Fatalf("= %d %s, want 403", r.status, r.body)
		}
		if r := e.send(t, driver, http.MethodPatch, fmt.Sprintf("/drivers/%d", ownDriver), `{"name":`); r.status != http.StatusForbidden {
			t.Fatalf("PATCH = %d %s, want 403", r.status, r.body)
		}
	})
}

func TestPairingRoutes(t *testing.T) {
	e := newHTTPEnv(t)
	fleet := insertFleet(t, e.db, "A")
	admin := e.as(t, fleet, roleAdmin)
	vid := e.vehicle(t, fleet, "PR1")
	vehiclePath := fmt.Sprintf("/vehicles/%d/pairings", vid)
	devA := e.issue(t, fleet, KindPWA)
	devB := e.issue(t, fleet, KindTracker)
	drv := e.driver(t, fleet, "Pat", "PAT00001")

	pair := func(device, driver string) reply {
		return e.send(t, admin, http.MethodPost, vehiclePath, fmt.Sprintf(`{"device_id":%s,"driver_id":%s}`, device, driver))
	}

	r := pair(fmt.Sprint(devA), "null")
	var first Pairing
	decode(t, r.body, &first)
	if r.status != http.StatusCreated || first.DeviceID != devA || first.EndedAt != nil {
		t.Fatalf("pair = %d %s, want 201 open row", r.status, r.body)
	}
	if r := pair(fmt.Sprint(devA), "null"); r.status != http.StatusOK || !strings.Contains(string(r.body), fmt.Sprintf(`"id":%d`, first.ID)) {
		t.Fatalf("same device again = %d %s, want 200 with the open row", r.status, r.body)
	}
	if r := pair(fmt.Sprint(devB), fmt.Sprint(drv)); r.status != http.StatusCreated {
		t.Fatalf("swap = %d %s, want 201", r.status, r.body)
	}
	r = e.send(t, admin, http.MethodPost, vehiclePath, `{"device_id":null,"driver_id":null}`)
	if r.status != http.StatusOK || !strings.Contains(string(r.body), `"ended_at":"2026-10-25`) {
		t.Fatalf("unpair = %d %s, want 200 with ended row", r.status, r.body)
	}

	r = e.send(t, admin, http.MethodGet, vehiclePath, "")
	var history []Pairing
	decode(t, r.body, &history)
	if r.status != http.StatusOK || len(history) != 2 || history[0].ID < history[1].ID {
		t.Fatalf("GET pairings = %d %s, want two rows newest first", r.status, r.body)
	}

	refused := map[string]struct {
		body   string
		status int
		code   string
	}{
		"missing device_id":  {`{"driver_id":null}`, http.StatusBadRequest, "validation_failed"},
		"driver without dev": {fmt.Sprintf(`{"device_id":null,"driver_id":%d}`, drv), http.StatusBadRequest, "validation_failed"},
		"unknown field":      {`{"device_id":null,"vehicle_id":1}`, http.StatusBadRequest, "validation_failed"},
	}
	for name, tc := range refused {
		t.Run(name, func(t *testing.T) {
			r := e.send(t, admin, http.MethodPost, vehiclePath, tc.body)
			if r.status != tc.status || r.errCode(t) != tc.code {
				t.Fatalf("= %d %s, want %d %s", r.status, r.body, tc.status, tc.code)
			}
		})
	}

	t.Run("device held by another vehicle is 409 device_in_use", func(t *testing.T) {
		other := e.vehicle(t, fleet, "PR2")
		r := e.send(t, admin, http.MethodPost, fmt.Sprintf("/vehicles/%d/pairings", other),
			fmt.Sprintf(`{"device_id":%d,"driver_id":null}`, devB))
		if r.status != http.StatusCreated {
			t.Fatalf("pair on vehicle 2 = %d %s", r.status, r.body)
		}
		r = e.send(t, admin, http.MethodPost, vehiclePath, fmt.Sprintf(`{"device_id":%d,"driver_id":null}`, devB))
		if r.status != http.StatusConflict || r.errCode(t) != "device_in_use" {
			t.Fatalf("= %d %s, want 409 device_in_use", r.status, r.body)
		}
	})
	t.Run("retired vehicle is 409 vehicle_not_active", func(t *testing.T) {
		v := e.vehicle(t, fleet, "PR3")
		if r := e.send(t, admin, http.MethodPatch, fmt.Sprintf("/vehicles/%d", v), `{"status":"retired"}`); r.status != http.StatusOK {
			t.Fatalf("retire = %d %s", r.status, r.body)
		}
		r := e.send(t, admin, http.MethodPost, fmt.Sprintf("/vehicles/%d/pairings", v), fmt.Sprintf(`{"device_id":%d}`, devA))
		if r.status != http.StatusConflict || r.errCode(t) != "vehicle_not_active" {
			t.Fatalf("= %d %s, want 409 vehicle_not_active", r.status, r.body)
		}
	})
	t.Run("revoked device is 409 device_revoked", func(t *testing.T) {
		dev := e.issue(t, fleet, KindTracker)
		if r := e.send(t, admin, http.MethodPost, fmt.Sprintf("/devices/%d/revoke", dev), ""); r.status != http.StatusOK {
			t.Fatalf("revoke = %d %s", r.status, r.body)
		}
		r := e.send(t, admin, http.MethodPost, vehiclePath, fmt.Sprintf(`{"device_id":%d}`, dev))
		if r.status != http.StatusConflict || r.errCode(t) != "device_revoked" {
			t.Fatalf("= %d %s, want 409 device_revoked", r.status, r.body)
		}
	})
	t.Run("cross-fleet vehicle is 404, cross-fleet device is 400", func(t *testing.T) {
		fleetB := insertFleet(t, e.db, "B")
		bAdmin := e.as(t, fleetB, roleAdmin)
		devForeign := e.issue(t, fleetB, KindPWA)
		if r := e.send(t, bAdmin, http.MethodPost, vehiclePath, fmt.Sprintf(`{"device_id":%d}`, devForeign)); r.status != http.StatusNotFound {
			t.Fatalf("foreign vehicle = %d %s, want 404", r.status, r.body)
		}
		if r := e.send(t, admin, http.MethodPost, vehiclePath, fmt.Sprintf(`{"device_id":%d}`, devForeign)); r.status != http.StatusBadRequest {
			t.Fatalf("foreign device = %d %s, want 400", r.status, r.body)
		}
	})
}

// issue creates a device through the service and returns its id.
func (e *httpEnv) issue(t *testing.T, fleetID int64, kind string) int64 {
	t.Helper()
	d, err := e.svc.IssueDevice(context.Background(), Actor{UserID: e.user(t, fleetID, roleAdmin), FleetID: fleetID}, kind)
	mustOK(t, err, "issue device")
	return d.ID
}

// TestDeviceKeyLifecycle is validation item 8: issue, rotate, and revoke over
// HTTP. The key appears in exactly the issue and rotate responses, is never
// stored in plaintext, and dies on rotate.
func TestDeviceKeyLifecycle(t *testing.T) {
	e := newHTTPEnv(t)
	fleet := insertFleet(t, e.db, "A")
	admin := e.as(t, fleet, roleAdmin)
	ctx := context.Background()

	issued := e.send(t, admin, http.MethodPost, "/devices", `{"kind":"pwa"}`)
	if issued.status != http.StatusCreated {
		t.Fatalf("POST /devices = %d %s", issued.status, issued.body)
	}
	var iss IssuedDevice
	decode(t, issued.body, &iss)
	if got := keyPattern.FindAllString(string(issued.body), -1); len(got) != 1 || got[0] != iss.APIKey {
		t.Fatalf("issue response holds keys %v, want exactly %q", got, iss.APIKey)
	}
	id, _, err := devicekey.Parse(iss.APIKey)
	if err != nil || id != iss.ID {
		t.Fatalf("issued key %q does not parse to device %d: %v", iss.APIKey, iss.ID, err)
	}

	var hashLen int
	if err := e.db.QueryRow(`SELECT octet_length(api_key_hash) FROM devices WHERE id = $1`, iss.ID).Scan(&hashLen); err != nil || hashLen != 32 {
		t.Fatalf("api_key_hash length = %d (%v), want 32", hashLen, err)
	}
	if n := countRowsWhere(t, e.db, `api_key_hash = convert_to($1, 'UTF8')`, iss.APIKey); n != 0 {
		t.Fatalf("plaintext key found in devices.api_key_hash")
	}
	if _, err := AuthenticateDeviceKey(ctx, e.db, storeSalt, iss.APIKey); err != nil {
		t.Fatalf("issued key does not authenticate: %v", err)
	}

	e.now = e.now.Add(time.Minute)
	rotated := e.send(t, admin, http.MethodPost, fmt.Sprintf("/devices/%d/rotate", iss.ID), "")
	if rotated.status != http.StatusOK {
		t.Fatalf("rotate = %d %s", rotated.status, rotated.body)
	}
	var rot IssuedDevice
	decode(t, rotated.body, &rot)
	if got := keyPattern.FindAllString(string(rotated.body), -1); len(got) != 1 || got[0] != rot.APIKey || rot.APIKey == iss.APIKey {
		t.Fatalf("rotate response keys %v, want one new key", got)
	}
	if _, err := AuthenticateDeviceKey(ctx, e.db, storeSalt, iss.APIKey); !errors.Is(err, ErrDeviceKeyInvalid) {
		t.Fatalf("old key after rotate = %v, want ErrDeviceKeyInvalid", err)
	}
	if _, err := AuthenticateDeviceKey(ctx, e.db, storeSalt, rot.APIKey); err != nil {
		t.Fatalf("rotated key does not authenticate: %v", err)
	}

	revoked := e.send(t, admin, http.MethodPost, fmt.Sprintf("/devices/%d/revoke", iss.ID), "")
	if revoked.status != http.StatusOK || keyPattern.Match(revoked.body) {
		t.Fatalf("revoke = %d %s", revoked.status, revoked.body)
	}
	var first Device
	decode(t, revoked.body, &first)
	e.now = e.now.Add(time.Minute)
	again := e.send(t, admin, http.MethodPost, fmt.Sprintf("/devices/%d/revoke", iss.ID), "")
	var second Device
	decode(t, again.body, &second)
	if again.status != http.StatusOK || first.RevokedAt == nil || second.RevokedAt == nil || !first.RevokedAt.Equal(*second.RevokedAt) {
		t.Fatalf("second revoke = %d %s, want the first timestamp", again.status, again.body)
	}
	if r := e.send(t, admin, http.MethodPost, fmt.Sprintf("/devices/%d/rotate", iss.ID), ""); r.status != http.StatusConflict || r.errCode(t) != "device_revoked" {
		t.Fatalf("rotate revoked = %d %s, want 409 device_revoked", r.status, r.body)
	}
	if _, err := AuthenticateDeviceKey(ctx, e.db, storeSalt, rot.APIKey); !errors.Is(err, ErrDeviceRevoked) {
		t.Fatalf("revoked key = %v, want ErrDeviceRevoked", err)
	}

	t.Run("refusals write no key", func(t *testing.T) {
		for _, tc := range []struct{ path, body string }{
			{"/devices", `{"kind":"broker"}`},
			{"/devices", `{"kind":"pwa","fleet_id":9}`},
		} {
			r := e.send(t, admin, http.MethodPost, tc.path, tc.body)
			if r.status != http.StatusBadRequest || keyPattern.Match(r.body) {
				t.Fatalf("%s %s = %d %s", tc.path, tc.body, r.status, r.body)
			}
		}
		other := e.as(t, insertFleet(t, e.db, "B"), roleAdmin)
		if r := e.send(t, other, http.MethodPost, fmt.Sprintf("/devices/%d/rotate", iss.ID), ""); r.status != http.StatusNotFound {
			t.Fatalf("cross-fleet rotate = %d, want 404", r.status)
		}
	})
}

// countRowsWhere counts devices matching a predicate with one bound argument.
func countRowsWhere(t *testing.T, db *sql.DB, predicate string, arg any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM devices WHERE `+predicate, arg).Scan(&n); err != nil {
		t.Fatalf("count devices: %v", err)
	}
	return n
}
