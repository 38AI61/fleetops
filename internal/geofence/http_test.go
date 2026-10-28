package geofence

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"fleetops/internal/auth"
	"fleetops/internal/testinfra"
)

// testSecret is 32 bytes, the minimum (D-03). It is never a real secret.
var testSecret = []byte(strings.Repeat("g", 32))

// env is the geofence routes on an echo instance backed by a migrated PostGIS
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

// token inserts a user with the role in the fleet and returns a bearer token.
func (v *env) token(fleet int64, role string) string {
	v.t.Helper()
	var uid int64
	email := fmt.Sprintf("%s-%d-%d@geo.example", role, fleet, time.Now().UnixNano())
	if err := v.db.QueryRow(
		`INSERT INTO users (fleet_id, email, password_hash, role) VALUES ($1, $2, 'x', $3) RETURNING id`,
		fleet, email, role).Scan(&uid); err != nil {
		v.t.Fatalf("insert user: %v", err)
	}
	tok, err := v.iss.Issue(uid, fleet, role)
	if err != nil {
		v.t.Fatalf("Issue: %v", err)
	}
	return tok
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

// count reads the stored fence count for the fleet straight from SQL.
func (v *env) count(fleet int64) int {
	v.t.Helper()
	var n int
	if err := v.db.QueryRow(`SELECT count(*) FROM geofences WHERE fleet_id = $1`, fleet).Scan(&n); err != nil {
		v.t.Fatalf("count: %v", err)
	}
	return n
}

// expectError asserts status and the apierr code.
func expectError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d (%s), want %d", rec.Code, rec.Body.String(), status)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] != code {
		t.Fatalf("body = %s, want error %q", rec.Body.String(), code)
	}
}

// polygon builds a Feature with a Polygon ring and the given property text.
func polygon(props, ring string) string {
	return `{"type":"Feature","geometry":{"type":"Polygon","coordinates":[` + ring + `]},"properties":{` + props + `}}`
}

// circle builds a Feature with a Point centre (13.4, 52.5) and the given property text.
func circle(props string) string {
	return `{"type":"Feature","geometry":{"type":"Point","coordinates":[13.4,52.5]},"properties":{` + props + `}}`
}

// ccwSquare is a counter-clockwise square ring (RFC 7946 orientation).
const ccwSquare = `[[13.0,52.0],[13.1,52.0],[13.1,52.1],[13.0,52.1],[13.0,52.0]]`

// geometryOf decodes the geometry member of a Feature body or response.
func geometryOf(t *testing.T, body []byte) any {
	t.Helper()
	var f struct {
		Geometry any `json:"geometry"`
	}
	if err := json.Unmarshal(body, &f); err != nil {
		t.Fatalf("decode geometry: %v (%s)", err, body)
	}
	return f.Geometry
}

// listFeatures returns the fleet's GET /geofences features.
func (v *env) listFeatures(tok string) []Feature {
	v.t.Helper()
	rec := v.do(http.MethodGet, "/geofences", tok, "")
	if rec.Code != http.StatusOK {
		v.t.Fatalf("GET status = %d (%s)", rec.Code, rec.Body.String())
	}
	var col Collection
	if err := json.Unmarshal(rec.Body.Bytes(), &col); err != nil {
		v.t.Fatalf("decode collection: %v", err)
	}
	if col.Type != "FeatureCollection" {
		v.t.Fatalf("type = %q, want FeatureCollection", col.Type)
	}
	return col.Features
}

