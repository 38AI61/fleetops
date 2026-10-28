package geofence

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Stage names the step of an evaluation that failed. The ingest hook counts
// failures by stage (GF-285).
type Stage string

const (
	StageCandidates Stage = "candidates"
	StageState      Stage = "state"
	StageInsert     Stage = "insert"
)

// StageError is an evaluation failure tagged with its stage.
type StageError struct {
	Stage Stage
	Err   error
}

func (e *StageError) Error() string { return fmt.Sprintf("geofence %s: %v", e.Stage, e.Err) }
func (e *StageError) Unwrap() error { return e.Err }

// Observation is one fix stored by a flush (GF-278). It is the evaluator input.
type Observation struct {
	VehicleID int64
	FleetID   int64
	MsgID     uuid.UUID
	ServerTS  time.Time
	Lng, Lat  float64
}

// Result reports one evaluation. Counts are set only when the events committed.
// Failed is empty on success and names the failed stage otherwise.
type Result struct {
	Emitted   map[EventKind]int
	Debounced int
	Failed    Stage
	Duration  time.Duration
}

// pairKey identifies a vehicle-fence pair.
type pairKey struct {
	vehicle, fence int64
}

// Evaluator turns stored fixes into geofence events (GF-278 to GF-284). Its pair
// state is in memory and owned by one goroutine: the ingest consume loop (GF-280).
type Evaluator struct {
	db     *sql.DB
	loaded map[int64]bool
	pairs  map[pairKey]pairState
}

// NewEvaluator returns an evaluator with no pair state. The first flush that
// stores a fix for a vehicle rebuilds that vehicle's state from geofence_events.
func NewEvaluator(db *sql.DB) *Evaluator {
	return &Evaluator{db: db, loaded: map[int64]bool{}, pairs: map[pairKey]pairState{}}
}

// hit is one bbox prefilter match from qCandidates. inside is the exact result.
type hit struct {
	vehicleID int64
	msgID     uuid.UUID
	fenceID   int64
	dwellMin  *int
	inside    bool
}

// fixFence keys the exact result of one fix against one fence.
type fixFence struct {
	msg   uuid.UUID
	fence int64
}

// event is one row to write, with the fleet and vehicle it belongs to.
type event struct {
	fleet, fence, vehicle int64
	kind                  EventKind
	at                    time.Time
}

// Evaluate runs one flush's observations through the state machine. obs must be
// in (server_ts, msg_id) order. Nothing in the evaluator changes unless every
// stage succeeds, so a failed pair keeps its old state (GF-279).
func (e *Evaluator) Evaluate(ctx context.Context, obs []Observation) (res Result, err error) {
	start := time.Now()
	res.Emitted = map[EventKind]int{}
	defer func() { res.Duration = time.Since(start) }()
	if len(obs) == 0 {
		return res, nil
	}
	fail := func(stage Stage, err error) (Result, error) {
		res.Failed = stage
		res.Emitted = map[EventKind]int{}
		return res, &StageError{Stage: stage, Err: err}
	}

	hits, err := e.candidates(ctx, obs)
	if err != nil {
		return fail(StageCandidates, err)
	}

	// Group the fixes by vehicle, keeping their (server_ts, msg_id) order.
	byVehicle := map[int64][]Observation{}
	var vehicles []int64
	for _, o := range obs {
		if _, ok := byVehicle[o.VehicleID]; !ok {
			vehicles = append(vehicles, o.VehicleID)
		}
		byVehicle[o.VehicleID] = append(byVehicle[o.VehicleID], o)
	}

	// truth holds the exact result for each bbox hit. A missing key is outside:
	// the bbox is a superset of every fence (GF-284).
	truth := map[fixFence]bool{}
	dwell := map[int64]*int{}
	hitFences := map[int64][]int64{}
	for _, h := range hits {
		truth[fixFence{h.msgID, h.fenceID}] = h.inside
		dwell[h.fenceID] = h.dwellMin
		if !slices.Contains(hitFences[h.vehicleID], h.fenceID) {
			hitFences[h.vehicleID] = append(hitFences[h.vehicleID], h.fenceID)
		}
	}

	// staged holds every pair state this evaluation touches. It is installed
	// only after the events commit.
	staged := map[pairKey]pairState{}
	cur := func(k pairKey) pairState {
		if st, ok := staged[k]; ok {
			return st
		}
		return e.pairs[k]
	}

	var newlyLoaded []int64
	for _, v := range vehicles {
		if e.loaded[v] {
			continue
		}
		rebuilt, err := e.rebuild(ctx, byVehicle[v][0].FleetID, v)
		if err != nil {
			return fail(StageState, err)
		}
		maps.Copy(staged, rebuilt)
		newlyLoaded = append(newlyLoaded, v)
	}

	// insideBy lists the fences each vehicle is inside, so a fix that falls
	// outside them can close the episode even when no bbox hit names them.
	insideBy := map[int64][]int64{}
	for _, m := range []map[pairKey]pairState{e.pairs, staged} {
		for k := range m {
			if cur(k).inside && !slices.Contains(insideBy[k.vehicle], k.fence) {
				insideBy[k.vehicle] = append(insideBy[k.vehicle], k.fence)
			}
		}
	}

	var events []event
	suppressed := 0
	for _, v := range vehicles {
		fences := slices.Concat(hitFences[v], insideBy[v])
		slices.Sort(fences)
		fences = slices.Compact(fences)
		for _, f := range fences {
			k := pairKey{v, f}
			st := cur(k)
			for _, o := range byVehicle[v] {
				next, out, sup := advance(st, o.ServerTS, truth[fixFence{o.MsgID, f}], dwell[f])
				st = next
				suppressed += sup
				for _, em := range out {
					events = append(events, event{o.FleetID, f, v, em.kind, em.at})
				}
			}
			staged[k] = st
		}
	}

	if len(events) > 0 {
		if err := e.insertEvents(ctx, events); err != nil {
			return fail(StageInsert, err)
		}
	}

	// Commit point: the events are durable, so the staged state becomes the truth.
	maps.Copy(e.pairs, staged)
	for _, v := range newlyLoaded {
		e.loaded[v] = true
	}
	for _, ev := range events {
		res.Emitted[ev.kind]++
	}
	res.Debounced = suppressed
	return res, nil
}

