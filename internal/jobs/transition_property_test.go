package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"pgregory.net/rapid"

	"fleetops/internal/testinfra"
)

// propTargets are the states the property drives to, excluding assigned (only Assign reaches it).
var propTargets = []State{StateAccepted, StateEnRoute, StatePickedUp, StateDelivered, StateFailed, StateCancelled}

// propJob is the model of one job: the state and payload it must have, and how
// many transitions the store applied to it.
type propJob struct {
	id      int64
	state   State
	vehicle int64
	driver  int64
	applied int
	last    string // "assign" or "transition": the kind of the last applied operation
}

// TestTransitionProperty drives random operation sequences through the store
// and checks the P10 invariants after every op (GF-172, roadmap P10 testing).
func TestTransitionProperty(t *testing.T) {
	newDB := testinfra.NewMigratedDBFrom(t)

	rapid.Check(t, func(rt *rapid.T) {
		db := newDB(rt)
		s := &Store{DB: db}
		ctx := context.Background()

		fleet := propID(db, `INSERT INTO fleets (name) VALUES ('prop-a') RETURNING id`)
		admin := propID(db, `INSERT INTO users (fleet_id, email, password_hash, role) VALUES ($1, 'prop-a@example.test', 'x', 'admin') RETURNING id`, fleet)
		otherFleet := propID(db, `INSERT INTO fleets (name) VALUES ('prop-b') RETURNING id`)
		otherAdmin := propID(db, `INSERT INTO users (fleet_id, email, password_hash, role) VALUES ($1, 'prop-b@example.test', 'x', 'admin') RETURNING id`, otherFleet)

		vehicles := make([]int64, rapid.IntRange(2, 4).Draw(rt, "vehicles"))
		for i := range vehicles {
			vehicles[i] = propID(db, `INSERT INTO vehicles (fleet_id, plate) VALUES ($1, $2) RETURNING id`, fleet, fmt.Sprintf("PV%d", i))
		}
		drivers := make([]int64, rapid.IntRange(1, 3).Draw(rt, "drivers"))
		for i := range drivers {
			drivers[i] = propID(db, `INSERT INTO drivers (fleet_id, name, license_no_hash) VALUES ($1, $2, $3) RETURNING id`,
				fleet, fmt.Sprintf("pd%d", i), []byte(fmt.Sprintf("prop-driver-%d", i)))
		}

		var model []*propJob
		pick := func(label string) *propJob {
			return model[rapid.IntRange(0, len(model)-1).Draw(rt, label)]
		}
		events := func(job int64) int {
			return propCount(rt, db, `SELECT count(*) FROM job_events WHERE job_id = $1`, job)
		}

		// doTransition runs Transition or Cancel and checks the D-24 outcome against the model.
		doTransition := func(j *propJob, to State, viaCancel bool) {
			before := events(j.id)
			var (
				ch  Change
				out Outcome
				err error
			)
			if viaCancel {
				ch, out, err = s.Cancel(ctx, admin, fleet, j.id)
			} else {
				ch, out, err = s.Transition(ctx, admin, fleet, j.id, to)
			}
			switch {
			case j.state == to:
				expectOutcome(rt, "replay", err, out, Replayed)
			case CanTransition(j.state, to):
				expectOutcome(rt, "applied", err, out, Applied)
				j.state = to
				j.applied++
				j.last = "transition"
				if ch.Job.State != to {
					rt.Fatalf("applied transition returned state %s, want %s", ch.Job.State, to)
				}
			default:
				expectErr(rt, "illegal transition", err, ErrConflict)
			}
			if after := events(j.id); after != before+boolInt(out == Applied && err == nil) {
				rt.Fatalf("events %d -> %d for outcome %v (err %v)", before, after, out, err)
			}
		}

		check := func() {
			rt.Helper()
			if n := propCount(rt, db, `
				SELECT count(*) FROM job_events e
				WHERE e.from_state IS NOT NULL
				  AND NOT EXISTS (SELECT 1 FROM job_transitions t
				                  WHERE t.from_state = e.from_state AND t.to_state = e.to_state)`); n != 0 {
				rt.Fatalf("%d persisted events are not in job_transitions", n)
			}
			for _, j := range model {
				if n := events(j.id); n != 1+j.applied {
					rt.Fatalf("job %d: %d events, want 1 creation + %d applied", j.id, n, j.applied)
				}
				var state string
				if err := db.QueryRow(`SELECT state FROM jobs WHERE id = $1`, j.id).Scan(&state); err != nil {
					rt.Fatalf("read state: %v", err)
				}
				if State(state) != j.state {
					rt.Fatalf("job %d: state %s, model %s", j.id, state, j.state)
				}
			}
		}

		ops := []string{"create", "assign", "transition", "cancel", "replay", "invalid", "invalidAssign", "cross"}
		for range rapid.IntRange(1, 40).Draw(rt, "length") {
			kind := rapid.SampledFrom(ops).Draw(rt, "op")
			if kind != "create" && len(model) == 0 {
				kind = "create"
			}
			switch kind {
			case "create":
				if len(model) >= 5 {
					break
				}
				job, err := s.Create(ctx, admin, fleet, newTestJob())
				if err != nil {
					rt.Fatalf("create: %v", err)
				}
				model = append(model, &propJob{id: job.ID, state: StateCreated})

			case "assign":
				j := pick("job")
				v := vehicles[rapid.IntRange(0, len(vehicles)-1).Draw(rt, "vehicle")]
				d := drivers[rapid.IntRange(0, len(drivers)-1).Draw(rt, "driver")]
				before := events(j.id)
				_, out, err := s.Assign(ctx, admin, fleet, j.id, v, d)
				switch {
				case j.state == StateCreated || (j.state == StateAssigned && (j.vehicle != v || j.driver != d)):
					expectOutcome(rt, "assign", err, out, Applied)
					j.state, j.vehicle, j.driver = StateAssigned, v, d
					j.applied++
					j.last = "assign"
				case j.state == StateAssigned:
					expectOutcome(rt, "assign replay", err, out, Replayed)
				default:
					expectErr(rt, "assign on "+string(j.state), err, ErrConflict)
				}
				if after := events(j.id); after != before+boolInt(out == Applied && err == nil) {
					rt.Fatalf("assign events %d -> %d", before, after)
				}

			case "transition":
				j := pick("job")
				doTransition(j, rapid.SampledFrom(propTargets).Draw(rt, "to"), false)

			case "cancel":
				doTransition(pick("job"), StateCancelled, true)

			case "replay":
				j := pick("job")
				if j.applied == 0 {
					break
				}
				before := events(j.id)
				var out Outcome
				var err error
				if j.last == "assign" {
					_, out, err = s.Assign(ctx, admin, fleet, j.id, j.vehicle, j.driver)
				} else {
					_, out, err = s.Transition(ctx, admin, fleet, j.id, j.state)
				}
				expectOutcome(rt, "replay of last transition", err, out, Replayed)
				if after := events(j.id); after != before {
					rt.Fatalf("replay wrote events: %d -> %d", before, after)
				}

			case "invalid":
				j := pick("job")
				var candidates []State
				for _, to := range propTargets {
					if to != j.state && !CanTransition(j.state, to) {
						candidates = append(candidates, to)
					}
				}
				if len(candidates) == 0 {
					break
				}
				to := rapid.SampledFrom(candidates).Draw(rt, "invalid")
				before := events(j.id)
				_, _, err := s.Transition(ctx, admin, fleet, j.id, to)
				expectErr(rt, fmt.Sprintf("invalid %s -> %s", j.state, to), err, ErrConflict)
				if events(j.id) != before {
					rt.Fatalf("refused transition wrote events: %d -> %d", before, events(j.id))
				}

			case "invalidAssign":
				j := pick("job")
				if j.state == StateCreated || j.state == StateAssigned {
					break
				}
				before := events(j.id)
				_, _, err := s.Assign(ctx, admin, fleet, j.id, vehicles[0], drivers[0])
				expectErr(rt, "assign on "+string(j.state), err, ErrConflict)
				if events(j.id) != before {
					rt.Fatalf("refused assign wrote events")
				}

			case "cross":
				j := pick("job")
				before := events(j.id)
				_, _, err := s.Transition(ctx, otherAdmin, otherFleet, j.id, StateCancelled)
				expectErr(rt, "cross-fleet transition", err, ErrNotFound)
				_, _, err = s.Assign(ctx, otherAdmin, otherFleet, j.id, vehicles[0], drivers[0])
				expectErr(rt, "cross-fleet assign", err, ErrNotFound)
				if events(j.id) != before {
					rt.Fatalf("cross-fleet op wrote events")
				}
			}
			check()
		}
		check()
	})
}

// expectOutcome fails the check unless err is nil and out is want.
func expectOutcome(rt *rapid.T, what string, err error, out, want Outcome) {
	rt.Helper()
	if err != nil {
		rt.Fatalf("%s: unexpected error %v", what, err)
	}
	if out != want {
		rt.Fatalf("%s: outcome %v, want %v", what, out, want)
	}
}

// expectErr fails the check unless err is want.
func expectErr(rt *rapid.T, what string, err, want error) {
	rt.Helper()
	if !errors.Is(err, want) {
		rt.Fatalf("%s: err = %v, want %v", what, err, want)
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// propID runs a query that returns one id. Panics are reported as failing checks.
func propID(db *sql.DB, query string, args ...any) int64 {
	var id int64
	if err := db.QueryRow(query, args...).Scan(&id); err != nil {
		panic(err)
	}
	return id
}

// propCount runs a count query inside a check.
func propCount(rt *rapid.T, db *sql.DB, query string, args ...any) int {
	rt.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		rt.Fatalf("count query: %v", err)
	}
	return n
}