// TestGeofenceRoundTrip posts a polygon and a circle, then checks that GET
// returns the same geometry and properties (GF-263, exact coordinates).
func TestGeofenceRoundTrip(t *testing.T) {
	v := newEnv(t)
	fleet := fixtureFleet(t, v.db, "round-trip")
	tok := v.token(fleet, roleAdmin)

	polyBody := polygon(`"name":"Depot yard","kind":"polygon"`, ccwSquare)
	circBody := circle(`"name":"Depot ring","kind":"circle","radius_m":500,"dwell_min":5,` +
		`"schedule":{"days":[1,2,3,4,5],"from":"08:00","to":"18:00","tz":"Europe/Istanbul"}`)
	for _, body := range []string{polyBody, circBody} {
		rec := v.do(http.MethodPost, "/geofences", tok, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST status = %d (%s), want 201", rec.Code, rec.Body.String())
		}
		var f Feature
		if err := json.Unmarshal(rec.Body.Bytes(), &f); err != nil {
			t.Fatalf("decode POST: %v", err)
		}
		if f.ID == 0 || len(f.BBox) != 4 {
			t.Fatalf("POST response missing id or bbox: %+v", f)
		}
	}

	got := v.listFeatures(tok)
	if len(got) != 2 {
		t.Fatalf("GET features = %d, want 2", len(got))
	}
	if got[0].ID >= got[1].ID {
		t.Fatalf("features not ordered by id ascending: %d, %d", got[0].ID, got[1].ID)
	}
	byName := map[string]Feature{}
	for _, f := range got {
		byName[f.Properties.Name] = f
	}

	poly := byName["Depot yard"]
	if poly.Properties.Kind != KindPolygon || poly.Properties.RadiusM != nil || poly.Properties.DwellMin != nil {
		t.Fatalf("polygon properties = %+v", poly.Properties)
	}
	if !reflect.DeepEqual(geometryOf(t, []byte(polyBody)), geometryOf(t, mustJSON(t, poly.Geometry))) {
		t.Fatalf("polygon geometry differs:\nposted %s\n   got %s", polyBody, poly.Geometry)
	}

	ring := byName["Depot ring"]
	if ring.Properties.Kind != KindCircle || ring.Properties.RadiusM == nil || *ring.Properties.RadiusM != 500 {
		t.Fatalf("circle properties = %+v", ring.Properties)
	}
	if ring.Properties.DwellMin == nil || *ring.Properties.DwellMin != 5 {
		t.Fatalf("circle dwell = %+v", ring.Properties.DwellMin)
	}
	if !reflect.DeepEqual(geometryOf(t, []byte(circBody)), geometryOf(t, mustJSON(t, ring.Geometry))) {
		t.Fatalf("circle geometry differs:\n   got %s", ring.Geometry)
	}
	s := ring.Properties.Schedule
	if s == nil || !reflect.DeepEqual(s.Days, []int{1, 2, 3, 4, 5}) || s.From != "08:00" || s.To != "18:00" || s.Tz != "Europe/Istanbul" {
		t.Fatalf("schedule = %+v", s)
	}
}

// TestGeofenceRejectsInvalidPolygon covers the GF-264 geometry rules. Each
// refusal is 400 validation_failed and stores nothing.
func TestGeofenceRejectsInvalidPolygon(t *testing.T) {
	v := newEnv(t)
	fleet := fixtureFleet(t, v.db, "invalid-polygon")
	tok := v.token(fleet, roleAdmin)

	// 1001 positions: 1000 distinct, then the closing copy of the first.
	var many strings.Builder
	many.WriteString("[")
	for i := 0; i < 1000; i++ {
		fmt.Fprintf(&many, "[13.%06d,52.0],", i)
	}
	many.WriteString("[13.000000,52.0]]")

	cases := map[string]string{
		"bowtie":            `[[0,0],[1,1],[1,0],[0,1],[0,0]]`,
		"spike":             `[[0,0],[2,0],[1,0],[1,1],[0,0]]`,
		"open ring":         `[[0,0],[1,0],[1,1],[0,1]]`,
		"three positions":   `[[0,0],[1,0],[0,0]]`,
		"hole":              `[[0,0],[4,0],[4,4],[0,4],[0,0]],[[1,1],[2,1],[2,2],[1,1]]`,
		"over 1000":         many.String(),
		"lng 200":           `[[200,0],[201,0],[201,1],[200,1],[200,0]]`,
		"lat 91":            `[[0,91],[1,91],[1,92],[0,92],[0,91]]`,
		"lng span over 180": `[[-100,0],[100,0],[100,1],[-100,1],[-100,0]]`,
	}
	before := v.count(fleet)
	for name, ring := range cases {
		t.Run(name, func(t *testing.T) {
			body := polygon(fmt.Sprintf(`"name":%q,"kind":"polygon"`, name), ring)
			expectError(t, v.do(http.MethodPost, "/geofences", tok, body), http.StatusBadRequest, "validation_failed")
		})
	}
	if after := v.count(fleet); after != before {
		t.Fatalf("stored rows changed on refusal: before %d, after %d", before, after)
	}
}

