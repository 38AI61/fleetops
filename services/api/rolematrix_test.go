package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"fleetops/internal/registry"
)

// roleCols are the callers of the role matrix (R-6). "" means no token.
var roleCols = []struct{ name, role string }{
	{"admin", "admin"},
	{"manager", "manager"},
	{"dispatcher", "dispatcher"},
	{"driver", "driver"},
	{"unauthenticated", ""},
}

// TestRoleMatrix is the R-6 table: every P2 user verb and every P3 registry
// verb against every caller. Expected values follow spec 03 group 11 and GF-28.
// Future phases add rows to this table.
func TestRoleMatrix(t *testing.T) {
	h := startServer(t)
	fleet := h.fleet(t, "A")
	tokens := map[string]string{"": ""}
	ids := map[string]int64{}
	for _, c := range roleCols {
		if c.role == "" {
			continue
		}
		email := c.role + "@a.example"
		ids[c.role] = h.user(t, fleet, email, c.role)
		tokens[c.role] = h.login(t, email)
	}
	target := h.user(t, fleet, "target@a.example", "dispatcher")
	targetPath := fmt.Sprintf("/users/%d", target)

	// Registry fixtures. The admin writes them through the service so the
	// matrix rows start from known rows; the driver caller is linked to driver.
	svc := &registry.Service{DB: h.db, Salt: testSalt, Clock: h.clock.now}
	admin := registry.Actor{UserID: ids["admin"], FleetID: fleet}
	ctx := context.Background()
	vehicle, err := svc.CreateVehicle(ctx, admin, registry.VehicleInput{Plate: "RMX0001"})
	if err != nil {
		t.Fatalf("fixture vehicle: %v", err)
	}
	vehiclePath := fmt.Sprintf("/vehicles/%d", vehicle.ID)
	driver, err := svc.CreateDriver(ctx, admin, registry.DriverInput{Name: "Linked", LicenseNo: "LINKED01"})
	if err != nil {
		t.Fatalf("fixture driver: %v", err)
	}
	if _, err := h.db.Exec(`UPDATE users SET driver_id = $1 WHERE id = $2`, driver.ID, ids["driver"]); err != nil {
		t.Fatalf("link driver user: %v", err)
	}
	driverPath := fmt.Sprintf("/drivers/%d", driver.ID)
	pairingsPath := fmt.Sprintf("/vehicles/%d/pairings", vehicle.ID)
	rotateDev, err := svc.IssueDevice(ctx, admin, registry.KindPWA)
	if err != nil {
		t.Fatalf("fixture device: %v", err)
	}
	revokeDev, err := svc.IssueDevice(ctx, admin, registry.KindTracker)
	if err != nil {
		t.Fatalf("fixture device: %v", err)
	}

	const (
		ok200 = http.StatusOK
		ok201 = http.StatusCreated
		forb  = http.StatusForbidden
		unau  = http.StatusUnauthorized
		bad   = http.StatusBadRequest
	)
	// want[row][caller], in roleCols order: admin, manager, dispatcher, driver, unauthenticated.
	rows := []struct {
		name   string
		method string
		path   string
		body   func(n int) string
		want   [5]int
	}{
		{"GET /users", http.MethodGet, "/users", func(int) string { return "" }, [5]int{ok200, forb, forb, forb, unau}},
		{"POST /users", http.MethodPost, "/users", func(n int) string {
			return fmt.Sprintf(`{"email":"new%d@a.example","password":"%s","role":"driver"}`, n, testPassword)
		}, [5]int{ok201, forb, forb, forb, unau}},
		{"PATCH /users/:id", http.MethodPatch, targetPath, func(int) string {
			return `{"role":"dispatcher"}`
		}, [5]int{ok200, forb, forb, forb, unau}},
		// GF-28: role check runs before body parsing, so a malformed body is 403 for non-admins.
		{"POST /users (malformed body)", http.MethodPost, "/users", func(int) string {
			return `{"email":`
		}, [5]int{bad, forb, forb, forb, unau}},

		{"GET /vehicles", http.MethodGet, "/vehicles", func(int) string { return "" }, [5]int{ok200, ok200, ok200, forb, unau}},
		{"GET /vehicles/:id", http.MethodGet, vehiclePath, func(int) string { return "" }, [5]int{ok200, ok200, ok200, forb, unau}},
		{"POST /vehicles", http.MethodPost, "/vehicles", func(n int) string {
			return fmt.Sprintf(`{"plate":"RM%06d"}`, n)
		}, [5]int{ok201, ok201, ok201, forb, unau}},
		{"PATCH /vehicles/:id", http.MethodPatch, vehiclePath, func(int) string {
			return `{"model":"Ford Transit"}`
		}, [5]int{ok200, ok200, forb, forb, unau}},

		// GF-126: any role reads its own fleet's map; no credential gets 401.
		// The bbox is a city-sized viewport: wide boxes hit the geography
		// predicate problem recorded in the P8 report, which is not this row's concern.
		{"GET /map/vehicles", http.MethodGet, "/map/vehicles?bbox=13.30,52.45,13.50,52.60", func(int) string { return "" },
			[5]int{ok200, ok200, ok200, ok200, unau}},

		{"GET /drivers", http.MethodGet, "/drivers", func(int) string { return "" }, [5]int{ok200, ok200, ok200, ok200, unau}},
		{"GET /drivers/:id (own id for driver)", http.MethodGet, driverPath, func(int) string { return "" }, [5]int{ok200, ok200, ok200, ok200, unau}},
		{"POST /drivers", http.MethodPost, "/drivers", func(n int) string {
			return fmt.Sprintf(`{"name":"N%d","license_no":"LIC%08d"}`, n, n)
		}, [5]int{ok201, ok201, ok201, forb, unau}},
		{"PATCH /drivers/:id", http.MethodPatch, driverPath, func(int) string {
			return `{"phone":"+90 555 0000"}`
		}, [5]int{ok200, ok200, forb, forb, unau}},

		{"GET /vehicles/:id/pairings", http.MethodGet, pairingsPath, func(int) string { return "" }, [5]int{ok200, ok200, ok200, forb, unau}},
		// Each pair cell pairs a fresh device, so the admin and manager cells are both new rows (201).
		{"POST /vehicles/:id/pairings", http.MethodPost, pairingsPath, func(int) string {
			d, err := svc.IssueDevice(ctx, admin, registry.KindPWA)
			if err != nil {
				t.Fatalf("pairing fixture device: %v", err)
			}
			return fmt.Sprintf(`{"device_id":%d,"driver_id":null}`, d.ID)
		}, [5]int{ok201, ok201, forb, forb, unau}},

		{"POST /devices", http.MethodPost, "/devices", func(int) string {
			return `{"kind":"pwa"}`
		}, [5]int{ok201, forb, forb, forb, unau}},
		{"POST /devices/:id/rotate", http.MethodPost, fmt.Sprintf("/devices/%d/rotate", rotateDev.ID), func(int) string { return "" },
			[5]int{ok200, forb, forb, forb, unau}},
		// Revoke comes after rotate: rotate needs a live device.
		{"POST /devices/:id/revoke", http.MethodPost, fmt.Sprintf("/devices/%d/revoke", revokeDev.ID), func(int) string { return "" },
			[5]int{ok200, forb, forb, forb, unau}},
	}

	seq := 0 // gives each POST a unique email, plate, or license
	for _, row := range rows {
		for i, c := range roleCols {
			seq++
			// Bodies are built here, outside the subtest, so a fixture failure is a fatal on t.
			body := row.body(seq)
			t.Run(row.name+"/"+c.name, func(t *testing.T) {
				r := h.do(t, row.method, row.path, tokens[c.role], body, nil)
				if r.status != row.want[i] {
					t.Fatalf("status = %d (%s), want %d", r.status, r.body, row.want[i])
				}
			})
		}
	}

	// Login, refresh, and logout take no JWT; their expected results come from §8.
	t.Run("no-JWT/POST /auth/login valid credentials", func(t *testing.T) {
		r := h.do(t, http.MethodPost, "/auth/login", "",
			fmt.Sprintf(`{"email":"admin@a.example","password":"%s"}`, testPassword), nil)
		if r.status != ok200 {
			t.Fatalf("status = %d, want 200", r.status)
		}
	})
	t.Run("no-JWT/POST /auth/login wrong password", func(t *testing.T) {
		r := h.do(t, http.MethodPost, "/auth/login", "",
			`{"email":"admin@a.example","password":"wrong password long"}`, nil)
		if r.status != unau || r.errCode(t) != "invalid_credentials" {
			t.Fatalf("status = %d %s, want 401 invalid_credentials", r.status, r.body)
		}
	})
	t.Run("no-JWT/POST /auth/refresh without cookie", func(t *testing.T) {
		if r := h.do(t, http.MethodPost, "/auth/refresh", "", "", nil); r.status != unau {
			t.Fatalf("status = %d, want 401", r.status)
		}
	})
	t.Run("no-JWT/POST /auth/logout without cookie", func(t *testing.T) {
		if r := h.do(t, http.MethodPost, "/auth/logout", "", "", nil); r.status != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", r.status)
		}
	})
}

