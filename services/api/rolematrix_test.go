package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"fleetops/internal/jobs"
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

	// freshJob creates a created job in the admin's fleet and returns the path for action.
	freshJob := func(action string) func() string {
		return func() string {
			job, err := (&jobs.Store{DB: h.db}).Create(ctx, ids["admin"], fleet, jobs.NewJob{
				Pickup:  jobs.Point{Lat: 41.0, Lng: 29.0},
				Dropoff: jobs.Point{Lat: 41.1, Lng: 29.1},
			})
			if err != nil {
				t.Fatalf("fixture job: %v", err)
			}
			return fmt.Sprintf("/jobs/%d/%s", job.ID, action)
		}
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

		// P10 job rows (GF-177). Each row with a job id starts from a fresh created job.
		{"POST /jobs", http.MethodPost, "/jobs", func(int) string {
			return `{"pickup":{"lat":41.0,"lng":29.0},"dropoff":{"lat":41.1,"lng":29.1}}`
		}, [5]int{ok201, forb, ok201, forb, unau}},
		{"GET /jobs", http.MethodGet, "/jobs", func(int) string { return "" }, [5]int{ok200, ok200, ok200, forb, unau}},
		{"POST /jobs/:id/assign", http.MethodPost, "/jobs/0/assign", func(int) string {
			return fmt.Sprintf(`{"vehicle_id":%d,"driver_id":%d}`, vehicle.ID, driver.ID)
		}, [5]int{ok200, forb, ok200, forb, unau}},
		{"POST /jobs/:id/cancel", http.MethodPost, "/jobs/0/cancel", func(int) string { return "" },
			[5]int{ok200, forb, ok200, forb, unau}},

		// P11 driver rows (GF-186). Only the driver cell is allowed; the others are refused by role.
		{"GET /driver/jobs/today", http.MethodGet, "/driver/jobs/today", func(int) string { return "" },
			[5]int{forb, forb, forb, ok200, unau}},
		{"POST /driver/jobs/:id/accept", http.MethodPost, "/driver/jobs/0/accept", func(int) string { return "" },
			[5]int{forb, forb, forb, ok200, unau}},
		{"POST /driver/jobs/:id/start", http.MethodPost, "/driver/jobs/0/start", func(int) string { return "" },
			[5]int{forb, forb, forb, ok200, unau}},
		{"POST /driver/jobs/:id/pickup", http.MethodPost, "/driver/jobs/0/pickup", func(int) string { return "" },
			[5]int{forb, forb, forb, ok200, unau}},
		{"POST /driver/jobs/:id/fail", http.MethodPost, "/driver/jobs/0/fail", func(int) string {
			return `{"reason":"matrix"}`
		}, [5]int{forb, forb, forb, ok200, unau}},
	}

	// driverJob creates a job assigned to the linked driver, advances it through
	// steps as the admin, and returns the driver route for action. Only the driver
	// cell mutates it, so one job per row is enough.
	driverJob := func(action string, steps ...jobs.State) func() string {
		return func() string {
			st := &jobs.Store{DB: h.db}
			job, err := st.Create(ctx, ids["admin"], fleet, jobs.NewJob{
				Pickup:  jobs.Point{Lat: 41.0, Lng: 29.0},
				Dropoff: jobs.Point{Lat: 41.1, Lng: 29.1},
			})
			if err != nil {
				t.Fatalf("fixture job: %v", err)
			}
			if _, _, err := st.Assign(ctx, ids["admin"], fleet, job.ID, vehicle.ID, driver.ID); err != nil {
				t.Fatalf("fixture assign: %v", err)
			}
			for _, to := range steps {
				if _, _, err := st.Transition(ctx, ids["admin"], fleet, job.ID, to); err != nil {
					t.Fatalf("fixture advance to %s: %v", to, err)
				}
			}
			return fmt.Sprintf("/driver/jobs/%d/%s", job.ID, action)
		}
	}

	// jobRowSetup gives each job row a fresh job in the state its route needs, so every row starts from the same state.
	jobRowSetup := map[string]func() string{
		"POST /jobs/:id/assign":        freshJob("assign"),
		"POST /jobs/:id/cancel":        freshJob("cancel"),
		"POST /driver/jobs/:id/accept": driverJob("accept"),
		"POST /driver/jobs/:id/start":  driverJob("start", jobs.StateAccepted),
		"POST /driver/jobs/:id/pickup": driverJob("pickup", jobs.StateAccepted, jobs.StateEnRoute),
		"POST /driver/jobs/:id/fail":   driverJob("fail", jobs.StateAccepted),
	}

	seq := 0 // gives each POST a unique email, plate, or license
	for _, row := range rows {
		path := row.path
		if setup, ok := jobRowSetup[row.name]; ok {
			path = setup()
		}
		for i, c := range roleCols {
			seq++
			// Bodies are built here, outside the subtest, so a fixture failure is a fatal on t.
			body := row.body(seq)
			t.Run(row.name+"/"+c.name, func(t *testing.T) {
				r := h.do(t, row.method, path, tokens[c.role], body, nil)
				if r.status != row.want[i] {
					t.Fatalf("status = %d (%s), want %d", r.status, r.body, row.want[i])
				}
			})
		}
	}

	// P12 deliver row (GF-206). The driver cell sends a valid multipart photo with
	// lat and lng and gets 200. The other cells send no body, because the role
	// check runs before any body read. Each cell gets its own picked_up job, so a
	// 2xx in one cell cannot change the job another cell sees. The row is a
	// separate loop: the rows above use positional literals, and a content type
	// field would touch every one of them.
	deliverJob := driverJob("deliver", jobs.StateAccepted, jobs.StateEnRoute, jobs.StatePickedUp)
	deliverWant := [5]int{forb, forb, forb, ok200, unau}
	for i, c := range roleCols {
		path := deliverJob()
		ctype, body := "", []byte(nil)
		if c.role == "driver" {
			ctype, body = deliverForm(t, jpegPhoto, validCoords)
		}
		t.Run("POST /driver/jobs/:id/deliver/"+c.name, func(t *testing.T) {
			r := h.send(t, http.MethodPost, path, tokens[c.role], ctype, body)
			if r.status != deliverWant[i] {
				t.Fatalf("status = %d (%s), want %d", r.status, r.body, deliverWant[i])
			}
		})
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
