package reports

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"fleetops/internal/auth"
)

// testSecret is 32 bytes, the minimum (D-03). It is never a real secret.
var testSecret = []byte(strings.Repeat("r", 32))

// apiEnv is the report route on an echo instance over the golden fixture, rolled
// up for 2026-03-08. Requests go through ServeHTTP, so no port is bound.
type apiEnv struct {
	t      *testing.T
	e      *echo.Echo
	f      *fx
	iss    *auth.Issuer
	fleet  int64
	driver map[string]int64
}

func newAPIEnv(t *testing.T) *apiEnv {
	t.Helper()
	f := newFx(t)
	fleet, drv := seedGolden(f)
	f.rollup(fleet, goldenLoc, goldenDay)
	iss, err := auth.NewIssuer(testSecret, time.Now)
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	e := echo.New()
	e.HideBanner = true
	// The clock reads 08:00 on the 8th in New York, so the default date is 2026-03-08.
	(&Handler{Store: &Store{DB: f.db}, Clock: fixed(at(12, 0, 0))}).Register(e, auth.RequireAuth(iss))
	return &apiEnv{t: t, e: e, f: f, iss: iss, fleet: fleet, driver: drv}
}

// user inserts a user in the fleet, linked to driver when driver is non-nil,
// and returns a bearer token.
func (v *apiEnv) user(fleet int64, role string, driver *int64) string {
	v.t.Helper()
	var drv any
	if driver != nil {
		drv = *driver
	}
	var uid int64
	email := fmt.Sprintf("%s-%d@reports.example", role, time.Now().UnixNano())
	if err := v.f.db.QueryRow(
		`INSERT INTO users (fleet_id, email, password_hash, role, driver_id) VALUES ($1, $2, 'x', $3, $4) RETURNING id`,
		fleet, email, role, drv,
	).Scan(&uid); err != nil {
		v.t.Fatalf("insert user: %v", err)
	}
	tok, err := v.iss.Issue(uid, fleet, role)
	if err != nil {
		v.t.Fatalf("Issue: %v", err)
	}
	return tok
}

func (v *apiEnv) get(tok, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if tok != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+tok)
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

// daily decodes a JSON 200 body.
func daily(t *testing.T, rec *httptest.ResponseRecorder) dailyBody {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body %s, want 200", rec.Code, rec.Body.String())
	}
	var b dailyBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return b
}

// driverIDs lists the driver ids of the rows, in order.
func driverIDs(rows []Row) []int64 {
	out := make([]int64, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.DriverID)
	}
	return out
}