// TestGeofenceRejectsBadFields covers the GF-265 bounds and unknown keys.
// Each refusal is 400 and stores nothing.
func TestGeofenceRejectsBadFields(t *testing.T) {
	v := newEnv(t)
	fleet := fixtureFleet(t, v.db, "bad-fields")
	tok := v.token(fleet, roleAdmin)

	sched := func(days, from, to, tz string) string {
		return fmt.Sprintf(`"schedule":{"days":%s,"from":%q,"to":%q,"tz":%q}`, days, from, to, tz)
	}
	cases := map[string]string{
		"radius 0":              circle(`"name":"r0","kind":"circle","radius_m":0`),
		"radius 100001":         circle(`"name":"r100001","kind":"circle","radius_m":100001`),
		"radius on polygon":     polygon(`"name":"rp","kind":"polygon","radius_m":10`, ccwSquare),
		"circle without radius": circle(`"name":"nr","kind":"circle"`),
		"dwell 0":               polygon(`"name":"d0","kind":"polygon","dwell_min":0`, ccwSquare),
		"dwell 1441":            polygon(`"name":"d1441","kind":"polygon","dwell_min":1441`, ccwSquare),
		"tz Mars/Base":          circle(`"name":"tz","kind":"circle","radius_m":5,` + sched("[1]", "08:00", "17:00", "Mars/Base")),
		"from equals to":        circle(`"name":"eq","kind":"circle","radius_m":5,` + sched("[1]", "08:00", "08:00", "UTC")),
		"day 0":                 circle(`"name":"d0day","kind":"circle","radius_m":5,` + sched("[0]", "08:00", "17:00", "UTC")),
		"duplicate day":         circle(`"name":"dup","kind":"circle","radius_m":5,` + sched("[1,1]", "08:00", "17:00", "UTC")),
		"malformed HH:MM":       circle(`"name":"hhmm","kind":"circle","radius_m":5,` + sched("[1]", "8:00", "17:00", "UTC")),
		"unknown property":      polygon(`"name":"extra","kind":"polygon","colour":"red"`, ccwSquare),
		"kind mismatch":         polygon(`"name":"km","kind":"circle","radius_m":5`, ccwSquare),
		"unknown schedule key":  circle(`"name":"sk","kind":"circle","radius_m":5,"schedule":{"days":[1],"from":"08:00","to":"17:00","tz":"UTC","x":1}`),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			expectError(t, v.do(http.MethodPost, "/geofences", tok, body), http.StatusBadRequest, "validation_failed")
		})
	}
	if n := v.count(fleet); n != 0 {
		t.Fatalf("stored rows = %d after refusals, want 0", n)
	}
}

// TestGeofenceScheduleAccepted checks an overnight window (from > to) and
// stored canonical days: sorted, and returned in the same shape.
func TestGeofenceScheduleAccepted(t *testing.T) {
	v := newEnv(t)
	fleet := fixtureFleet(t, v.db, "schedule")
	tok := v.token(fleet, roleDispatcher)

	body := circle(`"name":"Night watch","kind":"circle","radius_m":250,` +
		`"schedule":{"days":[7,1,3],"from":"22:00","to":"06:00","tz":"Europe/Istanbul"}`)
	rec := v.do(http.MethodPost, "/geofences", tok, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST status = %d (%s), want 201", rec.Code, rec.Body.String())
	}
	got := v.listFeatures(tok)
	if len(got) != 1 || got[0].Properties.Schedule == nil {
		t.Fatalf("GET = %+v, want one fence with a schedule", got)
	}
	s := got[0].Properties.Schedule
	if !reflect.DeepEqual(s.Days, []int{1, 3, 7}) || s.From != "22:00" || s.To != "06:00" || s.Tz != "Europe/Istanbul" {
		t.Fatalf("schedule = %+v, want days [1 3 7] 22:00-06:00 Europe/Istanbul", s)
	}
}

// TestGeofenceNormalizesWinding posts a clockwise square and checks that the
// stored ring is counter-clockwise (GF-264).
func TestGeofenceNormalizesWinding(t *testing.T) {
	v := newEnv(t)
	fleet := fixtureFleet(t, v.db, "winding")
	tok := v.token(fleet, roleAdmin)

	cw := `[[13.0,52.0],[13.0,52.1],[13.1,52.1],[13.1,52.0],[13.0,52.0]]`
	rec := v.do(http.MethodPost, "/geofences", tok, polygon(`"name":"cw","kind":"polygon"`, cw))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST status = %d (%s), want 201", rec.Code, rec.Body.String())
	}
	got := v.listFeatures(tok)
	if len(got) != 1 {
		t.Fatalf("GET features = %d, want 1", len(got))
	}
	var g struct {
		Coordinates [][][]float64 `json:"coordinates"`
	}
	if err := json.Unmarshal(got[0].Geometry, &g); err != nil {
		t.Fatalf("decode ring: %v", err)
	}
	if area := signedArea(g.Coordinates[0]); area <= 0 {
		t.Fatalf("stored ring is clockwise (signed area %v), want counter-clockwise", area)
	}
}

// signedArea is the shoelace sum: positive for counter-clockwise rings.
func signedArea(ring [][]float64) float64 {
	var a float64
	for i := 0; i+1 < len(ring); i++ {
		a += ring[i][0]*ring[i+1][1] - ring[i+1][0]*ring[i][1]
	}
	return a / 2
}

