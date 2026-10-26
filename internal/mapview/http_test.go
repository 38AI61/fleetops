package mapview

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"fleetops/internal/auth"
	"fleetops/internal/testinfra"
)

// fakeJobs is a test double of our own JobLookup seam (GF-133). It is not a
// mock of infrastructure: it answers from a map the test fills in.
type fakeJobs struct {
	states    map[int64]JobState
	gotFleet  int64
	gotIDsLen int
}

func (f *fakeJobs) ActiveJobStates(_ context.Context, fleetID int64, ids []int64) (map[int64]JobState, error) {
	f.gotFleet = fleetID
	f.gotIDsLen = len(ids)
	out := map[int64]JobState{}
	for _, id := range ids {
		if s, ok := f.states[id]; ok {
			out[id] = s
		}
	}
	return out, nil
}

// TestMapVehiclesEnRouteFromLookup proves the JobLookup seam: an en_route job
// from the lookup yields en_route, and a stale vehicle is offline even mid-job (D-27).
func TestMapVehiclesEnRouteFromLookup(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	now := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	fleet := newFleet(t, db, "lookup")
	driving := seedVehicle(t, db, fleet, "DRV-1", 13.40, 52.52)
	idle := seedVehicle(t, db, fleet, "IDL-1", 13.41, 52.52)
	stale := seedVehicle(t, db, fleet, "STA-1", 13.42, 52.52)
	if _, err := db.Exec(
		`UPDATE vehicles_latest SET server_ts = server_ts - interval '10 minutes' WHERE vehicle_id = $1`, stale,
	); err != nil {
		t.Fatalf("age vehicle: %v", err)
	}

	jobs := &fakeJobs{states: map[int64]JobState{driving: JobEnRoute, stale: JobPickedUp}}
	iss, err := auth.NewIssuer([]byte(strings.Repeat("j", 32)), func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	tok, err := iss.Issue(7, fleet, "dispatcher")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	e := echo.New()
	h := &Handler{DB: db, Clock: func() time.Time { return now }, PresenceTTL: 120 * time.Second, Jobs: jobs}
	h.Register(e, auth.RequireAuth(iss))

	req := httptest.NewRequest(http.MethodGet, "/map/vehicles?bbox=13.30,52.45,13.50,52.60", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Vehicles []struct {
			VehicleID int64  `json:"vehicle_id"`
			Status    string `json:"status"`
		} `json:"vehicles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := map[int64]string{driving: "en_route", idle: "idle", stale: "offline"}
	if len(body.Vehicles) != len(want) {
		t.Fatalf("got %d vehicles, want %d: %s", len(body.Vehicles), len(want), rec.Body.String())
	}
	for _, v := range body.Vehicles {
		if want[v.VehicleID] != v.Status {
			t.Errorf("vehicle %d status = %s, want %s", v.VehicleID, v.Status, want[v.VehicleID])
		}
	}
	if jobs.gotFleet != fleet || jobs.gotIDsLen != 3 {
		t.Fatalf("lookup called with fleet %d and %d ids, want fleet %d and 3", jobs.gotFleet, jobs.gotIDsLen, fleet)
	}
}
