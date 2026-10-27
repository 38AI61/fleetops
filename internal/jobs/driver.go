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