// candidates runs qCandidates for the observed fixes (GF-284).
func (e *Evaluator) candidates(ctx context.Context, obs []Observation) ([]hit, error) {
	n := len(obs)
	vehicles, msgs := make(pq.Int64Array, n), make(pq.StringArray, n)
	lngs, lats := make(pq.Float64Array, n), make(pq.Float64Array, n)
	ts := make(pq.StringArray, n)
	for i, o := range obs {
		vehicles[i] = o.VehicleID
		msgs[i] = o.MsgID.String()
		lngs[i], lats[i] = o.Lng, o.Lat
		ts[i] = o.ServerTS.UTC().Format(time.RFC3339Nano)
	}
	rows, err := e.db.QueryContext(ctx, qCandidates, vehicles, msgs, lngs, lats, ts)
	if err != nil {
		return nil, fmt.Errorf("candidates: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []hit
	for rows.Next() {
		var h hit
		var msg string
		var dwell sql.NullInt64
		if err := rows.Scan(&h.vehicleID, &msg, &h.fenceID, &dwell, &h.inside); err != nil {
			return nil, fmt.Errorf("scan candidates: %w", err)
		}
		if h.msgID, err = uuid.Parse(msg); err != nil {
			return nil, fmt.Errorf("candidates msg_id: %w", err)
		}
		if dwell.Valid {
			d := int(dwell.Int64)
			h.dwellMin = &d
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("candidates: %w", err)
	}
	return out, nil
}

// rebuild loads one vehicle's pair state from the last 24 h of its events (GF-280).
// A pair with no rows in the window stays unknown, which is outside.
func (e *Evaluator) rebuild(ctx context.Context, fleetID, vehicleID int64) (map[pairKey]pairState, error) {
	rows, err := e.db.QueryContext(ctx, qRebuild, fleetID, vehicleID)
	if err != nil {
		return nil, fmt.Errorf("rebuild: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[pairKey]pairState{}
	for rows.Next() {
		var fence int64
		var kind string
		var at time.Time
		if err := rows.Scan(&fence, &kind, &at); err != nil {
			return nil, fmt.Errorf("scan rebuild: %w", err)
		}
		k := pairKey{vehicleID, fence}
		out[k] = replay(out[k], EventKind(kind), at)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rebuild: %w", err)
	}
	return out, nil
}

// insertEvents appends the events in one transaction (GF-279). A failure rolls back.
func (e *Evaluator) insertEvents(ctx context.Context, events []event) error {
	n := len(events)
	fleets, fences, vehicles := make(pq.Int64Array, n), make(pq.Int64Array, n), make(pq.Int64Array, n)
	kinds, ats := make(pq.StringArray, n), make(pq.StringArray, n)
	for i, ev := range events {
		fleets[i], fences[i], vehicles[i] = ev.fleet, ev.fence, ev.vehicle
		kinds[i] = string(ev.kind)
		ats[i] = ev.at.UTC().Format(time.RFC3339Nano)
	}
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin events: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, qInsertEvents, fleets, fences, vehicles, kinds, ats); err != nil {
		return fmt.Errorf("insert events: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit events: %w", err)
	}
	return nil
}
