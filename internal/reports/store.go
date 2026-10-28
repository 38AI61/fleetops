package reports

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"fleetops/internal/auth"
)

// Row is one driver's day as the API returns it (GF-324). OnTimePct is nil when
// the denominator is zero.
type Row struct {
	DriverID      int64    `json:"driver_id"`
	DriverName    string   `json:"driver_name"`
	JobsCompleted int      `json:"jobs_completed"`
	OnTimeCount   int      `json:"on_time_count"`
	OnTimeDenom   int      `json:"on_time_denom"`
	OnTimePct     *float64 `json:"on_time_pct"`
	DistanceM     float64  `json:"distance_m"`
	IdleS         int      `json:"idle_s"`
}

// Store reads the rollup table and the registry rows the handler needs. It never
// reads positions or jobs: the report is one indexed SELECT (GF-324, README §9).
type Store struct {
	DB *sql.DB
}

// rowsSQL reads one fleet-day from the rollup, joined to the driver name. $3
// filters to one driver, and a NULL $3 keeps every driver. The on-time percentage
// is rounded in SQL so JSON and CSV print the same value (GF-324, GF-327).
const rowsSQL = `
SELECT s.driver_id, d.name, s.jobs_completed, s.on_time_count, s.on_time_denom,
       CASE WHEN s.on_time_denom = 0 THEN NULL
            ELSE ROUND(100.0 * s.on_time_count / s.on_time_denom, 1) END,
       s.distance_m, s.idle_s
FROM daily_driver_stats s
JOIN drivers d ON d.id = s.driver_id
WHERE s.fleet_id = $1::bigint AND s.day = $2::date
  AND ($3::bigint IS NULL OR s.driver_id = $3::bigint)
ORDER BY s.driver_id`

// Rows opens the cursor for one fleet-day. The caller closes it. driver nil
// means every driver of the fleet.
func (s *Store) Rows(ctx context.Context, fleetID int64, date string, driver *int64) (*sql.Rows, error) {
	var d any
	if driver != nil {
		d = *driver
	}
	rows, err := s.DB.QueryContext(ctx, rowsSQL, fleetID, date, d)
	if err != nil {
		return nil, fmt.Errorf("read daily rows: %w", err)
	}
	return rows, nil
}

// scanRow reads the current row of a Rows cursor opened by Rows.
func scanRow(rows *sql.Rows) (Row, error) {
	var r Row
	var pct sql.NullFloat64
	if err := rows.Scan(&r.DriverID, &r.DriverName, &r.JobsCompleted, &r.OnTimeCount, &r.OnTimeDenom,
		&pct, &r.DistanceM, &r.IdleS); err != nil {
		return Row{}, fmt.Errorf("scan daily row: %w", err)
	}
	if pct.Valid {
		v := pct.Float64
		r.OnTimePct = &v
	}
	return r, nil
}

// Daily returns the JSON rows of one fleet-day. It is never nil, so an empty day encodes as [].
func (s *Store) Daily(ctx context.Context, fleetID int64, date string, driver *int64) ([]Row, error) {
	rows, err := s.Rows(ctx, fleetID, date, driver)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]Row, 0)
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// fleetTimezone returns the fleet's timezone name (GF-14).
func (s *Store) fleetTimezone(ctx context.Context, fleetID int64) (string, error) {
	var tz string
	if err := s.DB.QueryRowContext(ctx, `SELECT timezone FROM fleets WHERE id = $1`, fleetID).Scan(&tz); err != nil {
		return "", fmt.Errorf("fleet timezone: %w", err)
	}
	return tz, nil
}

// driverInFleet reports whether driver is a driver row of the fleet (GF-324).
func (s *Store) driverInFleet(ctx context.Context, fleetID, driver int64) (bool, error) {
	var ok bool
	if err := s.DB.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM drivers WHERE id = $1 AND fleet_id = $2)`, driver, fleetID,
	).Scan(&ok); err != nil {
		return false, fmt.Errorf("driver in fleet: %w", err)
	}
	return ok, nil
}

// ownDriver returns the driver row the caller's user is linked to, in the
// principal's fleet (GF-324, the same link as internal/jobs/driver.go). ok is
// false when the user has no such row.
func (s *Store) ownDriver(ctx context.Context, p auth.Principal) (int64, bool, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, `
		SELECT d.id FROM users u
		JOIN drivers d ON d.id = u.driver_id AND d.fleet_id = $2
		WHERE u.id = $1`, p.UserID, p.FleetID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("own driver: %w", err)
	}
	return id, true, nil
}