// TestRoleMatrixIngest is the R-6 row for POST /ingest/position (GF-64). The
// device route refuses every JWT, so each JWT column and the no-auth column get
// 401; a valid device key gets 204; a malformed key gets 401.
func TestRoleMatrixIngest(t *testing.T) {
	h := startServer(t)
	fleet := h.fleet(t, "IngestMatrix")
	tokens := map[string]string{}
	var adminID int64
	for _, c := range roleCols {
		if c.role == "" {
			continue
		}
		email := c.role + "@ingest-matrix.example"
		id := h.user(t, fleet, email, c.role)
		if c.role == "admin" {
			adminID = id
		}
		tokens[c.role] = h.login(t, email)
	}
	svc := &registry.Service{DB: h.db, Salt: testSalt, Clock: h.clock.now}
	dev, err := svc.IssueDevice(context.Background(), registry.Actor{UserID: adminID, FleetID: fleet}, registry.KindPWA)
	if err != nil {
		t.Fatalf("fixture device: %v", err)
	}
	body := fmt.Sprintf(`{"lat":41,"lng":29,"speed":50,"heading":90,"accuracy":8,"ts":%q}`, h.clock.t.Format(time.RFC3339))

	const path = "/ingest/position"
	for _, c := range roleCols {
		if c.role == "" {
			continue
		}
		t.Run("POST "+path+"/JWT "+c.name, func(t *testing.T) {
			r := h.postIngest(t, tokens[c.role], "", body)
			if r.status != http.StatusUnauthorized || r.errCode(t) != "unauthorized" {
				t.Fatalf("status = %d (%s), want 401 unauthorized", r.status, r.body)
			}
		})
	}
	t.Run("POST "+path+"/no auth", func(t *testing.T) {
		r := h.postIngest(t, "", "", body)
		if r.status != http.StatusUnauthorized || r.errCode(t) != "unauthorized" {
			t.Fatalf("status = %d (%s), want 401 unauthorized", r.status, r.body)
		}
	})
	t.Run("POST "+path+"/valid device key", func(t *testing.T) {
		r := h.postIngest(t, "", dev.APIKey, body)
		if r.status != http.StatusNoContent {
			t.Fatalf("status = %d (%s), want 204", r.status, r.body)
		}
	})
	t.Run("POST "+path+"/malformed device key", func(t *testing.T) {
		r := h.postIngest(t, "", "fo_dev_nope", body)
		if r.status != http.StatusUnauthorized || r.errCode(t) != "unauthorized" {
			t.Fatalf("status = %d (%s), want 401 unauthorized", r.status, r.body)
		}
	})
}

// postIngest sends one POST /ingest/position. bearer, when set, goes in
// Authorization; deviceKey, when set, goes in X-Device-Key.
func (h *harness) postIngest(t *testing.T, bearer, deviceKey, body string) reply {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.url+"/ingest/position", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if deviceKey != "" {
		req.Header.Set("X-Device-Key", deviceKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /ingest/position: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return reply{status: resp.StatusCode, body: b}
}