// TestReportsRoleMatrix covers check (k): 4 roles times default, own driver,
// other driver, cross-fleet driver and CSV, plus anonymous 401 (GF-324).
func TestReportsRoleMatrix(t *testing.T) {
	v := newAPIEnv(t)
	d1, d3 := v.driver["d1"], v.driver["d3"]
	other := newFleet(t, v)
	stranger := v.f.driver(other, "Zed")
	const day = "/reports/daily?date=2026-03-08"

	for _, role := range []string{roleAdmin, roleManager, roleDispatcher} {
		tok := v.user(v.fleet, role, nil)
		if got := driverIDs(daily(t, v.get(tok, day)).Rows); !reflect.DeepEqual(got, []int64{d1, d3}) {
			t.Fatalf("%s default rows = %v, want both drivers", role, got)
		}
		if got := driverIDs(daily(t, v.get(tok, day+"&driver="+i64(d1))).Rows); !reflect.DeepEqual(got, []int64{d1}) {
			t.Fatalf("%s own-driver filter = %v", role, got)
		}
		if got := driverIDs(daily(t, v.get(tok, day+"&driver="+i64(d3))).Rows); !reflect.DeepEqual(got, []int64{d3}) {
			t.Fatalf("%s other-driver filter = %v", role, got)
		}
		if rec := v.get(tok, day+"&driver="+i64(stranger)); rec.Code != http.StatusNotFound || errCode(t, rec) != "not_found" {
			t.Fatalf("%s cross-fleet driver: %d %s, want 404 not_found", role, rec.Code, rec.Body.String())
		}
		if rec := v.get(tok, day+"&driver=999999"); rec.Code != http.StatusNotFound {
			t.Fatalf("%s unknown driver: %d, want 404", role, rec.Code)
		}
		if rec := v.get(tok, day+"&format=csv"); rec.Code != http.StatusOK || strings.Count(rec.Body.String(), "\r\n") != 3 {
			t.Fatalf("%s csv: %d %q, want 200 with header and two rows", role, rec.Code, rec.Body.String())
		}
	}

	own := v.user(v.fleet, roleDriver, &d1)
	if got := driverIDs(daily(t, v.get(own, day)).Rows); !reflect.DeepEqual(got, []int64{d1}) {
		t.Fatalf("driver default rows = %v, want own row only", got)
	}
	if got := driverIDs(daily(t, v.get(own, day+"&driver="+i64(d1))).Rows); !reflect.DeepEqual(got, []int64{d1}) {
		t.Fatalf("driver own filter = %v", got)
	}
	if rec := v.get(own, day+"&driver="+i64(d3)); rec.Code != http.StatusNotFound {
		t.Fatalf("driver other-driver: %d, want 404", rec.Code)
	}
	if rec := v.get(own, day+"&driver="+i64(stranger)); rec.Code != http.StatusNotFound {
		t.Fatalf("driver cross-fleet: %d, want 404", rec.Code)
	}
	if rec := v.get(own, day+"&format=csv"); rec.Code != http.StatusOK || strings.Count(rec.Body.String(), "\r\n") != 2 {
		t.Fatalf("driver csv: %d %q, want header and own row", rec.Code, rec.Body.String())
	}

	unlinked := v.user(v.fleet, roleDriver, nil)
	if body := daily(t, v.get(unlinked, day)); body.Rows == nil || len(body.Rows) != 0 {
		t.Fatalf("unlinked driver rows = %v, want an empty list", body.Rows)
	}
	if rec := v.get(unlinked, day+"&driver="+i64(d1)); rec.Code != http.StatusNotFound {
		t.Fatalf("unlinked driver other filter: %d, want 404", rec.Code)
	}

	if rec := v.get("", day); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d, want 401", rec.Code)
	}
}

// TestReportsBadParams: bad date, driver or format is 400 (GF-324).
func TestReportsBadParams(t *testing.T) {
	v := newAPIEnv(t)
	tok := v.user(v.fleet, roleAdmin, nil)
	for _, path := range []string{
		"/reports/daily?date=2026-13-01",
		"/reports/daily?date=2026-3-8",
		"/reports/daily?date=yesterday",
		"/reports/daily?driver=abc",
		"/reports/daily?format=xml",
	} {
		rec := v.get(tok, path)
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
			t.Fatalf("%s: %d %s, want 400 validation_failed", path, rec.Code, rec.Body.String())
		}
	}
}

// TestReportsDefaultDateIsFleetLocal: a missing date is the fleet-local today (GF-324).
func TestReportsDefaultDateIsFleetLocal(t *testing.T) {
	v := newAPIEnv(t)
	tok := v.user(v.fleet, roleAdmin, nil)
	body := daily(t, v.get(tok, "/reports/daily"))
	if body.Date != "2026-03-08" || len(body.Rows) != 2 {
		t.Fatalf("default = %+v, want 2026-03-08 with two rows", body)
	}
}

// TestReportsJSONShape checks the JSON row values, the rounded on-time percentage and the sort order.
func TestReportsJSONShape(t *testing.T) {
	v := newAPIEnv(t)
	tok := v.user(v.fleet, roleAdmin, nil)
	body := daily(t, v.get(tok, "/reports/daily?date=2026-03-08"))
	if len(body.Rows) != 2 || body.Rows[0].DriverID != v.driver["d1"] {
		t.Fatalf("rows = %+v, want D1 then D3", body.Rows)
	}
	d1 := body.Rows[0]
	if d1.JobsCompleted != 4 || d1.OnTimeCount != 1 || d1.OnTimeDenom != 2 || d1.IdleS != 180 {
		t.Fatalf("D1 = %+v", d1)
	}
	if d1.OnTimePct == nil || *d1.OnTimePct != 50 {
		t.Fatalf("D1 on_time_pct = %v, want 50", d1.OnTimePct)
	}
	if d3 := body.Rows[1]; d3.OnTimePct == nil || *d3.OnTimePct != 100 {
		t.Fatalf("D3 on_time_pct = %v, want 100", d3.OnTimePct)
	}
}

