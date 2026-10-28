package geofence

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"fleetops/internal/testinfra"
)

// obsAt is one fix of vehicle v in fleet f, stamped at ts.
func obsAt(v, f int64, ts time.Time, lng, lat float64) Observation {
	return Observation{VehicleID: v, FleetID: f, MsgID: uuid.New(), ServerTS: ts, Lng: lng, Lat: lat}
}

// candidatesOf runs the GF-284 candidate query for obs on a fresh evaluator.
func candidatesOf(t *testing.T, db *sql.DB, obs ...Observation) []hit {
	t.Helper()
	hits, err := NewEvaluator(db).candidates(context.Background(), obs)
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	return hits
}

// hitFor returns the hit for one fix and fence, and whether there is one.
func hitFor(hits []hit, msg uuid.UUID, fence int64) (hit, bool) {
	for _, h := range hits {
		if h.msgID == msg && h.fenceID == fence {
			return h, true
		}
	}
	return hit{}, false
}

// TestCandidatesBboxMissIsOutside: a fix outside the bbox returns no row, which
// the state machine reads as outside.
func TestCandidatesBboxMissIsOutside(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	fleet := fixtureFleet(t, db, "cand-miss")
	v := fixtureVehicle(t, db, fleet, "CM-1")
	fence := fixtureFence(t, db, fleet, "square")

	far := obsAt(v, fleet, base, 14.5, 50.5)
	near := obsAt(v, fleet, base, 13.05, 52.05)
	hits := candidatesOf(t, db, far, near)

	if _, ok := hitFor(hits, far.MsgID, fence); ok {
		t.Fatalf("bbox miss returned a row: %+v", hits)
	}
	h, ok := hitFor(hits, near.MsgID, fence)
	if !ok || !h.inside {
		t.Fatalf("interior fix hit = %+v ok=%v, want inside", h, ok)
	}
}

// TestCandidatesBoundaryIsInside: ST_Covers is boundary-inclusive (GF-284).
func TestCandidatesBoundaryIsInside(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	fleet := fixtureFleet(t, db, "cand-edge")
	v := fixtureVehicle(t, db, fleet, "CE-1")
	fence := fixtureFence(t, db, fleet, "square")

	// The fence is 13.0..13.1 by 52.0..52.1. Its edges are geodesics. The meridian
	// edges lie on the boundary, so their midpoints are on the edge. The parallel
	// at 52.0 is not a geodesic: the arc between its ends bows north, so a point on
	// the parallel is outside the geography polygon.
	edges := map[string]struct {
		p    [2]float64
		want bool
	}{
		"vertex":                     {[2]float64{13.0, 52.0}, true},
		"west meridian midpoint":     {[2]float64{13.0, 52.05}, true},
		"east meridian midpoint":     {[2]float64{13.1, 52.05}, true},
		"parallel 52.0 (geodesic)":   {[2]float64{13.05, 52.0}, false},
		"just outside west meridian": {[2]float64{12.99999, 52.05}, false},
	}
	for name, c := range edges {
		p := c.p
		o := obsAt(v, fleet, base, p[0], p[1])
		h, ok := hitFor(candidatesOf(t, db, o), o.MsgID, fence)
		if !c.want && !ok {
			continue // a bbox miss has no row, and no row is outside
		}
		if !ok {
			t.Fatalf("%s: no hit row for a point inside the bbox", name)
		}
		if h.inside != c.want {
			t.Fatalf("%s: inside = %v, want %v", name, h.inside, c.want)
		}
	}
}

// TestCandidatesCircleRadiusEdge puts a point at distance d from a centre and
// sets fences at radius d-1 and d+1. The exact distance comes from PostGIS, so
// the edge is exact with no tolerance.
func TestCandidatesCircleRadiusEdge(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	ctx := context.Background()
	fleet := fixtureFleet(t, db, "cand-circle")
	v := fixtureVehicle(t, db, fleet, "CC-1")
	s := &Store{DB: db}

	const cLng, cLat, pLng, pLat = 13.5, 52.5, 13.51, 52.5
	var d float64
	if err := db.QueryRowContext(ctx, `
		SELECT ST_Distance(ST_MakePoint($1, $2)::geography, ST_MakePoint($3, $4)::geography)`,
		cLng, cLat, pLng, pLat).Scan(&d); err != nil {
		t.Fatalf("distance: %v", err)
	}

	minus, err := s.Create(ctx, fleet, Input{Name: "circle minus", Kind: KindCircle, Centre: [2]float64{cLng, cLat}, RadiusM: d - 1})
	if err != nil {
		t.Fatalf("create minus: %v", err)
	}
	plus, err := s.Create(ctx, fleet, Input{Name: "circle plus", Kind: KindCircle, Centre: [2]float64{cLng, cLat}, RadiusM: d + 1})
	if err != nil {
		t.Fatalf("create plus: %v", err)
	}

	o := obsAt(v, fleet, base, pLng, pLat)
	hits := candidatesOf(t, db, o)
	h, ok := hitFor(hits, o.MsgID, minus.ID)
	if !ok || h.inside {
		t.Fatalf("radius d-1: hit=%+v ok=%v, want a bbox hit that is outside", h, ok)
	}
	h, ok = hitFor(hits, o.MsgID, plus.ID)
	if !ok || !h.inside {
		t.Fatalf("radius d+1: hit=%+v ok=%v, want inside", h, ok)
	}
}

