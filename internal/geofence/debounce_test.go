package geofence

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"pgregory.net/rapid"

	"fleetops/internal/testinfra"
)

// fenceSpec is one fence of a stream and its dwell_min (nil = never dwells).
type fenceSpec struct {
	id    int64
	dwell *int
}

// fixRow is one generated fix: the index of its vehicle, its server_ts, its
// position, and its message id.
type fixRow struct {
	vehicle  int
	ts       time.Time
	lng, lat float64
	msg      uuid.UUID
}

// storedEvent is one geofence_events row, read back in insert order.
type storedEvent struct {
	vehicle, fence int64
	kind           EventKind
	at             time.Time
}

// vehicleTS keys the fixes of one vehicle by server_ts in Unix nanoseconds. A
// time.Time key would compare its *Location too, and times read from Postgres
// carry a different one from the generated times.
type vehicleTS struct {
	vehicle int
	ts      int64
}

// qOracle is the ground truth: the exact predicate on every fence of the fleet,
// with no bbox prefilter, so it does not depend on the code under test.
const qOracle = `
SELECT f.n, g.id,
       CASE WHEN g.kind = 'polygon' THEN ST_Covers(g.geom, p.geog)
            ELSE ST_DWithin(g.geom, p.geog, g.radius_m) END
FROM unnest($1::bigint[], $2::float8[], $3::float8[]) AS f(n, lng, lat)
CROSS JOIN geofences g
CROSS JOIN LATERAL (SELECT ST_SetSRID(ST_MakePoint(f.lng, f.lat), 4326)::geography AS geog) p
WHERE g.fleet_id = $4`

// debounceRun pushes one stream through the evaluator in batches of the sizes
// nextBatch returns, then checks the stored events against the oracle. fatalf
// reports a violation, so one checker serves rapid and the fixed streams.
func debounceRun(db *sql.DB, fatalf func(string, ...any), fleet int64, vehicles []int64, fences []fenceSpec,
	fixes []fixRow, nextBatch func() int) {
	ctx := context.Background()
	slices.SortFunc(fixes, func(a, b fixRow) int {
		if c := a.ts.Compare(b.ts); c != 0 {
			return c
		}
		return bytes.Compare(a.msg[:], b.msg[:])
	})

	ev := NewEvaluator(db)
	for i := 0; i < len(fixes); {
		n := min(nextBatch(), len(fixes)-i)
		obs := make([]Observation, n)
		for j := range obs {
			f := fixes[i+j]
			obs[j] = Observation{VehicleID: vehicles[f.vehicle], FleetID: fleet, MsgID: f.msg,
				ServerTS: f.ts, Lng: f.lng, Lat: f.lat}
		}
		if _, err := ev.Evaluate(ctx, obs); err != nil {
			fatalf("evaluate batch at fix %d: %v", i, err)
			return
		}
		i += n
	}

	// truth[i][fence] is the exact result for fix i. A fence absent for a fix is outside.
	truth := map[int]map[int64]bool{}
	if len(fixes) > 0 {
		ns, lngs, lats := make(pq.Int64Array, len(fixes)), make(pq.Float64Array, len(fixes)), make(pq.Float64Array, len(fixes))
		for i, f := range fixes {
			ns[i], lngs[i], lats[i] = int64(i), f.lng, f.lat
		}
		rows, err := db.QueryContext(ctx, qOracle, ns, lngs, lats, fleet)
		if err != nil {
			fatalf("oracle: %v", err)
			return
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var n, fence int64
			var in bool
			if err := rows.Scan(&n, &fence, &in); err != nil {
				fatalf("scan oracle: %v", err)
				return
			}
			if truth[int(n)] == nil {
				truth[int(n)] = map[int64]bool{}
			}
			truth[int(n)][fence] = in
		}
		if err := rows.Err(); err != nil {
			fatalf("oracle rows: %v", err)
			return
		}
	}

	byVTS := map[vehicleTS][]int{}
	for i, f := range fixes {
		k := vehicleTS{f.vehicle, f.ts.UnixNano()}
		byVTS[k] = append(byVTS[k], i)
	}
	vehicleIndex := map[int64]int{}
	for i, v := range vehicles {
		vehicleIndex[v] = i
	}
	specs := map[int64]fenceSpec{}
	for _, f := range fences {
		specs[f.id] = f
	}

	events, ok := readEvents(fatalf, db, fleet)
	if !ok {
		return
	}
	pairs := map[[2]int64][]storedEvent{}
	var order [][2]int64
	for _, e := range events {
		k := [2]int64{e.vehicle, e.fence}
		if _, seen := pairs[k]; !seen {
			order = append(order, k)
		}
		pairs[k] = append(pairs[k], e)
	}
	for _, k := range order {
		if msg := checkPair(pairs[k], specs[k[1]], vehicleIndex[k[0]], byVTS, truth, k[1]); msg != "" {
			fatalf("pair (vehicle %d, fence %d): %s", k[0], k[1], msg)
			return
		}
	}
}

