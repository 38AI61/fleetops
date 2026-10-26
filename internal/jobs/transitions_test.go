package jobs

import (
	"context"
	"strings"
	"testing"

	"fleetops/internal/testinfra"
)

// TestTransitionEdges pins the D-23 edge set and a sample of illegal pairs.
func TestTransitionEdges(t *testing.T) {
	want := map[Edge]bool{
		{StateCreated, StateAssigned}:   true,
		{StateAssigned, StateAssigned}:  true,
		{StateAssigned, StateAccepted}:  true,
		{StateAccepted, StateEnRoute}:   true,
		{StateEnRoute, StatePickedUp}:   true,
		{StatePickedUp, StateDelivered}: true,
		{StateAccepted, StateFailed}:    true,
		{StateEnRoute, StateFailed}:     true,
		{StatePickedUp, StateFailed}:    true,
		{StateCreated, StateCancelled}:  true,
		{StateAssigned, StateCancelled}: true,
		{StateAccepted, StateCancelled}: true,
	}
	if len(Edges) != len(want) {
		t.Fatalf("Edges has %d entries, want %d", len(Edges), len(want))
	}
	for _, e := range Edges {
		if !want[e] {
			t.Errorf("unexpected edge %s -> %s", e.From, e.To)
		}
		if !CanTransition(e.From, e.To) {
			t.Errorf("CanTransition(%s, %s) = false for a D-23 edge", e.From, e.To)
		}
	}
	for e := range want {
		if !CanTransition(e.From, e.To) {
			t.Errorf("CanTransition(%s, %s) = false, want true", e.From, e.To)
		}
	}

	for _, illegal := range []Edge{
		{StateCreated, StateAccepted},
		{StateDelivered, StateFailed},
		{StateCancelled, StateAssigned},
		{StateAssigned, StateCreated},
		{StateCreated, StateCreated},
		{StateEnRoute, StateCancelled},
	} {
		if CanTransition(illegal.From, illegal.To) {
			t.Errorf("CanTransition(%s, %s) = true, want false", illegal.From, illegal.To)
		}
	}
}

// TestStateVocabulary keeps States in step with the eight D-23 states.
func TestStateVocabulary(t *testing.T) {
	if len(States) != 8 {
		t.Fatalf("States has %d entries, want 8", len(States))
	}
	for _, s := range States {
		if !s.Valid() {
			t.Errorf("%s is not valid", s)
		}
	}
	if State("bogus").Valid() {
		t.Error("bogus state reported valid")
	}
}

// TestJobTransitionsMirror: the job_transitions rows equal the Go edges (D-23 single source).
func TestJobTransitionsMirror(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	rows, err := db.QueryContext(context.Background(), `SELECT from_state, to_state FROM job_transitions`)
	if err != nil {
		t.Fatalf("query job_transitions: %v", err)
	}
	defer func() { _ = rows.Close() }()

	got := map[Edge]bool{}
	for rows.Next() {
		var from, to string
		if err := rows.Scan(&from, &to); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[Edge{State(from), State(to)}] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(got) != len(Edges) {
		t.Fatalf("job_transitions has %d rows, Edges has %d", len(got), len(Edges))
	}
	for _, e := range Edges {
		if !got[e] {
			t.Errorf("job_transitions is missing %s -> %s", e.From, e.To)
		}
	}
}

// TestJobsTriggerRejectsInvalidUpdate checks the BEFORE UPDATE guard by SQLSTATE (GF-171).
func TestJobsTriggerRejectsInvalidUpdate(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	fleet, user := fleetWithAdmin(t, db, "guard")
	other, _ := fleetWithAdmin(t, db, "guard-other")
	job := insertJobRaw(t, db, fleet, user)
	ctx := context.Background()

	// Illegal edge: created -> delivered is FL409 (checked before the actor).
	_, err := db.ExecContext(ctx, `UPDATE jobs SET state = 'delivered' WHERE id = $1`, job)
	if got := sqlState(err); got != "FL409" {
		t.Errorf("created -> delivered: SQLSTATE %q (%v), want FL409", got, err)
	}

	// Assignment columns may not change without a state change from created.
	fleetVeh := fleetVehicle(t, db, fleet, "GUARD-1")
	fleetDrv := fleetDriver(t, db, fleet, "guard-driver")
	_, err = db.ExecContext(ctx, `UPDATE jobs SET vehicle_id = $2, driver_id = $3 WHERE id = $1`, job, fleetVeh, fleetDrv)
	if got := sqlState(err); got != "FL409" {
		t.Errorf("assignment change on created: SQLSTATE %q (%v), want FL409", got, err)
	}

	// Valid edge with no actor is FL428.
	_, err = db.ExecContext(ctx, `UPDATE jobs SET state = 'cancelled' WHERE id = $1`, job)
	if got := sqlState(err); got != "FL428" {
		t.Errorf("created -> cancelled without actor: SQLSTATE %q (%v), want FL428", got, err)
	}

	// A non-event update with no actor succeeds.
	if _, err := db.ExecContext(ctx, `UPDATE jobs SET notes = 'call at the gate' WHERE id = $1`, job); err != nil {
		t.Errorf("notes update without actor: %v", err)
	}

	// fleet_id is immutable.
	_, err = db.ExecContext(ctx, `UPDATE jobs SET fleet_id = $2 WHERE id = $1`, job, other)
	if got := sqlState(err); got != "23514" {
		t.Errorf("fleet_id change: SQLSTATE %q (%v), want 23514", got, err)
	}

	// The same valid edge with an actor succeeds and writes exactly one event.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('app.actor_id', $1::text, true)`, user); err != nil {
		t.Fatalf("set actor: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET state = 'cancelled' WHERE id = $1`, job); err != nil {
		t.Fatalf("cancel with actor: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if n := rowCount(t, db, `SELECT count(*) FROM job_events WHERE job_id = $1 AND from_state = 'created' AND to_state = 'cancelled'`, job); n != 1 {
		t.Errorf("created -> cancelled events = %d, want 1", n)
	}
}

// TestJobEventsAppendOnly: UPDATE, DELETE and TRUNCATE on job_events are refused (R-7, GF-171).
func TestJobEventsAppendOnly(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	fleet, user := fleetWithAdmin(t, db, "append")
	insertJobRaw(t, db, fleet, user)
	ctx := context.Background()

	if n := rowCount(t, db, `SELECT count(*) FROM job_events`); n != 1 {
		t.Fatalf("creation events = %d, want 1", n)
	}
	for _, stmt := range []string{
		`UPDATE job_events SET to_state = 'cancelled'`,
		`DELETE FROM job_events`,
		`TRUNCATE job_events`,
	} {
		_, err := db.ExecContext(ctx, stmt)
		if got := sqlState(err); got != "23514" {
			t.Errorf("%s: SQLSTATE %q (%v), want 23514", stmt, got, err)
			continue
		}
		if err == nil || !strings.Contains(err.Error(), "job_events is append-only") {
			t.Errorf("%s: message %v, want job_events is append-only", stmt, err)
		}
	}
	if n := rowCount(t, db, `SELECT count(*) FROM job_events`); n != 1 {
		t.Errorf("events after refused writes = %d, want 1", n)
	}
}
