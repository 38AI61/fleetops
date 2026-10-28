package alerts

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"fleetops/internal/auth"
	"fleetops/internal/testinfra"
)

// testSecret is 32 bytes, the minimum (D-03). It is never a real secret.
var testSecret = []byte(strings.Repeat("a", 32))

// env is the alert routes on an echo instance backed by a migrated PostGIS
// database. Requests go through ServeHTTP, so no port is bound.
type env struct {
	t   *testing.T
	e   *echo.Echo
	db  *sql.DB
	iss *auth.Issuer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db := testinfra.NewMigratedDB(t)
	iss, err := auth.NewIssuer(testSecret, time.Now)
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	e := echo.New()
	e.HideBanner = true
	(&Handler{Store: &Store{DB: db}}).Register(e, auth.RequireAuth(iss))
	return &env{t: t, e: e, db: db, iss: iss}
}

// user inserts a user with the role in the fleet and returns its id and a bearer token.
func (v *env) user(fleet int64, role string) (int64, string) {
	v.t.Helper()
	uid := newUser(v.t, v.db, fleet, role)
	tok, err := v.iss.Issue(uid, fleet, role)
	if err != nil {
		v.t.Fatalf("Issue: %v", err)
	}
	return uid, tok
}

// fleet inserts a fleet and seeds its three rules, as seed does.
func (v *env) fleet(name string) int64 {
	v.t.Helper()
	f := newFleet(v.t, v.db, name)
	tx, err := v.db.BeginTx(context.Background(), nil)
	if err != nil {
		v.t.Fatalf("begin: %v", err)
	}
	if err := SeedRules(context.Background(), tx, f); err != nil {
		_ = tx.Rollback()
		v.t.Fatalf("SeedRules: %v", err)
	}
	if err := tx.Commit(); err != nil {
		v.t.Fatalf("commit: %v", err)
	}
	return f
}

func (v *env) do(method, path, tok, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if tok != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+tok)
	}
	if body != "" {
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	rec := httptest.NewRecorder()
	v.e.ServeHTTP(rec, req)
	return rec
}

// errCode returns the apierr code of a response body, or "" when there is none.
func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Error
}

// TestAlertsRoleMatrix: each P19 route answers per the GF-298 table for
// admin, manager, dispatcher, driver and anonymous. Each ack case gets its own alert.
func TestAlertsRoleMatrix(t *testing.T) {
	v := newEnv(t)
	fleet := v.fleet("matrix")
	const (
		ok   = http.StatusOK
		forb = http.StatusForbidden
		unau = http.StatusUnauthorized
	)
	roles := []string{"admin", "manager", "dispatcher", "driver", ""}

	type route struct {
		name   string
		method string
		path   func(int) string
		body   string
		want   [5]int
	}
	routes := []route{
		{"GET /alerts", http.MethodGet, func(int) string { return "/alerts" }, "", [5]int{ok, ok, ok, forb, unau}},
		{"PATCH /alerts/:id/ack", http.MethodPatch, func(n int) string {
			return fmt.Sprintf("/alerts/%d/ack", newAlert(t, v.db, fleet, KindDeviceSilent, SeverityWarning))
		}, "", [5]int{ok, ok, ok, forb, unau}},
		{"GET /alert-rules", http.MethodGet, func(int) string { return "/alert-rules" }, "", [5]int{ok, ok, forb, forb, unau}},
		{"PATCH /alert-rules", http.MethodPatch, func(int) string { return "/alert-rules" }, `{"rules":[]}`, [5]int{ok, ok, forb, forb, unau}},
	}
	for _, r := range routes {
		for i, role := range roles {
			name := role
			if name == "" {
				name = "anonymous"
			}
			t.Run(r.name+"/"+name, func(t *testing.T) {
				tok := ""
				if role != "" {
					_, tok = v.user(fleet, role)
				}
				rec := v.do(r.method, r.path(i), tok, r.body)
				if rec.Code != r.want[i] {
					t.Fatalf("status = %d (%s), want %d", rec.Code, rec.Body.String(), r.want[i])
				}
			})
		}
	}
}