// readEvents returns the fleet's events in insert order. ok is false after a
// read error, which is reported through fatalf.
func readEvents(fatalf func(string, ...any), db *sql.DB, fleet int64) (out []storedEvent, ok bool) {
	rows, err := db.QueryContext(context.Background(), `
		SELECT vehicle_id, geofence_id, kind, occurred_at FROM geofence_events
		WHERE fleet_id = $1 ORDER BY id`, fleet)
	if err != nil {
		fatalf("read events: %v", err)
		return nil, false
	}
	defer func() { _ = rows.Close() }()
	out = []storedEvent{}
	for rows.Next() {
		var e storedEvent
		var kind string
		if err := rows.Scan(&e.vehicle, &e.fence, &kind, &e.at); err != nil {
			fatalf("scan event: %v", err)
			return nil, false
		}
		e.kind = EventKind(kind)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		fatalf("read events: %v", err)
		return nil, false
	}
	return out, true
}

// checkPair returns "" when one pair's events satisfy GF-282 and GF-283, or a
// message naming the first violation. vi is the vehicle index, fence is the fence id.
func checkPair(evs []storedEvent, spec fenceSpec, vi int, byVTS map[vehicleTS][]int,
	truth map[int]map[int64]bool, fence int64) string {
	// Debounce: consecutive emitted events of a pair are at least 30 s apart.
	for i := 1; i < len(evs); i++ {
		if gap := evs[i].at.Sub(evs[i-1].at); gap < debounceWindow {
			return fmt.Sprintf("events %d and %d are %v apart, want >= %v", i-1, i, gap, debounceWindow)
		}
	}

	// Oracle: an enter or dwell has a fix inside at its time, an exit a fix outside.
	// Fixes that share a server_ts are matched by any of them.
	for i, e := range evs {
		want := e.kind != EventExit
		matched := false
		for _, n := range byVTS[vehicleTS{vi, e.at.UnixNano()}] {
			if truth[n][fence] == want {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Sprintf("%s #%d at %v has no fix with truth inside=%v", e.kind, i, e.at, want)
		}
	}

	// Alternation: enter and exit alternate from enter. A dwell sits inside an
	// episode, at least dwell_min after its enter, and at most once per episode.
	expectEnter := true
	var enteredAt time.Time
	dwelled := false
	for i, e := range evs {
		switch e.kind {
		case EventEnter:
			if !expectEnter {
				return fmt.Sprintf("enter #%d follows an enter", i)
			}
			expectEnter, enteredAt, dwelled = false, e.at, false
		case EventExit:
			if expectEnter {
				return fmt.Sprintf("exit #%d without an open enter", i)
			}
			expectEnter = true
		case EventDwell:
			switch {
			case expectEnter:
				return fmt.Sprintf("dwell #%d outside an episode", i)
			case spec.dwell == nil:
				return fmt.Sprintf("dwell #%d on a fence with dwell_min NULL", i)
			case e.at.Before(enteredAt.Add(time.Duration(*spec.dwell) * time.Minute)):
				return fmt.Sprintf("dwell #%d at %v is before dwell_min after the enter at %v", i, e.at, enteredAt)
			case dwelled:
				return fmt.Sprintf("dwell #%d is a second dwell in one episode", i)
			}
			dwelled = true
		}
	}
	return ""
}

// newStreamFleet makes a fleet with one vehicle and returns both ids.
func newStreamFleet(t testing.TB, db *sql.DB, name string) (fleet, vehicle int64) {
	t.Helper()
	fleet = fixtureFleet(t, db, name)
	vehicle = fixtureVehicle(t, db, fleet, name+"-v")
	return fleet, vehicle
}

// squareFence is the 13.0..13.1 by 52.0..52.1 polygon of the fixed streams.
func squareFence(t testing.TB, db *sql.DB, fleet int64, dwell *int) fenceSpec {
	t.Helper()
	f, err := (&Store{DB: db}).Create(context.Background(), fleet, Input{
		Name: "square", Kind: KindPolygon,
		Ring:     [][2]float64{{13.0, 52.0}, {13.1, 52.0}, {13.1, 52.1}, {13.0, 52.1}, {13.0, 52.0}},
		DwellMin: dwell,
	})
	if err != nil {
		t.Fatalf("create fence: %v", err)
	}
	return fenceSpec{id: f.ID, dwell: dwell}
}

// msgID is a deterministic message id for the i-th fix of a stream.
func msgID(i int) uuid.UUID {
	var u uuid.UUID
	binary.BigEndian.PutUint64(u[8:], uint64(i))
	return u
}

// streamBase is the fixed origin of the fixed streams.
var streamBase = time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)