// TestGeofenceNameUnique covers GF-265: names are trimmed and unique per
// fleet, ignoring case. The same name in another fleet is allowed.
func TestGeofenceNameUnique(t *testing.T) {
	v := newEnv(t)
	fleetA := fixtureFleet(t, v.db, "names-a")
	fleetB := fixtureFleet(t, v.db, "names-b")
	tokA := v.token(fleetA, roleAdmin)
	tokB := v.token(fleetB, roleAdmin)

	body := func(name string) string { return polygon(fmt.Sprintf(`"name":%q,"kind":"polygon"`, name), ccwSquare) }
	if rec := v.do(http.MethodPost, "/geofences", tokA, body("Depot")); rec.Code != http.StatusCreated {
		t.Fatalf("first POST status = %d (%s), want 201", rec.Code, rec.Body.String())
	}
	expectError(t, v.do(http.MethodPost, "/geofences", tokA, body("  depot ")), http.StatusConflict, "conflict")
	if rec := v.do(http.MethodPost, "/geofences", tokB, body("Depot")); rec.Code != http.StatusCreated {
		t.Fatalf("other fleet POST status = %d (%s), want 201", rec.Code, rec.Body.String())
	}
}

// TestGeofenceFleetScope covers GF-269: another fleet's fence is invisible,
// its delete is 404, and a malformed id is 404.
func TestGeofenceFleetScope(t *testing.T) {
	v := newEnv(t)
	fleetA := fixtureFleet(t, v.db, "scope-a")
	fleetB := fixtureFleet(t, v.db, "scope-b")
	tokA := v.token(fleetA, roleAdmin)
	tokB := v.token(fleetB, roleAdmin)

	rec := v.do(http.MethodPost, "/geofences", tokB, polygon(`"name":"B only","kind":"polygon"`, ccwSquare))
	if rec.Code != http.StatusCreated {
		t.Fatalf("B POST status = %d (%s), want 201", rec.Code, rec.Body.String())
	}
	var f Feature
	if err := json.Unmarshal(rec.Body.Bytes(), &f); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got := v.listFeatures(tokA); len(got) != 0 {
		t.Fatalf("fleet A sees %d fences, want 0", len(got))
	}
	expectError(t, v.do(http.MethodDelete, fmt.Sprintf("/geofences/%d", f.ID), tokA, ""), http.StatusNotFound, "not_found")
	expectError(t, v.do(http.MethodDelete, "/geofences/abc", tokA, ""), http.StatusNotFound, "not_found")
	if got := v.listFeatures(tokB); len(got) != 1 {
		t.Fatalf("fleet B lost its fence: %d left", len(got))
	}
}

// TestGeofenceDeleteBlockedByEvents covers GF-267: a fence with no events
// deletes (204), and a fence with an event row is refused (409) and kept.
func TestGeofenceDeleteBlockedByEvents(t *testing.T) {
	v := newEnv(t)
	fleet := fixtureFleet(t, v.db, "delete")
	tok := v.token(fleet, roleDispatcher)
	vehicle := fixtureVehicle(t, v.db, fleet, "DEL-0001")

	post := func(name string) Feature {
		rec := v.do(http.MethodPost, "/geofences", tok, polygon(fmt.Sprintf(`"name":%q,"kind":"polygon"`, name), ccwSquare))
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST %s status = %d (%s), want 201", name, rec.Code, rec.Body.String())
		}
		var f Feature
		if err := json.Unmarshal(rec.Body.Bytes(), &f); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return f
	}
	free := post("No events")
	used := post("With event")
	if _, err := v.db.Exec(`
		INSERT INTO geofence_events (fleet_id, geofence_id, vehicle_id, kind, occurred_at)
		VALUES ($1, $2, $3, 'enter', now())`, fleet, used.ID, vehicle); err != nil {
		t.Fatalf("insert fixture event: %v", err)
	}

	if rec := v.do(http.MethodDelete, fmt.Sprintf("/geofences/%d", free.ID), tok, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete free fence status = %d (%s), want 204", rec.Code, rec.Body.String())
	}
	expectError(t, v.do(http.MethodDelete, fmt.Sprintf("/geofences/%d", used.ID), tok, ""), http.StatusConflict, "conflict")
	if n := v.count(fleet); n != 1 {
		t.Fatalf("stored rows = %d, want 1 (the fence with events stays)", n)
	}
}

// mustJSON re-encodes a raw JSON value so it can be decoded by geometryOf.
func mustJSON(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	b, err := json.Marshal(struct {
		Geometry json.RawMessage `json:"geometry"`
	}{raw})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return b
}
