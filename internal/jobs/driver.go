package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"fleetops/internal/auth"
)

// resolveDriver returns the driver row the caller's user is linked to, in the
// principal's fleet (GF-184). ok is false when the user has no such row: that
// caller has no jobs and cannot mutate any.
func resolveDriver(ctx context.Context, q querier, p auth.Principal) (int64, bool, error) {
	var id int64
	err := q.QueryRowContext(ctx, `
		SELECT d.id FROM users u
		JOIN drivers d ON d.id = u.driver_id AND d.fleet_id = $2
		WHERE u.id = $1`, p.UserID, p.FleetID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("resolve driver: %w", err)
	}
	return id, true, nil
}

// driverTodaySQL is GET /driver/jobs/today (GF-185). Terminal jobs are excluded.
// A dated job is included when its window_start day, in the fleet timezone, is
// on or before the day of $3, so carried-over work stays visible. A NULL
// window_start is active work and is always included.
const driverTodaySQL = selectJobSQL + `
JOIN fleets f ON f.id = j.fleet_id
WHERE j.fleet_id = $1
  AND j.driver_id = $2
  AND j.state NOT IN ('delivered','failed','cancelled')
  AND (j.window_start IS NULL
       OR (j.window_start AT TIME ZONE f.timezone)::date <= ($3::timestamptz AT TIME ZONE f.timezone)::date)
ORDER BY j.window_start ASC NULLS LAST, j.id ASC`

// driverTarget reports whether a driver may request to. deliver is P12; assigned
// and the dispatcher-only targets are not driver moves.
func driverTarget(to State) bool {
	switch to {
	case StateAccepted, StateEnRoute, StatePickedUp, StateFailed:
		return true
	}
	return false
}

