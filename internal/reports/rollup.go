// Package reports builds the daily driver rollup (D-32) and serves the daily
// report. The rollup runs in services/ingest; the API reads its table only.
package reports

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Rule constants (GF-318). They are not env vars.
const (
	// maxGap is the longest sample gap a leg may have. Longer gaps count for
	// neither distance nor idle (GF-321).
	maxGap = 300 * time.Second
	// idleBelowKmh is the strict idle threshold on the opening sample's speed.
	idleBelowKmh = 1.0
)

// dayRow is one driver's computed day, the JSON payload of the write (GF-323).
type dayRow struct {
	DriverID      int64   `json:"driver_id"`
	JobsCompleted int     `json:"jobs_completed"`
	OnTimeCount   int     `json:"on_time_count"`
	OnTimeDenom   int     `json:"on_time_denom"`
	DistanceM     float64 `json:"distance_m"`
	IdleS         int     `json:"idle_s"`
}

// Written counts the rows one RollupDay call removed and wrote (GF-323).
// A second run on unchanged input reports zero for both.
type Written struct {
	Deleted  int64
	Upserted int64
}

// DayBounds returns the fleet-local day [start, end) for the civil date of day,
// computed in loc so a 23 h or 25 h DST day gets its real length (GF-326).
// Only the Year, Month and Day of day are read.
func DayBounds(loc *time.Location, day time.Time) (time.Time, time.Time) {
	y, m, d := day.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc), time.Date(y, m, d+1, 0, 0, 0, 0, loc)
}