// TestReportsNullOnTimePct: a driver with no windowed deliveries has a null percentage.
func TestReportsNullOnTimePct(t *testing.T) {
	v := newAPIEnv(t)
	when := time.Date(2026, time.March, 8, 18, 0, 0, 0, time.UTC)
	dee := v.driver["dee"]
	v.f.job(v.fleet, "delivered", v.f.vehicle(v.fleet), dee, nil, &when)
	v.f.rollup(v.fleet, goldenLoc, goldenDay)
	tok := v.user(v.fleet, roleAdmin, nil)
	rec := v.get(tok, "/reports/daily?date=2026-03-08&driver="+i64(dee))
	if !strings.Contains(rec.Body.String(), `"on_time_pct":null`) {
		t.Fatalf("body = %s, want on_time_pct null", rec.Body.String())
	}
}

// TestReportsCSVHeaderAndRoundTrip: the header is byte-for-byte (GF-327), the
// content type and disposition are set, and parsing the body with encoding/csv
// gives back the JSON rows. The driver name has a comma and quotes.
func TestReportsCSVHeaderAndRoundTrip(t *testing.T) {
	v := newAPIEnv(t)
	tok := v.user(v.fleet, roleAdmin, nil)
	rec := v.get(tok, "/reports/daily?date=2026-03-08&format=csv")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/csv; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != `attachment; filename="daily-2026-03-08.csv"` {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	body := rec.Body.String()
	const header = "date,driver_id,driver_name,jobs_completed,on_time_count,on_time_denom,on_time_pct,distance_m,idle_s\r\n"
	if !strings.HasPrefix(body, header) {
		t.Fatalf("body does not start with the exact header:\n%q", body)
	}

	records, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("records = %d, want header plus two rows", len(records))
	}
	jsonRows := daily(t, v.get(tok, "/reports/daily?date=2026-03-08")).Rows
	for i, row := range jsonRows {
		rec := records[i+1]
		pct := ""
		if row.OnTimePct != nil {
			pct = strconv.FormatFloat(*row.OnTimePct, 'f', -1, 64)
		}
		dist, err := strconv.ParseFloat(rec[7], 64)
		if err != nil {
			t.Fatalf("distance %q: %v", rec[7], err)
		}
		got := []string{rec[0], rec[1], rec[2], rec[3], rec[4], rec[5], rec[6], rec[8]}
		want := []string{"2026-03-08", i64(row.DriverID), row.DriverName, i64(int64(row.JobsCompleted)),
			i64(int64(row.OnTimeCount)), i64(int64(row.OnTimeDenom)), pct, i64(int64(row.IdleS))}
		if !reflect.DeepEqual(got, want) || dist != row.DistanceM {
			t.Fatalf("csv row %d = %v dist %v, want %v dist %v", i, got, dist, want, row.DistanceM)
		}
	}
	if records[2][2] != `Cy, "the" jobs-only` {
		t.Fatalf("quoted name = %q", records[2][2])
	}
}

// TestReportsCSVEmptyDay: an empty day exports the header only, and JSON gives [].
func TestReportsCSVEmptyDay(t *testing.T) {
	v := newAPIEnv(t)
	tok := v.user(v.fleet, roleAdmin, nil)
	rec := v.get(tok, "/reports/daily?date=2026-03-09&format=csv")
	const header = "date,driver_id,driver_name,jobs_completed,on_time_count,on_time_denom,on_time_pct,distance_m,idle_s\r\n"
	if rec.Code != http.StatusOK || rec.Body.String() != header {
		t.Fatalf("empty csv = %d %q, want header only", rec.Code, rec.Body.String())
	}
	if body := v.get(tok, "/reports/daily?date=2026-03-09").Body.String(); !strings.Contains(body, `"rows":[]`) {
		t.Fatalf("empty json = %s, want rows []", body)
	}
}

func i64(v int64) string { return strconv.FormatInt(v, 10) }

// newFleet inserts a second fleet, for the cross-fleet cases.
func newFleet(t *testing.T, v *apiEnv) int64 {
	t.Helper()
	return v.f.fleet("other-fleet", "UTC")
}