// DriverTransition applies a driver move to a job the driver owns (GF-187, D-24).
// One transaction, in order: lock the row; the job must belong to the fleet and
// to driverID, else ErrNotFound, checked before the replay so a non-owner learns
// nothing from a 200; the current state equal to the target is a replay with no
// event; a D-23 edge is applied by an UPDATE guarded on the state read under the
// lock; anything else is ErrConflict. A fail writes reason in the same UPDATE
// (GF-189). The trigger writes the event.
func (s *Store) DriverTransition(ctx context.Context, actor, fleetID, jobID, driverID int64, to State, reason *string) (Change, Outcome, error) {
	if !driverTarget(to) || (to == StateFailed && reason == nil) {
		return Change{}, Applied, ErrValidation
	}
	tx, err := s.begin(ctx, actor)
	if err != nil {
		return Change{}, Applied, err
	}
	defer func() { _ = tx.Rollback() }()

	current, err := selectJob(ctx, tx, fleetID, jobID, true)
	if err != nil {
		return Change{}, Applied, err
	}
	if current.DriverID == nil || *current.DriverID != driverID {
		return Change{}, Applied, ErrNotFound
	}
	if current.State == to {
		return replayed(tx, current)
	}
	if !CanTransition(current.State, to) {
		return Change{}, Applied, ErrConflict
	}

	res, err := execDriverMove(ctx, tx, fleetID, jobID, current.State, to, reason)
	if err != nil {
		return Change{}, Applied, mapWriteErr(err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return Change{}, Applied, fmt.Errorf("rows affected: %w", err)
	} else if n == 0 {
		// D-24: zero rows, then re-read. The row lock makes this branch
		// unreachable in practice; it keeps the rule as written.
		now, err := selectJob(ctx, tx, fleetID, jobID, false)
		if err != nil {
			return Change{}, Applied, err
		}
		if now.State == to {
			return replayed(tx, now)
		}
		return Change{}, Applied, ErrConflict
	}

	change, err := appliedChange(ctx, tx, fleetID, jobID)
	if err != nil {
		return Change{}, Applied, err
	}
	if err := tx.Commit(); err != nil {
		return Change{}, Applied, fmt.Errorf("commit driver transition: %w", err)
	}
	return change, Applied, nil
}

// execDriverMove runs the guarded UPDATE. A fail also writes failure_reason; the
// trigger guards only state and assignment, so that write is unrestricted (GF-189).
func execDriverMove(ctx context.Context, tx *sql.Tx, fleetID, jobID int64, from, to State, reason *string) (sql.Result, error) {
	if to == StateFailed {
		return tx.ExecContext(ctx,
			`UPDATE jobs SET state = $1, failure_reason = $2 WHERE id = $3 AND fleet_id = $4 AND state = $5`,
			string(to), *reason, jobID, fleetID, string(from))
	}
	return tx.ExecContext(ctx,
		`UPDATE jobs SET state = $1 WHERE id = $2 AND fleet_id = $3 AND state = $4`,
		string(to), jobID, fleetID, string(from))
}

// DriverJobs returns the driver's active jobs for today (GF-185). now is the
// request clock; SQL converts it to the fleet timezone.
func (s *Store) DriverJobs(ctx context.Context, fleetID, driverID int64, now time.Time) ([]Job, error) {
	rows, err := s.DB.QueryContext(ctx, driverTodaySQL, fleetID, driverID, now)
	if err != nil {
		return nil, fmt.Errorf("driver jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan driver job: %w", err)
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("driver rows: %w", err)
	}
	return out, nil
}

// DriverDeliverGate is the read-only pre-check for POST /driver/jobs/:id/deliver
// (GF-197). It runs before any body parse and before any upload. A job the driver
// does not own in the fleet is ErrNotFound. A state other than picked_up or
// delivered is ErrConflict. DriverDeliver re-checks both under the row lock.
func (s *Store) DriverDeliverGate(ctx context.Context, fleetID, jobID, driverID int64) (State, error) {
	var state string
	err := s.DB.QueryRowContext(ctx,
		`SELECT state FROM jobs WHERE id = $1 AND fleet_id = $2 AND driver_id = $3`,
		jobID, fleetID, driverID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("deliver gate: %w", err)
	}
	switch st := State(state); st {
	case StatePickedUp, StateDelivered:
		return st, nil
	}
	return "", ErrConflict
}

// DriverDeliver applies the picked_up -> delivered move with its proof (GF-194,
// D-24). One transaction, in order: lock the row; the ownership guard returns
// ErrNotFound before the replay (GF-187); delivered is a replay with no event and
// the first proof key stays (D-24); any state other than picked_up is ErrConflict.
// One UPDATE sets state, delivered_at, proof_photo and delivery_geom together. The
// trigger writes the event.
func (s *Store) DriverDeliver(ctx context.Context, actor, fleetID, jobID, driverID int64, photoKey string, lat, lng float64) (Change, Outcome, error) {
	tx, err := s.begin(ctx, actor)
	if err != nil {
		return Change{}, Applied, err
	}
	defer func() { _ = tx.Rollback() }()

	current, err := selectJob(ctx, tx, fleetID, jobID, true)
	if err != nil {
		return Change{}, Applied, err
	}
	if current.DriverID == nil || *current.DriverID != driverID {
		return Change{}, Applied, ErrNotFound
	}
	if current.State == StateDelivered {
		return replayed(tx, current)
	}
	if current.State != StatePickedUp {
		return Change{}, Applied, ErrConflict
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE jobs
		SET state = 'delivered', delivered_at = now(), proof_photo = $1,
		    delivery_geom = ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography
		WHERE id = $4 AND fleet_id = $5 AND state = 'picked_up'`,
		photoKey, lng, lat, jobID, fleetID)
	if err != nil {
		return Change{}, Applied, mapWriteErr(err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return Change{}, Applied, fmt.Errorf("rows affected: %w", err)
	} else if n == 0 {
		// D-24: zero rows, then re-read. The row lock makes this branch
		// unreachable in practice; it keeps the rule as written.
		now, err := selectJob(ctx, tx, fleetID, jobID, false)
		if err != nil {
			return Change{}, Applied, err
		}
		if now.State == StateDelivered {
			return replayed(tx, now)
		}
		return Change{}, Applied, ErrConflict
	}

	change, err := appliedChange(ctx, tx, fleetID, jobID)
	if err != nil {
		return Change{}, Applied, err
	}
	if err := tx.Commit(); err != nil {
		return Change{}, Applied, fmt.Errorf("commit driver deliver: %w", err)
	}
	return change, Applied, nil
}