// streamFix is one fix of vehicle 0, sec seconds after streamBase, inside or
// outside the square.
func streamFix(i, sec int, inside bool) fixRow {
	lng := 12.5
	if inside {
		lng = 13.05
	}
	return fixRow{vehicle: 0, ts: streamBase.Add(time.Duration(sec) * time.Second), lng: lng, lat: 52.05, msg: msgID(i)}
}

// TestDebounceCrossingStream: one crossing gives one enter and one exit.
func TestDebounceCrossingStream(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	fleet, vehicle := newStreamFleet(t, db, "stream-cross")
	fence := squareFence(t, db, fleet, nil)
	fixes := []fixRow{streamFix(1, 0, false), streamFix(2, 60, true), streamFix(3, 120, true), streamFix(4, 200, false)}

	debounceRun(db, t.Fatalf, fleet, []int64{vehicle}, []fenceSpec{fence}, fixes, func() int { return 1 })
	assertEvents(t, db, fleet, []storedEvent{
		{vehicle, fence.id, EventEnter, streamBase.Add(60 * time.Second)},
		{vehicle, fence.id, EventExit, streamBase.Add(200 * time.Second)},
	})
}

// TestDebounceJitterBurstStream: flips every 5 s over 25 s give one enter. The
// exit comes only once the window has closed.
func TestDebounceJitterBurstStream(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	fleet, vehicle := newStreamFleet(t, db, "stream-jitter")
	fence := squareFence(t, db, fleet, nil)
	var fixes []fixRow
	for i := range 6 {
		fixes = append(fixes, streamFix(i+1, 5*i, i%2 == 0))
	}
	fixes = append(fixes, streamFix(7, 60, false))

	debounceRun(db, t.Fatalf, fleet, []int64{vehicle}, []fenceSpec{fence}, fixes, func() int { return 2 })
	assertEvents(t, db, fleet, []storedEvent{
		{vehicle, fence.id, EventEnter, streamBase},
		{vehicle, fence.id, EventExit, streamBase.Add(60 * time.Second)},
	})
}

// TestDebounceDwellStream: a stay of 2 min with dwell_min 1 fires dwell once, at
// the first fix at or after the minute. The exit follows.
func TestDebounceDwellStream(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	fleet, vehicle := newStreamFleet(t, db, "stream-dwell")
	one := 1
	fence := squareFence(t, db, fleet, &one)
	var fixes []fixRow
	for i := range 8 {
		fixes = append(fixes, streamFix(i+1, 20*i, true))
	}
	fixes = append(fixes, streamFix(9, 150, false))

	debounceRun(db, t.Fatalf, fleet, []int64{vehicle}, []fenceSpec{fence}, fixes, func() int { return 3 })
	assertEvents(t, db, fleet, []storedEvent{
		{vehicle, fence.id, EventEnter, streamBase},
		{vehicle, fence.id, EventDwell, streamBase.Add(60 * time.Second)},
		{vehicle, fence.id, EventExit, streamBase.Add(150 * time.Second)},
	})
}

