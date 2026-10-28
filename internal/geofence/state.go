package geofence

import "time"

// EventKind is the kind of a geofence_events row (D-33). P18 writes only these three.
type EventKind string

const (
	EventEnter EventKind = "enter"
	EventExit  EventKind = "exit"
	EventDwell EventKind = "dwell"
)

// debounceWindow is the minimum gap between emitted events of one pair (GF-282).
// It is anchored at the last emitted event, not at the first candidate.
const debounceWindow = 30 * time.Second

// pairState is one vehicle-fence pair (GF-280, GF-281). The zero value is the
// unknown start, which is outside.
type pairState struct {
	inside       bool      // emitted state; tracks recorded events, not raw geometry
	enteredAt    time.Time // occurred_at of the enter that opened the inside episode
	lastEmit     time.Time // occurred_at of the last emitted event; zero if none
	lastObs      time.Time // server_ts of the newest fix the pair has seen; zero if none
	dwellEmitted bool      // a dwell has fired in the current inside episode
}

// emit is one event the state machine wants written.
type emit struct {
	kind EventKind
	at   time.Time
}

// clear reports whether the debounce window allows an emission at at (GF-282).
func (st pairState) clear(at time.Time) bool {
	return st.lastEmit.IsZero() || !at.Before(st.lastEmit.Add(debounceWindow))
}

// advance applies one observation to a pair (GF-281 to GF-283). It is pure: the
// caller keeps the returned state only after the events are written. suppressed
// counts enter and exit candidates held back by the debounce. A suppressed
// candidate leaves the state alone, so the next fix re-tests it.
func advance(st pairState, at time.Time, inside bool, dwellMin *int) (next pairState, out []emit, suppressed int) {
	if !st.lastObs.IsZero() && at.Before(st.lastObs) {
		return st, nil, 0
	}
	st.lastObs = at

	switch {
	case inside && !st.inside:
		if st.clear(at) {
			st.inside, st.enteredAt, st.lastEmit, st.dwellEmitted = true, at, at, false
			out = append(out, emit{EventEnter, at})
		} else {
			suppressed++
		}
	case !inside && st.inside:
		if st.clear(at) {
			st.inside, st.lastEmit, st.dwellEmitted = false, at, false
			out = append(out, emit{EventExit, at})
		} else {
			suppressed++
		}
	}

	// Dwell needs a truth inside at this fix (no sweeper, D-33). Its clock is the
	// fix server_ts, so a vehicle that stops reporting does not dwell until its next fix.
	if inside && st.inside && dwellMin != nil && !st.dwellEmitted &&
		!at.Before(st.enteredAt.Add(time.Duration(*dwellMin)*time.Minute)) && st.clear(at) {
		st.dwellEmitted, st.lastEmit = true, at
		out = append(out, emit{EventDwell, at})
	}
	return st, out, suppressed
}

// replay folds one recorded event into a pair during the GF-280 rebuild. Rows
// arrive in occurred_at order, so the last row for a pair gives its state.
func replay(st pairState, kind EventKind, at time.Time) pairState {
	st.lastEmit, st.lastObs = at, at
	switch kind {
	case EventEnter:
		st.inside, st.enteredAt, st.dwellEmitted = true, at, false
	case EventExit:
		st.inside, st.dwellEmitted = false, false
	case EventDwell:
		st.dwellEmitted = true
	}
	return st
}
