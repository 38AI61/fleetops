package geofence

import (
	"slices"
	"testing"
	"time"
)

// base is the fixed clock for the table tests. Offsets are whole seconds.
var base = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

// step is one observation of a pair and what the state machine must do with it.
type step struct {
	at     int // seconds after base
	inside bool
	want   []emit
	sup    int // suppressed candidates
}

// ev is a want entry: an event of kind at the given offset.
func ev(kind EventKind, sec int) emit { return emit{kind, base.Add(time.Duration(sec) * time.Second)} }

// dwellMin returns a pointer to n, for a fence with dwell_min n.
func dwellMin(n int) *int { return &n }

func runSteps(t *testing.T, dwell *int, steps []step) {
	t.Helper()
	var st pairState
	for i, s := range steps {
		next, out, sup := advance(st, base.Add(time.Duration(s.at)*time.Second), s.inside, dwell)
		if !slices.Equal(out, s.want) {
			t.Fatalf("step %d (t=%d inside=%v): events = %v, want %v", i, s.at, s.inside, out, s.want)
		}
		if sup != s.sup {
			t.Fatalf("step %d (t=%d inside=%v): suppressed = %d, want %d", i, s.at, s.inside, sup, s.sup)
		}
		st = next
	}
}

// TestStateMachineTable covers the GF-281 to GF-283 rules, one case each.
func TestStateMachineTable(t *testing.T) {
	cases := []struct {
		name  string
		dwell *int
		steps []step
	}{
		{
			name: "crossing gives one enter and one exit",
			steps: []step{
				{at: 0, inside: true, want: []emit{ev(EventEnter, 0)}},
				{at: 60, inside: false, want: []emit{ev(EventExit, 60)}},
			},
		},
		{
			name: "exit without a recorded enter is ignored",
			steps: []step{
				{at: 0, inside: false},
				{at: 100, inside: false},
			},
		},
		{
			name: "unknown start with an inside truth emits enter",
			steps: []step{
				{at: 0, inside: true, want: []emit{ev(EventEnter, 0)}},
			},
		},
		{
			name: "unknown start with an outside truth emits nothing",
			steps: []step{
				{at: 0, inside: false},
			},
		},
		{
			name: "suppressed flip inside 30 s emits nothing and is counted",
			steps: []step{
				{at: 0, inside: true, want: []emit{ev(EventEnter, 0)}},
				{at: 10, inside: false, sup: 1},
				{at: 40, inside: false, want: []emit{ev(EventExit, 40)}},
			},
		},
		{
			name: "a flip back to the emitted state inside the window emits nothing",
			steps: []step{
				{at: 0, inside: true, want: []emit{ev(EventEnter, 0)}},
				{at: 5, inside: false, sup: 1},
				{at: 8, inside: true},
				{at: 31, inside: false, want: []emit{ev(EventExit, 31)}},
			},
		},
		{
			name: "out-of-order fix is ignored",
			steps: []step{
				{at: 100, inside: true, want: []emit{ev(EventEnter, 100)}},
				{at: 50, inside: false},
				{at: 110, inside: false, sup: 1},
				{at: 130, inside: false, want: []emit{ev(EventExit, 130)}},
			},
		},
		{
			name:  "dwell at exactly dwell_min fires once",
			dwell: dwellMin(1),
			steps: []step{
				{at: 0, inside: true, want: []emit{ev(EventEnter, 0)}},
				{at: 59, inside: true},
				{at: 60, inside: true, want: []emit{ev(EventDwell, 60)}},
			},
		},
		{
			name:  "dwell at dwell_min plus 1 s fires once",
			dwell: dwellMin(1),
			steps: []step{
				{at: 0, inside: true, want: []emit{ev(EventEnter, 0)}},
				{at: 61, inside: true, want: []emit{ev(EventDwell, 61)}},
				{at: 120, inside: true},
			},
		},
		{
			name:  "dwell does not fire again in the same episode",
			dwell: dwellMin(1),
			steps: []step{
				{at: 0, inside: true, want: []emit{ev(EventEnter, 0)}},
				{at: 60, inside: true, want: []emit{ev(EventDwell, 60)}},
				{at: 120, inside: true},
				{at: 300, inside: true},
			},
		},
		{
			name:  "dwell re-arms after an exit emission",
			dwell: dwellMin(1),
			steps: []step{
				{at: 0, inside: true, want: []emit{ev(EventEnter, 0)}},
				{at: 60, inside: true, want: []emit{ev(EventDwell, 60)}},
				{at: 90, inside: false, want: []emit{ev(EventExit, 90)}},
				{at: 120, inside: true, want: []emit{ev(EventEnter, 120)}},
				{at: 180, inside: true, want: []emit{ev(EventDwell, 180)}},
			},
		},
		{
			name: "dwell_min NULL never dwells",
			steps: []step{
				{at: 0, inside: true, want: []emit{ev(EventEnter, 0)}},
				{at: 600, inside: true},
			},
		},
		{
			name:  "dwell is not emitted on an outside truth",
			dwell: dwellMin(1),
			steps: []step{
				{at: 0, inside: true, want: []emit{ev(EventEnter, 0)}},
				{at: 10, inside: false, sup: 1},
				{at: 70, inside: false, want: []emit{ev(EventExit, 70)}},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { runSteps(t, c.dwell, c.steps) })
	}
}

// TestReplayRestoresDwellState checks the GF-280 rebuild fold: a dwell row marks
// the episode dwelled, so a later inside fix does not dwell again.
func TestReplayRestoresDwellState(t *testing.T) {
	st := replay(pairState{}, EventEnter, base)
	st = replay(st, EventDwell, base.Add(60*time.Second))
	if !st.inside || !st.dwellEmitted {
		t.Fatalf("state after enter+dwell = %+v, want inside and dwellEmitted", st)
	}
	_, out, _ := advance(st, base.Add(120*time.Second), true, dwellMin(1))
	if len(out) != 0 {
		t.Fatalf("events after rebuild = %v, want none", out)
	}

	st = replay(st, EventExit, base.Add(130*time.Second))
	if st.inside || st.dwellEmitted {
		t.Fatalf("state after exit = %+v, want outside and not dwelled", st)
	}
}