// assertEvents compares the fleet's stored events with want, in insert order.
func assertEvents(t *testing.T, db *sql.DB, fleet int64, want []storedEvent) {
	t.Helper()
	got, ok := readEvents(t.Fatalf, db, fleet)
	if !ok {
		return
	}
	if len(got) != len(want) {
		t.Fatalf("events = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].vehicle != want[i].vehicle || got[i].fence != want[i].fence ||
			got[i].kind != want[i].kind || !got[i].at.Equal(want[i].at) {
			t.Fatalf("event %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestDebounceProperty is GF-282 and GF-283 on random streams. Each run draws
// 1 to 3 vehicles, 1 to 3 fences (polygons and circles near a fixed origin, with
// dwell_min NULL or 1 to 3), and 1 to 200 fixes per vehicle at server_ts gaps of
// 0 to 120 s, jittered across the boundaries. Batches are random sizes. The
// checker asserts the properties against the PostGIS oracle. rapid draws a fresh
// seed per run; a failure prints it for -rapid.seed=<n>, and that seed is then
// added as a fixed case.
func TestDebounceProperty(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	ctx := context.Background()
	origin := streamBase.Add(-3 * time.Hour)
	iter := 0

	rapid.Check(t, func(rt *rapid.T) {
		iter++
		var fleet int64
		if err := db.QueryRowContext(ctx, `INSERT INTO fleets (name) VALUES ($1) RETURNING id`,
			fmt.Sprintf("prop-%d-%d", iter, time.Now().UnixNano())).Scan(&fleet); err != nil {
			rt.Fatalf("fleet: %v", err)
		}

		nVeh := rapid.IntRange(1, 3).Draw(rt, "vehicles")
		vehicles := make([]int64, nVeh)
		for i := range vehicles {
			if err := db.QueryRowContext(ctx,
				`INSERT INTO vehicles (fleet_id, plate) VALUES ($1, $2) RETURNING id`,
				fleet, fmt.Sprintf("PR%d-%d", iter, i)).Scan(&vehicles[i]); err != nil {
				rt.Fatalf("vehicle: %v", err)
			}
		}

		nFence := rapid.IntRange(1, 3).Draw(rt, "fences")
		fences := make([]fenceSpec, nFence)
		for j := range fences {
			cLng := 13.05 + rapid.Float64Range(-0.01, 0.01).Draw(rt, "fence lng")
			cLat := 52.05 + rapid.Float64Range(-0.01, 0.01).Draw(rt, "fence lat")
			var dwell *int
			if d := rapid.IntRange(0, 3).Draw(rt, "dwell"); d > 0 {
				dwell = &d
			}
			in := Input{Name: fmt.Sprintf("p%d-%d", iter, j), DwellMin: dwell}
			if rapid.Bool().Draw(rt, "circle") {
				in.Kind, in.Centre = KindCircle, [2]float64{cLng, cLat}
				in.RadiusM = rapid.Float64Range(50, 2000).Draw(rt, "radius")
			} else {
				h := rapid.Float64Range(0.002, 0.01).Draw(rt, "half")
				in.Kind = KindPolygon
				in.Ring = [][2]float64{{cLng - h, cLat - h}, {cLng + h, cLat - h}, {cLng + h, cLat + h}, {cLng - h, cLat + h}, {cLng - h, cLat - h}}
			}
			f, err := (&Store{DB: db}).Create(ctx, fleet, in)
			if err != nil {
				rt.Fatalf("fence: %v", err)
			}
			fences[j] = fenceSpec{id: f.ID, dwell: dwell}
		}

		var fixes []fixRow
		for vi := range vehicles {
			n := rapid.IntRange(1, 200).Draw(rt, "fixes")
			ts := origin
			for range n {
				ts = ts.Add(time.Duration(rapid.IntRange(0, 120).Draw(rt, "gap")) * time.Second)
				fixes = append(fixes, fixRow{
					vehicle: vi, ts: ts,
					lng: 13.05 + rapid.Float64Range(-0.015, 0.015).Draw(rt, "lng"),
					lat: 52.05 + rapid.Float64Range(-0.015, 0.015).Draw(rt, "lat"),
					msg: msgID(len(fixes) + 1),
				})
			}
		}

		debounceRun(db, rt.Fatalf, fleet, vehicles, fences, fixes, func() int {
			return rapid.IntRange(1, 20).Draw(rt, "batch")
		})
	})
}