// TestAlertsListQuery: since and limit are validated (GF-298).
func TestAlertsListQuery(t *testing.T) {
	v := newEnv(t)
	fleet := v.fleet("query")
	_, tok := v.user(fleet, "dispatcher")
	cases := []struct {
		query string
		want  int
	}{
		{"", http.StatusOK},
		{"?since=2026-10-27T00:00:00Z", http.StatusOK},
		{"?since=yesterday", http.StatusBadRequest},
		{"?limit=1", http.StatusOK},
		{"?limit=200", http.StatusOK},
		{"?limit=0", http.StatusBadRequest},
		{"?limit=201", http.StatusBadRequest},
		{"?limit=abc", http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run("GET /alerts"+c.query, func(t *testing.T) {
			rec := v.do(http.MethodGet, "/alerts"+c.query, tok, "")
			if rec.Code != c.want {
				t.Fatalf("status = %d (%s), want %d", rec.Code, rec.Body.String(), c.want)
			}
			if c.want == http.StatusBadRequest && errCode(t, rec) != "validation_failed" {
				t.Fatalf("code = %s, want validation_failed", rec.Body.String())
			}
		})
	}
}

// TestAlertsAckAPI: ack 200 with the alert body, second ack 409, another
// fleet's alert 404, malformed and absent ids 404 (GF-295, GF-298).
func TestAlertsAckAPI(t *testing.T) {
	v := newEnv(t)
	fleet := v.fleet("ack-api")
	other := v.fleet("ack-other")
	uid, tok := v.user(fleet, "manager")
	_, otherTok := v.user(other, "manager")
	id := newAlert(t, v.db, fleet, KindDeviceSilent, SeverityWarning)
	foreign := newAlert(t, v.db, other, KindDeviceSilent, SeverityWarning)
	path := fmt.Sprintf("/alerts/%d/ack", id)

	rec := v.do(http.MethodPatch, path, tok, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("first ack = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var a Alert
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		t.Fatalf("ack body: %v", err)
	}
	if a.ID != id || a.FleetID != fleet || a.AckedBy == nil || *a.AckedBy != uid || a.AckedAt == nil {
		t.Fatalf("ack body = %+v, want acked by %d", a, uid)
	}

	rec = v.do(http.MethodPatch, path, tok, "")
	if rec.Code != http.StatusConflict || errCode(t, rec) != "conflict" {
		t.Fatalf("second ack = %d (%s), want 409 conflict", rec.Code, rec.Body.String())
	}
	rec = v.do(http.MethodPatch, fmt.Sprintf("/alerts/%d/ack", foreign), tok, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-fleet ack = %d (%s), want 404", rec.Code, rec.Body.String())
	}
	rec = v.do(http.MethodPatch, fmt.Sprintf("/alerts/%d/ack", foreign), otherTok, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("owner ack = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	for _, p := range []string{"/alerts/abc/ack", "/alerts/999999999/ack"} {
		rec = v.do(http.MethodPatch, p, tok, "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("PATCH %s = %d (%s), want 404", p, rec.Code, rec.Body.String())
		}
	}
}

// TestAlertsListIsolationAndWindow: the list is fleet-scoped and since-filtered,
// and unacked counts every unacked alert in the fleet regardless of since.
func TestAlertsListIsolationAndWindow(t *testing.T) {
	v := newEnv(t)
	fleet := v.fleet("list-a")
	other := v.fleet("list-b")
	_, tok := v.user(fleet, "admin")

	recent := newAlert(t, v.db, fleet, KindDeviceSilent, SeverityWarning)
	var old int64
	if err := v.db.QueryRow(
		`INSERT INTO alerts (fleet_id, kind, severity, payload, raised_at)
		 VALUES ($1, $2, $3, '{}'::jsonb, now() - interval '48 hours') RETURNING id`,
		fleet, KindGeofenceExit, SeverityCritical,
	).Scan(&old); err != nil {
		t.Fatalf("insert old alert: %v", err)
	}
	foreign := newAlert(t, v.db, other, KindDeviceSilent, SeverityWarning)

	rec := v.do(http.MethodGet, "/alerts", tok, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d (%s)", rec.Code, rec.Body.String())
	}
	var page struct {
		Alerts  []Alert `json:"alerts"`
		Unacked int64   `json:"unacked"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("list body: %v", err)
	}
	if len(page.Alerts) != 1 || page.Alerts[0].ID != recent {
		t.Fatalf("24 h list = %+v, want only alert %d", page.Alerts, recent)
	}
	if page.Unacked != 2 {
		t.Fatalf("unacked = %d, want 2 (the 48 h alert counts)", page.Unacked)
	}
	for _, a := range page.Alerts {
		if a.ID == foreign {
			t.Fatal("list leaked another fleet's alert")
		}
	}

	rec = v.do(http.MethodGet, "/alerts?since="+time.Now().Add(-72*time.Hour).UTC().Format(time.RFC3339), tok, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || len(page.Alerts) != 2 {
		t.Fatalf("72 h list = %s, want two alerts", rec.Body.String())
	}
}

// TestAlertRulesPatch: unknown kind, out-of-range and malformed params are 400;
// partial updates keep untouched fields; eta_slip reports evaluated true (GF-290, GF-298, GF-317).
func TestAlertRulesPatch(t *testing.T) {
	v := newEnv(t)
	fleet := v.fleet("rules")
	_, tok := v.user(fleet, "admin")

	bad := []string{
		`{"rules":[{"kind":"nope","enabled":true}]}`,
		`{"rules":[{"kind":"device_silent","params":{"threshold_s":59}}]}`,
		`{"rules":[{"kind":"device_silent","params":{"threshold_s":86401}}]}`,
		`{"rules":[{"kind":"device_silent","params":{"threshold_s":600.5}}]}`,
		`{"rules":[{"kind":"device_silent","params":{"threshold_s":"600"}}]}`,
		`{"rules":[{"kind":"device_silent","params":{}}]}`,
		`{"rules":[{"kind":"eta_slip","params":{"x":1}}]}`,
		`{"rules":[{"kind":"device_silent"},{"kind":"device_silent"}]}`,
		`{"rules":[{"kind":"device_silent","bogus":1}]}`,
		``,
	}
	for _, b := range bad {
		rec := v.do(http.MethodPatch, "/alert-rules", tok, b)
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
			t.Fatalf("PATCH %s = %d (%s), want 400 validation_failed", b, rec.Code, rec.Body.String())
		}
	}

	rec := v.do(http.MethodPatch, "/alert-rules", tok,
		`{"rules":[{"kind":"device_silent","params":{"threshold_s":300}},{"kind":"geofence_exit_outside_schedule","enabled":false}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("partial patch = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var body struct {
		Rules []Rule `json:"rules"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("rules body: %v", err)
	}
	if len(body.Rules) != 3 {
		t.Fatalf("rules = %d, want 3", len(body.Rules))
	}
	got := map[string]Rule{}
	for _, r := range body.Rules {
		got[r.Kind] = r
	}
	silent := got[KindDeviceSilent]
	if !silent.Enabled || !silent.Evaluated || string(silent.Params) != `{"threshold_s":300}` {
		t.Fatalf("device_silent = %+v, want enabled, evaluated, threshold 300", silent)
	}
	if got[KindGeofenceExit].Enabled {
		t.Fatal("geofence_exit_outside_schedule still enabled after patch")
	}
	eta := got[KindETASlip]
	if eta.Enabled || !eta.Evaluated {
		t.Fatalf("eta_slip = %+v, want disabled and evaluated true (GF-317)", eta)
	}

	rec = v.do(http.MethodPatch, "/alert-rules", tok, `{"rules":[{"kind":"eta_slip","enabled":true}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("eta_slip enable = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("rules body: %v", err)
	}
	for _, r := range body.Rules {
		if r.Kind == KindETASlip && (!r.Enabled || !r.Evaluated) {
			t.Fatalf("eta_slip after enable = %+v, want enabled, evaluated true", r)
		}
	}

	// The read side agrees with the write: GET shows the enabled eta_slip as evaluated (GF-317).
	rec = v.do(http.MethodGet, "/alert-rules", tok, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /alert-rules = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var listed struct {
		Rules []Rule `json:"rules"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("GET rules body: %v", err)
	}
	for _, r := range listed.Rules {
		if r.Kind == KindETASlip && (!r.Enabled || !r.Evaluated) {
			t.Fatalf("GET eta_slip = %+v, want enabled and evaluated true", r)
		}
	}
}