// LocalDay returns the fleet-local civil date of now as a UTC-midnight value,
// the form RollupDay and DayBounds take (GF-320).
func LocalDay(now time.Time, loc *time.Location) time.Time {
	y, m, d := now.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// computeSQL is the GF-321 aggregation for one fleet-day, one statement (GF-322).
// $1 fleet_id, $2 day start, $3 day end (both timestamptz, so pruning on
// server_ts applies). LAG runs over the in-window samples only, so a leg that
// crosses midnight has no opening sample and is dropped.
const computeSQL = `
WITH samples AS (
  SELECT p.vehicle_id, p.server_ts, p.geom, p.speed_kmh,
         LAG(p.server_ts) OVER w AS prev_ts,
         LAG(p.geom)      OVER w AS prev_geom,
         LAG(p.speed_kmh) OVER w AS prev_speed
  FROM positions p
  WHERE p.fleet_id = $1::bigint AND p.server_ts >= $2::timestamptz AND p.server_ts < $3::timestamptz
  WINDOW w AS (PARTITION BY p.vehicle_id ORDER BY p.server_ts, p.msg_id)
),
legs AS (
  SELECT vehicle_id, prev_ts, prev_speed,
         EXTRACT(EPOCH FROM (server_ts - prev_ts)) AS gap_s,
         ST_DistanceSphere(prev_geom::geometry, geom::geometry) AS dist_m
  FROM samples
  WHERE prev_ts IS NOT NULL AND server_ts - prev_ts <= interval '300 seconds'
),
attributed AS (
  SELECT pr.driver_id, l.gap_s, l.dist_m, l.prev_speed
  FROM legs l
  CROSS JOIN LATERAL (
    SELECT vp.driver_id FROM vehicle_pairings vp
    WHERE vp.vehicle_id = l.vehicle_id
      AND vp.started_at <= l.prev_ts
      AND (vp.ended_at IS NULL OR l.prev_ts < vp.ended_at)
      AND vp.driver_id IS NOT NULL
    ORDER BY vp.started_at DESC, vp.id DESC
    LIMIT 1
  ) pr
),
leg_agg AS (
  SELECT driver_id,
         ROUND(SUM(dist_m)::numeric, 1)::double precision AS distance_m,
         ROUND(SUM(CASE WHEN prev_speed < 1.0 THEN gap_s ELSE 0 END))::int AS idle_s
  FROM attributed
  GROUP BY driver_id
),
job_agg AS (
  SELECT j.driver_id,
         COUNT(*)::int AS jobs_completed,
         (COUNT(*) FILTER (WHERE j.window_start IS NOT NULL AND j.window_end IS NOT NULL))::int AS on_time_denom,
         (COUNT(*) FILTER (WHERE j.window_start IS NOT NULL AND j.window_end IS NOT NULL
                             AND j.delivered_at <= j.window_end))::int AS on_time_count
  FROM jobs j
  WHERE j.fleet_id = $1::bigint AND j.state = 'delivered' AND j.driver_id IS NOT NULL
    AND j.delivered_at >= $2::timestamptz AND j.delivered_at < $3::timestamptz
  GROUP BY j.driver_id
)
SELECT COALESCE(l.driver_id, j.driver_id),
       COALESCE(j.jobs_completed, 0), COALESCE(j.on_time_count, 0), COALESCE(j.on_time_denom, 0),
       COALESCE(l.distance_m, 0), COALESCE(l.idle_s, 0)
FROM leg_agg l
FULL JOIN job_agg j ON j.driver_id = l.driver_id
ORDER BY 1`

// deleteSQL drops rows for drivers no longer in the computed set (GF-323 step 1).
// $3 is the computed set as a JSON array of dayRow.
const deleteSQL = `
DELETE FROM daily_driver_stats
WHERE fleet_id = $1::bigint AND day = $2::date
  AND driver_id NOT IN (SELECT r.driver_id FROM jsonb_to_recordset($3::jsonb) AS r(driver_id bigint))`

// upsertSQL writes the computed set and skips unchanged rows, so a repeat run
// affects 0 rows (GF-323 step 2).
const upsertSQL = `
INSERT INTO daily_driver_stats (fleet_id, driver_id, day, jobs_completed, on_time_count, on_time_denom, distance_m, idle_s)
SELECT $1::bigint, r.driver_id, $2::date, r.jobs_completed, r.on_time_count, r.on_time_denom, r.distance_m, r.idle_s
FROM jsonb_to_recordset($3::jsonb) AS r(driver_id bigint, jobs_completed int, on_time_count int,
                                         on_time_denom int, distance_m double precision, idle_s int)
ON CONFLICT (fleet_id, driver_id, day) DO UPDATE SET
  jobs_completed = EXCLUDED.jobs_completed,
  on_time_count  = EXCLUDED.on_time_count,
  on_time_denom  = EXCLUDED.on_time_denom,
  distance_m     = EXCLUDED.distance_m,
  idle_s         = EXCLUDED.idle_s
WHERE (daily_driver_stats.jobs_completed, daily_driver_stats.on_time_count, daily_driver_stats.on_time_denom,
       daily_driver_stats.distance_m, daily_driver_stats.idle_s)
      IS DISTINCT FROM (EXCLUDED.jobs_completed, EXCLUDED.on_time_count, EXCLUDED.on_time_denom,
                        EXCLUDED.distance_m, EXCLUDED.idle_s)`

// RollupDay recomputes one fleet-day inside tx (GF-322, GF-323). day is a civil
// date (see LocalDay). The computed set is small (one row per driver), so only
// that set crosses into Go; sample rows never do.
func RollupDay(ctx context.Context, tx *sql.Tx, fleetID int64, loc *time.Location, day time.Time) (Written, error) {
	start, end := DayBounds(loc, day)
	date := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC).Format(time.DateOnly)

	rows, err := computeDay(ctx, tx, fleetID, start, end)
	if err != nil {
		return Written{}, err
	}
	payload, err := json.Marshal(rows)
	if err != nil {
		return Written{}, fmt.Errorf("encode day %s: %w", date, err)
	}

	del, err := tx.ExecContext(ctx, deleteSQL, fleetID, date, string(payload))
	if err != nil {
		return Written{}, fmt.Errorf("delete stale rows for fleet %d day %s: %w", fleetID, date, err)
	}
	deleted, err := del.RowsAffected()
	if err != nil {
		return Written{}, err
	}
	ups, err := tx.ExecContext(ctx, upsertSQL, fleetID, date, string(payload))
	if err != nil {
		return Written{}, fmt.Errorf("upsert rows for fleet %d day %s: %w", fleetID, date, err)
	}
	upserted, err := ups.RowsAffected()
	if err != nil {
		return Written{}, err
	}
	return Written{Deleted: deleted, Upserted: upserted}, nil
}

// computeDay runs computeSQL and returns the computed rows, sorted by driver.
// The slice is never nil, so an empty day encodes as [] (GF-323).
func computeDay(ctx context.Context, tx *sql.Tx, fleetID int64, start, end time.Time) ([]dayRow, error) {
	rows, err := tx.QueryContext(ctx, computeSQL, fleetID, start, end)
	if err != nil {
		return nil, fmt.Errorf("compute fleet %d: %w", fleetID, err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]dayRow, 0)
	for rows.Next() {
		var r dayRow
		if err := rows.Scan(&r.DriverID, &r.JobsCompleted, &r.OnTimeCount, &r.OnTimeDenom, &r.DistanceM, &r.IdleS); err != nil {
			return nil, fmt.Errorf("scan fleet %d: %w", fleetID, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("compute fleet %d: %w", fleetID, err)
	}
	return out, nil
}