// TestCandidatesFleetScope: a fence in another fleet is never returned for a
// vehicle of this fleet, even when the fix sits inside it.
func TestCandidatesFleetScope(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	mine := fixtureFleet(t, db, "scope-mine")
	other := fixtureFleet(t, db, "scope-other")
	v := fixtureVehicle(t, db, mine, "SC-1")
	foreign := fixtureFence(t, db, other, "foreign square")

	o := obsAt(v, mine, base, 13.05, 52.05)
	if _, ok := hitFor(candidatesOf(t, db, o), o.MsgID, foreign); ok {
		t.Fatal("fence of another fleet was returned")
	}
}

// explainLines runs an EXPLAIN statement on tx and joins the plan lines.
func explainLines(t *testing.T, tx *sql.Tx, stmt string) string {
	t.Helper()
	rows, err := tx.Query(stmt)
	if err != nil {
		t.Fatalf("explain %q: %v", stmt, err)
	}
	defer func() { _ = rows.Close() }()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		plan.WriteString(line + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("plan rows: %v", err)
	}
	return plan.String()
}

// TestCandidatePlanUsesBboxIndex is the GF-270 technique for the GF-284 statement.
// PREPARE with a generic parameter list, then EXPLAIN EXECUTE with seqscan off.
func TestCandidatePlanUsesBboxIndex(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	ctx := context.Background()
	fleet := fixtureFleet(t, db, "plan-cand")
	v := fixtureVehicle(t, db, fleet, "PC-1")
	for i := 0; i < 20; i++ {
		fixtureFence(t, db, fleet, fmt.Sprintf("plan fence %02d", i))
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatalf("set enable_seqscan: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `PREPARE cand (bigint[], text[], float8[], float8[], text[]) AS `+qCandidates); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	plan := explainLines(t, tx, fmt.Sprintf(
		`EXPLAIN EXECUTE cand('{%d}', '{%s}', '{13.05}', '{52.05}', '{%s}')`,
		v, uuid.New(), base.Format(time.RFC3339Nano)))
	if !strings.Contains(plan, "geofences_bbox_gix") {
		t.Fatalf("candidate plan does not use geofences_bbox_gix:\n%s", plan)
	}
}

// TestRebuildPlanUsesFleetAtIndex is the GF-280 stop rule: the rebuild must be
// served by geofence_events_fleet_at_idx, or the phase stops for a migration.
func TestRebuildPlanUsesFleetAtIndex(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	ctx := context.Background()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatalf("set enable_seqscan: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `PREPARE rb (bigint, bigint) AS `+qRebuild); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	plan := explainLines(t, tx, `EXPLAIN EXECUTE rb(1, 1)`)
	if !strings.Contains(plan, "geofence_events_fleet_at_idx") {
		t.Fatalf("rebuild plan does not use geofence_events_fleet_at_idx (GF-280 stop rule):\n%s", plan)
	}
}

// countEvents counts the events of one kind for one vehicle.
func countEvents(t *testing.T, db *sql.DB, vehicle int64, kind EventKind) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM geofence_events WHERE vehicle_id = $1 AND kind = $2`, vehicle, kind).Scan(&n); err != nil {
		t.Fatalf("count events: %v", err)
	}
	return n
}

// TestRebuildRestartNoDuplicateEnter: a restart inside the lookback rebuilds the
// pair as inside, so a fix still inside emits nothing.
func TestRebuildRestartNoDuplicateEnter(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	ctx := context.Background()
	fleet := fixtureFleet(t, db, "rebuild-restart")
	v := fixtureVehicle(t, db, fleet, "RR-1")
	fixtureFence(t, db, fleet, "square")
	t0 := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)

	first := NewEvaluator(db)
	res, err := first.Evaluate(ctx, []Observation{obsAt(v, fleet, t0, 13.05, 52.05)})
	if err != nil || res.Emitted[EventEnter] != 1 {
		t.Fatalf("first evaluate: res=%+v err=%v, want one enter", res, err)
	}

	restarted := NewEvaluator(db)
	res, err = restarted.Evaluate(ctx, []Observation{obsAt(v, fleet, t0.Add(5*time.Minute), 13.06, 52.06)})
	if err != nil {
		t.Fatalf("restarted evaluate: %v", err)
	}
	if n := res.Emitted[EventEnter]; n != 0 {
		t.Fatalf("restart emitted %d enter, want 0", n)
	}
	if got := countEvents(t, db, v, EventEnter); got != 1 {
		t.Fatalf("enter rows = %d, want 1", got)
	}
}

// TestRebuildLookbackResidualEmitsNewEnter documents the GF-280 residual: an enter
// older than 24 h is outside the rebuild window, so a vehicle still inside gets
// one new enter.
func TestRebuildLookbackResidualEmitsNewEnter(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	ctx := context.Background()
	fleet := fixtureFleet(t, db, "rebuild-lookback")
	v := fixtureVehicle(t, db, fleet, "RL-1")
	fence := fixtureFence(t, db, fleet, "square")

	old := time.Now().UTC().Add(-25 * time.Hour).Truncate(time.Second)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO geofence_events (fleet_id, geofence_id, vehicle_id, kind, occurred_at)
		VALUES ($1, $2, $3, 'enter', $4)`, fleet, fence, v, old); err != nil {
		t.Fatalf("seed old enter: %v", err)
	}

	res, err := NewEvaluator(db).Evaluate(ctx, []Observation{
		obsAt(v, fleet, time.Now().UTC().Add(-time.Hour).Truncate(time.Second), 13.05, 52.05),
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if n := res.Emitted[EventEnter]; n != 1 {
		t.Fatalf("enter after lookback = %d, want 1 (accepted GF-280 residual)", n)
	}
}
